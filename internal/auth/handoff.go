package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// MelHandoffClaims is the payload the /mel marketing site signs after
// successful Stripe checkout (or trial-no-card signup) and hands to
// Salvia via redirect to /api/v1/auth/handoff. See spec-v2-addendum
// §12.5 — these field names are part of the contract with /mel.
type MelHandoffClaims struct {
	jwt.RegisteredClaims
	// Email is the admin's work email — becomes the clinic primary
	// contact + first super-admin email. Required.
	Email string `json:"email"`
	// FullName is the admin's display name. Required.
	FullName string `json:"full_name"`
	// ClinicName is the clinic's display name shown across the app.
	// Required.
	ClinicName string `json:"clinic_name"`
	// Vertical pins which product the clinic signed up for. Required —
	// drives clinic.vertical and downstream form schemas. One of
	// veterinary, dental, general_clinic.
	Vertical domain.Vertical `json:"vertical"`
	// PlanCode is the billing SKU the customer purchased (e.g.
	// `paws_practice_monthly`). Empty for trial signups that have not
	// yet picked a plan — clinic stays in `trial` status until Stripe
	// webhook fills it on subscription.created.
	PlanCode string `json:"plan_code,omitempty"`
	// StripeCustomerID is the `cus_…` id /mel created at Stripe
	// Checkout. Required for paid-at-signup so the webhook can map
	// `subscription.created` → clinic. Empty on trial signups that
	// have not yet entered Stripe — the later upgrade flow (B4) will
	// write this via Salvia-owned Checkout Session metadata.
	StripeCustomerID string `json:"stripe_customer_id,omitempty"`
}

// HandoffFromMel verifies a /mel handoff JWT, provisions clinic + super
// admin (idempotent on email_hash via the HandoffProvisioner adapter),
// and issues a Salvia session. Single-use enforcement on jti — replays
// return domain.ErrTokenUsed. Token expiry returns domain.ErrTokenExpired.
// Bad signatures or malformed payloads return domain.ErrTokenInvalid.
func (s *Service) HandoffFromMel(ctx context.Context, rawJWT string) (*TokenPair, error) {
	if s.handoffSecret == nil || s.handoffProvisioner == nil {
		return nil, fmt.Errorf("auth.service.HandoffFromMel: %w", domain.ErrUnauthorized)
	}

	claims, err := s.parseHandoffJWT(rawJWT)
	if err != nil {
		return nil, fmt.Errorf("auth.service.HandoffFromMel: %w", err)
	}

	// Single-use jti — record before doing any side-effects so a
	// replayed token cannot trigger duplicate clinic provisioning even
	// in a race.
	if err := s.repo.ConsumeMelHandoffToken(ctx, claims.ID, claims.ExpiresAt.Time); err != nil {
		return nil, fmt.Errorf("auth.service.HandoffFromMel: consume jti: %w", err)
	}

	// Plan code is optional (trial signup leaves it empty). When set,
	// reject unknown codes — feeding a phantom plan into billing would
	// silently mis-bill the customer.
	var planCode *domain.PlanCode
	if claims.PlanCode != "" {
		pc := domain.PlanCode(claims.PlanCode)
		if _, ok := domain.PlanFor(pc); !ok {
			return nil, fmt.Errorf("auth.service.HandoffFromMel: %w: unknown plan_code", domain.ErrTokenInvalid)
		}
		planCode = &pc
	}

	var stripeCustomerID *string
	if claims.StripeCustomerID != "" {
		cid := claims.StripeCustomerID
		stripeCustomerID = &cid
	}

	clinicID, staffID, err := s.handoffProvisioner.ProvisionFromHandoff(ctx, HandoffProvisionInput{
		Email:            claims.Email,
		FullName:         claims.FullName,
		ClinicName:       claims.ClinicName,
		Vertical:         claims.Vertical,
		PlanCode:         planCode,
		StripeCustomerID: stripeCustomerID,
	})
	if err != nil {
		return nil, fmt.Errorf("auth.service.HandoffFromMel: provision: %w", err)
	}
	_ = clinicID // ClinicID is read off the returned staff row when minting tokens.

	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err != nil {
		return nil, fmt.Errorf("auth.service.HandoffFromMel: load staff: %w", err)
	}

	return s.issueTokenPair(ctx, staff)
}

