package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/crypto"
	"github.com/melamphic/sal/internal/platform/mailer"
)

// Service handles all authentication business logic.
// It has no knowledge of HTTP — inputs and outputs are plain Go types.
type Service struct {
	repo      repo // interface — see repo.go
	cipher    *crypto.Cipher
	mailer    mailer.Mailer
	jwtSecret []byte
	cfg       ServiceConfig
}

// ServiceConfig holds auth-specific configuration values.
type ServiceConfig struct {
	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration
	MagicLinkTTL  time.Duration
	AppURL        string
}

// TokenPair holds an access and refresh token issued together.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// NewService creates a new auth Service.
func NewService(repo repo, cipher *crypto.Cipher, m mailer.Mailer, jwtSecret []byte, cfg ServiceConfig) *Service {
	return &Service{
		repo:      repo,
		cipher:    cipher,
		mailer:    m,
		jwtSecret: jwtSecret,
		cfg:       cfg,
	}
}

// SendMagicLink generates a one-time login link and emails it to the staff member.
// We always return success even if the email is not found (prevents email enumeration).
func (s *Service) SendMagicLink(ctx context.Context, email string, r *http.Request) error {
	emailHash := s.cipher.Hash(email)

	staff, err := s.repo.FindStaffByEmailHash(ctx, emailHash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Silent success — do not reveal whether the email exists.
			return nil
		}
		return fmt.Errorf("auth.service.SendMagicLink: find staff: %w", err)
	}

	if staff.Status == domain.StaffStatusDeactivated {
		// Silent success — deactivated accounts cannot log in.
		return nil
	}

	rawToken, tokenHash, err := generateOpaqueToken()
	if err != nil {
		return fmt.Errorf("auth.service.SendMagicLink: generate token: %w", err)
	}

	fromIP := extractIP(r)
	expiresAt := domain.TimeNow().Add(s.cfg.MagicLinkTTL)

	if err := s.repo.CreateAuthToken(ctx, staff.ID, tokenHash, "magic_link", fromIP, expiresAt); err != nil {
		return fmt.Errorf("auth.service.SendMagicLink: store token: %w", err)
	}

	loginURL := fmt.Sprintf("%s/auth/verify?token=%s", strings.TrimRight(s.cfg.AppURL, "/"), rawToken)

	// Decrypt the name for the email. If decryption fails, fall back to a generic greeting.
	name, err := s.cipher.Decrypt(staff.FullName)
	if err != nil {
		name = "there"
	}
	firstName := strings.Fields(name)[0]

	if err := s.mailer.SendMagicLink(ctx, email, firstName, loginURL); err != nil {
		return fmt.Errorf("auth.service.SendMagicLink: send email: %w", err)
	}

	return nil
}

// VerifyMagicLink consumes a magic link token and returns a JWT token pair.
func (s *Service) VerifyMagicLink(ctx context.Context, rawToken string) (*TokenPair, error) {
	tokenHash := hashToken(rawToken)

	tokenRow, err := s.repo.GetAndConsumeAuthToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("auth.service.VerifyMagicLink: %w", err)
	}

	if tokenRow.TokenType != "magic_link" {
		return nil, domain.ErrTokenInvalid
	}

	staff, err := s.repo.GetStaffByID(ctx, tokenRow.StaffID)
	if err != nil {
		return nil, fmt.Errorf("auth.service.VerifyMagicLink: get staff: %w", err)
	}

	return s.issueTokenPair(ctx, staff)
}

// RefreshTokens validates a refresh token and issues a new token pair.
func (s *Service) RefreshTokens(ctx context.Context, rawRefreshToken string) (*TokenPair, error) {
	tokenHash := hashToken(rawRefreshToken)

	tokenRow, err := s.repo.GetAndConsumeAuthToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("auth.service.RefreshTokens: %w", err)
	}

	if tokenRow.TokenType != "refresh" {
		return nil, domain.ErrTokenInvalid
	}

	staff, err := s.repo.GetStaffByID(ctx, tokenRow.StaffID)
	if err != nil {
		return nil, fmt.Errorf("auth.service.RefreshTokens: get staff: %w", err)
	}

	return s.issueTokenPair(ctx, staff)
}

// Logout invalidates all refresh tokens for the authenticated staff member.
func (s *Service) Logout(ctx context.Context, staffID uuid.UUID) error {
	if err := s.repo.DeleteRefreshTokensForStaff(ctx, staffID); err != nil {
		return fmt.Errorf("auth.service.Logout: %w", err)
	}
	return nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// issueTokenPair creates and stores a refresh token, then issues an access+refresh pair.
func (s *Service) issueTokenPair(ctx context.Context, staff *staffRow) (*TokenPair, error) {
	accessToken, err := issueAccessToken(
		staff.ID, staff.ClinicID, staff.Role, staff.Perms,
		s.jwtSecret, s.cfg.JWTAccessTTL,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.service.issueTokenPair: access token: %w", err)
	}

	rawRefresh, refreshHash, err := generateOpaqueToken()
	if err != nil {
		return nil, fmt.Errorf("auth.service.issueTokenPair: generate refresh: %w", err)
	}

	refreshExpiry := domain.TimeNow().Add(s.cfg.JWTRefreshTTL)
	if err := s.repo.CreateAuthToken(ctx, staff.ID, refreshHash, "refresh", "", refreshExpiry); err != nil {
		return nil, fmt.Errorf("auth.service.issueTokenPair: store refresh: %w", err)
	}

	// Update last_active_at in the background — non-critical, must not block login.
	// Note: we do NOT delete old refresh tokens here. Deletion only happens on
	// explicit logout. Multiple active sessions (e.g. mobile + desktop) are allowed.
	go func() {
		_ = s.repo.UpdateLastActive(context.Background(), staff.ID)
	}()

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresAt:    domain.TimeNow().Add(s.cfg.JWTAccessTTL),
	}, nil
}

func extractIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	return r.RemoteAddr
}
