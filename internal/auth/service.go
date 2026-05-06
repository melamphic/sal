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
	"golang.org/x/time/rate"
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
	signupCheckout     SignupCheckoutClient // nil = signup-checkout disabled (handoffSecret also required)
	signupCancelURL    string               // absolute /mel URL the browser lands on if Checkout is abandoned
	emailLimiter       *emailLimiter      // nil = no per-email rate limit (tests)
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

// EnableMagicLinkEmailLimit installs the per-email rate limiter used by
// SendMagicLink. Production wires this in app.go; tests may leave it nil to
// disable throttling. Wired separately from NewService so existing call
// sites (and the wide test surface) need no change.
func (s *Service) EnableMagicLinkEmailLimit() {
	// rate.Every(2*time.Minute) with burst=3 = up to 3 immediate sends, then
	// one every 2 minutes thereafter. Generous enough that a real user
	// re-requesting after a typo isn't blocked, tight enough that flooding a
	// victim's inbox via a botnet hits the cap fast.
	s.emailLimiter = newEmailLimiter(rate.Every(2*time.Minute), 3)
}

// StopBackgroundJobs halts goroutines started by the service (currently the
// email limiter sweeper). Safe to call when no limiter is installed.
func (s *Service) StopBackgroundJobs() {
	if s.emailLimiter != nil {
		s.emailLimiter.Stop()
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

	// Per-email rate limit: silently drop excess requests. Returning a 429 here
	// would leak which addresses are being flooded (and thus exist in our DB),
	// so we mirror the not-found path and return nil.
	if s.emailLimiter != nil && !s.emailLimiter.allow(emailHash) {
		return nil
	}

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

// ── Invite management (team page) ─────────────────────────────────────────────

// InviteListEntry is the decrypted view of one invite_tokens row plus
// derived status, returned to staff.Service for the team page.
//
// Status is computed from accepted_at / revoked_at / expires_at:
//   - "accepted" — accepted_at IS NOT NULL (rare in list view; see filter)
//   - "revoked"  — revoked_at  IS NOT NULL
//   - "expired"  — expires_at  < now
//   - "pending"  — otherwise
type InviteListEntry struct {
	ID            uuid.UUID
	Email         string
	Role          domain.StaffRole
	NoteTier      domain.NoteTier
	Permissions   domain.Permissions
	InvitedByID   uuid.UUID
	InvitedByName string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	AcceptedAt    *time.Time
	RevokedAt     *time.Time
	Status        string
}

// ListInvitesForClinic returns pending + expired invites for the team
// page. Accepted invites are filtered out at the repo layer because they
// already surface as active staff in /staff. Revoked invites are also
// hidden — the row stays on disk for audit but the UI never sees it.
//
// Email and inviter name are decrypted here so the staff package never
// touches another domain's cipher state.
func (s *Service) ListInvitesForClinic(ctx context.Context, clinicID uuid.UUID) ([]InviteListEntry, error) {
	rows, err := s.repo.ListInvitesByClinic(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("auth.service.ListInvitesForClinic: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	// Resolve inviter names in one pass; same staff often invited
	// many people, so memoize.
	nameCache := map[uuid.UUID]string{}
	out := make([]InviteListEntry, 0, len(rows))
	for _, r := range rows {
		email, err := s.cipher.Decrypt(r.Email)
		if err != nil {
			return nil, fmt.Errorf("auth.service.ListInvitesForClinic: decrypt email: %w", err)
		}
		inviterName, ok := nameCache[r.InvitedByID]
		if !ok {
			st, sErr := s.repo.GetStaffByID(ctx, r.InvitedByID)
			if sErr == nil {
				if n, dErr := s.cipher.Decrypt(st.FullName); dErr == nil {
					inviterName = n
				}
			}
			nameCache[r.InvitedByID] = inviterName
		}
		out = append(out, InviteListEntry{
			ID:            r.ID,
			Email:         email,
			Role:          r.Role,
			NoteTier:      r.NoteTier,
			Permissions:   r.Permissions,
			InvitedByID:   r.InvitedByID,
			InvitedByName: inviterName,
			CreatedAt:     r.CreatedAt,
			ExpiresAt:     r.ExpiresAt,
			AcceptedAt:    r.AcceptedAt,
			RevokedAt:     r.RevokedAt,
			Status:        deriveInviteStatus(r),
		})
	}
	return out, nil
}

// GetInviteForClinic fetches one invite by id, decrypted. Used by the
// resend handler to look up the invite shape (role/perms/email) before
// minting a new token.
func (s *Service) GetInviteForClinic(ctx context.Context, id, clinicID uuid.UUID) (*InviteListEntry, error) {
	r, err := s.repo.GetInviteByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("auth.service.GetInviteForClinic: %w", err)
	}
	email, err := s.cipher.Decrypt(r.Email)
	if err != nil {
		return nil, fmt.Errorf("auth.service.GetInviteForClinic: decrypt email: %w", err)
	}
	var inviterName string
	if st, sErr := s.repo.GetStaffByID(ctx, r.InvitedByID); sErr == nil {
		if n, dErr := s.cipher.Decrypt(st.FullName); dErr == nil {
			inviterName = n
		}
	}
	return &InviteListEntry{
		ID:            r.ID,
		Email:         email,
		Role:          r.Role,
		NoteTier:      r.NoteTier,
		Permissions:   r.Permissions,
		InvitedByID:   r.InvitedByID,
		InvitedByName: inviterName,
		CreatedAt:     r.CreatedAt,
		ExpiresAt:     r.ExpiresAt,
		AcceptedAt:    r.AcceptedAt,
		RevokedAt:     r.RevokedAt,
		Status:        deriveInviteStatus(r),
	}, nil
}

// RevokeInviteForClinic stamps revoked_at on an invite. Returns
// ErrNotFound if the row isn't pending (already accepted, already
// revoked, or wrong clinic).
func (s *Service) RevokeInviteForClinic(ctx context.Context, id, clinicID uuid.UUID) error {
	if err := s.repo.RevokeInviteByID(ctx, id, clinicID); err != nil {
		return fmt.Errorf("auth.service.RevokeInviteForClinic: %w", err)
	}
	return nil
}

// LoginEntry is the decrypted view of one consumed magic-link login,
// returned to the staff-activity aggregator. Decryption of the IP
// happens here so the staff package never touches our cipher.
type LoginEntry struct {
	ID     uuid.UUID
	UsedAt time.Time
	IP     string // best-effort; "" when unknown or undecryptable
}

// ListLoginsForStaff returns the consumed magic-link logins for a
// staff member, newest-first. Backs the per-staff activity feed.
func (s *Service) ListLoginsForStaff(ctx context.Context, staffID uuid.UUID, limit int) ([]LoginEntry, error) {
	rows, err := s.repo.ListLoginsByStaff(ctx, staffID, limit)
	if err != nil {
		return nil, fmt.Errorf("auth.service.ListLoginsForStaff: %w", err)
	}
	out := make([]LoginEntry, 0, len(rows))
	for _, r := range rows {
		var ip string
		if r.CreatedFromIP != nil && *r.CreatedFromIP != "" {
			if dec, dErr := s.cipher.Decrypt(*r.CreatedFromIP); dErr == nil {
				ip = dec
			}
		}
		out = append(out, LoginEntry{ID: r.ID, UsedAt: r.UsedAt, IP: ip})
	}
	return out, nil
}

// deriveInviteStatus mirrors the comment on InviteListEntry.Status —
// kept as a function so the same precedence applies in List + Get.
func deriveInviteStatus(r *inviteRow) string {
	switch {
	case r.AcceptedAt != nil:
		return "accepted"
	case r.RevokedAt != nil:
		return "revoked"
	case domain.TimeNow().After(r.ExpiresAt):
		return "expired"
	default:
		return "pending"
	}
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