// parseHandoffJWT verifies the HS256 signature, enforces required
// claims, and returns the decoded payload. All failure modes map to
// domain.ErrToken* sentinels so the handler can translate uniformly.
func (s *Service) parseHandoffJWT(raw string) (*MelHandoffClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, domain.ErrTokenInvalid
	}

	tok, err := jwt.ParseWithClaims(raw, &MelHandoffClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return s.handoffSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))

	if err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return nil, domain.ErrTokenExpired
		case errors.Is(err, jwt.ErrTokenNotValidYet),
			errors.Is(err, jwt.ErrTokenSignatureInvalid),
			errors.Is(err, jwt.ErrTokenMalformed),
			errors.Is(err, jwt.ErrTokenUnverifiable):
			return nil, domain.ErrTokenInvalid
		default:
			return nil, domain.ErrTokenInvalid
		}
	}

	claims, ok := tok.Claims.(*MelHandoffClaims)
	if !ok || !tok.Valid {
		return nil, domain.ErrTokenInvalid
	}

	if claims.ID == "" {
		// jti is required for replay protection.
		return nil, fmt.Errorf("%w: missing jti", domain.ErrTokenInvalid)
	}
	if claims.ExpiresAt == nil {
		return nil, fmt.Errorf("%w: missing exp", domain.ErrTokenInvalid)
	}
	if claims.Email == "" || claims.FullName == "" || claims.ClinicName == "" {
		return nil, fmt.Errorf("%w: missing required field", domain.ErrTokenInvalid)
	}
	switch claims.Vertical {
	case domain.VerticalVeterinary, domain.VerticalDental, domain.VerticalGeneralClinic:
		// OK — aged_care intentionally excluded; not launched per
		// spec-v2-addendum §1.1.
	default:
		return nil, fmt.Errorf("%w: unsupported vertical", domain.ErrTokenInvalid)
	}

	return claims, nil
}

// signupHandoffTTL is how long a minted /mel handoff JWT is valid. Long
// enough to survive a redirect + clock skew, short enough that a leaked
// token is useless within minutes.
const signupHandoffTTL = 10 * time.Minute

// StartSignupInput is the payload the public signup/start endpoint mints
// a handoff JWT from. Mirrors the /mel marketing site contract so the
// frontend can redirect straight into /auth/handoff.
type StartSignupInput struct {
	Email      string
	FullName   string
	ClinicName string
	Vertical   domain.Vertical
	PlanCode   string // optional — empty for trial-only
}

// StartSignupResult is the value the caller (static /mel site) uses to
// bounce the browser into the Salvia app.
type StartSignupResult struct {
	// HandoffURL is "<AppURL>/auth/handoff?token=<jwt>". Single-use.
	HandoffURL string
	// ExpiresAt is the absolute expiry of the minted JWT.
	ExpiresAt time.Time
}

// StartSignup mints a single-use handoff JWT for a /mel-originated signup
// and returns the absolute URL to redirect the browser to. The JWT is
// later consumed by HandoffFromMel — provisioning + session issuance
// happen there, not here.
func (s *Service) StartSignup(_ context.Context, in StartSignupInput) (*StartSignupResult, error) {
	if s.handoffSecret == nil {
		return nil, fmt.Errorf("auth.service.StartSignup: %w", domain.ErrUnauthorized)
	}
	if s.cfg.AppURL == "" {
		return nil, fmt.Errorf("auth.service.StartSignup: app URL not configured")
	}

	switch in.Vertical {
	case domain.VerticalVeterinary, domain.VerticalDental, domain.VerticalGeneralClinic:
	default:
		return nil, fmt.Errorf("auth.service.StartSignup: %w: unsupported vertical", domain.ErrTokenInvalid)
	}

	if in.PlanCode != "" {
		if _, ok := domain.PlanFor(domain.PlanCode(in.PlanCode)); !ok {
			return nil, fmt.Errorf("auth.service.StartSignup: %w: unknown plan_code", domain.ErrTokenInvalid)
		}
	}

	now := domain.TimeNow()
	exp := now.Add(signupHandoffTTL)

	claims := MelHandoffClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Email:      in.Email,
		FullName:   in.FullName,
		ClinicName: in.ClinicName,
		Vertical:   in.Vertical,
		PlanCode:   in.PlanCode,
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.handoffSecret)
	if err != nil {
		return nil, fmt.Errorf("auth.service.StartSignup: sign: %w", err)
	}

	u, err := url.Parse(strings.TrimRight(s.cfg.AppURL, "/") + "/auth/handoff")
	if err != nil {
		return nil, fmt.Errorf("auth.service.StartSignup: build url: %w", err)
	}
	q := u.Query()
	q.Set("token", raw)
	u.RawQuery = q.Encode()

	return &StartSignupResult{HandoffURL: u.String(), ExpiresAt: exp}, nil
}
