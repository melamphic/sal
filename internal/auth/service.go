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

// StaffCreator creates a staff member record from an accepted invite.
// Implemented by an adapter in app.go that bridges to staff.Service.
type StaffCreator interface {
	CreateFromInvite(ctx context.Context, in CreateStaffFromInviteInput) (staffID uuid.UUID, err error)
}

// CreateStaffFromInviteInput holds the data for creating a staff member from an invite.
type CreateStaffFromInviteInput struct {
	ClinicID    uuid.UUID
	Email       string // plaintext
	FullName    string // from acceptance request
	Role        domain.StaffRole
	NoteTier    domain.NoteTier
	Permissions domain.Permissions
}

// HandoffProvisioner finds-or-creates the clinic + super admin staff for a
// /mel handoff JWT. Idempotent on email_hash so replaying the same email
// (with a fresh jti) returns the existing rows. Implemented in app.go by
// an adapter that bridges to clinic.Service + staff.Service.
type HandoffProvisioner interface {
	ProvisionFromHandoff(ctx context.Context, in HandoffProvisionInput) (clinicID, staffID uuid.UUID, err error)
}

// HandoffProvisionInput is the post-JWT-verify payload passed to the
// adapter. The adapter is responsible for clinic creation, staff
// creation, and idempotency.
type HandoffProvisionInput struct {
	Email            string // plaintext
	FullName         string
	ClinicName       string
	Vertical         domain.Vertical
	PlanCode         *domain.PlanCode // nil = trial signup, no plan yet
	StripeCustomerID *string          // cus_… from /mel Checkout; nil on trial
}

// Service handles all authentication business logic.
// It has no knowledge of HTTP — inputs and outputs are plain Go types.
type Service struct {
	repo               repo // interface — see repo.go
	cipher             *crypto.Cipher
	mailer             mailer.Mailer
	jwtSecret          []byte
	cfg                ServiceConfig
	staffCreator       StaffCreator       // nil = invite acceptance disabled
	handoffSecret      []byte             // shared HS256 secret with /mel; nil = handoff disabled
	handoffProvisioner HandoffProvisioner // nil = handoff disabled
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
func NewService(repo repo, cipher *crypto.Cipher, m mailer.Mailer, jwtSecret []byte, cfg ServiceConfig, staffCreator StaffCreator) *Service {
	return &Service{
		repo:         repo,
		cipher:       cipher,
		mailer:       m,
		jwtSecret:    jwtSecret,
		cfg:          cfg,
		staffCreator: staffCreator,
	}
}

// SetMelHandoff wires the /mel JWT handoff dependencies. Pass nil secret
// or nil provisioner to leave the feature disabled (the handoff endpoint
// then returns 503 — useful for environments where /mel is offline).
func (s *Service) SetMelHandoff(secret []byte, p HandoffProvisioner) {
	s.handoffSecret = secret
	s.handoffProvisioner = p
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
	firstName := "there"
	if fields := strings.Fields(name); len(fields) > 0 {
		firstName = fields[0]
	}

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

	if staff.Status == domain.StaffStatusDeactivated {
		return nil, domain.ErrForbidden
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

// CreateInviteToken generates an invite token, stores it, and returns the raw token.
// Called by staff.Service via adapter when an admin invites a new staff member.
func (s *Service) CreateInviteToken(ctx context.Context, clinicID uuid.UUID, email, emailHash string, role domain.StaffRole, noteTier domain.NoteTier, perms domain.Permissions, invitedByID uuid.UUID) (string, error) {
	rawToken, tokenHash, err := generateOpaqueToken()
	if err != nil {
		return "", fmt.Errorf("auth.service.CreateInviteToken: %w", err)
	}

	encEmail, err := s.cipher.Encrypt(email)
	if err != nil {
		return "", fmt.Errorf("auth.service.CreateInviteToken: encrypt email: %w", err)
	}

	expiresAt := domain.TimeNow().Add(7 * 24 * time.Hour) // 7-day expiry

	if err := s.repo.CreateInviteToken(ctx, CreateInviteParams{
		ID:          domain.NewID(),
		ClinicID:    clinicID,
		Email:       encEmail,
		EmailHash:   emailHash,
		Role:        role,
		NoteTier:    noteTier,
		Permissions: perms,
		TokenHash:   tokenHash,
		ExpiresAt:   expiresAt,
		InvitedByID: invitedByID,
	}); err != nil {
		return "", fmt.Errorf("auth.service.CreateInviteToken: %w", err)
	}

	return rawToken, nil
}

// AcceptInvite verifies an invite token, creates the staff record, and issues a JWT pair.
// The invited person provides their full name at acceptance time.
func (s *Service) AcceptInvite(ctx context.Context, rawToken, fullName string) (*TokenPair, error) {
	tokenHash := hashToken(rawToken)

	invite, err := s.repo.GetInviteByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("auth.service.AcceptInvite: %w", err)
	}

	// Decrypt the email stored in the invite.
	plainEmail, err := s.cipher.Decrypt(invite.Email)
	if err != nil {
		return nil, fmt.Errorf("auth.service.AcceptInvite: decrypt email: %w", err)
	}

	// Create the staff member via adapter.
	staffID, err := s.staffCreator.CreateFromInvite(ctx, CreateStaffFromInviteInput{
		ClinicID:    invite.ClinicID,
		Email:       plainEmail,
		FullName:    fullName,
		Role:        invite.Role,
		NoteTier:    invite.NoteTier,
		Permissions: invite.Permissions,
	})
	if err != nil {
		return nil, fmt.Errorf("auth.service.AcceptInvite: create staff: %w", err)
	}

	// Mark invite as accepted.
	if err := s.repo.MarkInviteAccepted(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth.service.AcceptInvite: mark accepted: %w", err)
	}

	// Look up the new staff record so we can issue a JWT.
	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err != nil {
		return nil, fmt.Errorf("auth.service.AcceptInvite: get staff: %w", err)
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
	//
	// We deliberately detach from the request context (so the update survives a
	// fast client-side cancel) but bound it with a short timeout so the
	// goroutine can't block during graceful shutdown / DB drain.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.repo.UpdateLastActive(bgCtx, staff.ID)
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
