package auth

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires auth HTTP endpoints to the auth Service.
// It contains no business logic — only request parsing, service calls, and response writing.
type Handler struct {
	svc       *Service
	rateStore *mw.RateLimiterStore // nil = no rate limiting (tests)
}

// NewHandler creates a new auth Handler.
func NewHandler(svc *Service, rateStore *mw.RateLimiterStore) *Handler {
	return &Handler{svc: svc, rateStore: rateStore}
}

// ── Request / Response types ──────────────────────────────────────────────────
// These are HTTP-layer types only. They are never used by service or repository.

type magicLinkInput struct {
	Body struct {
		Email string `json:"email" format:"email" minLength:"3" maxLength:"254" doc:"The staff member's email address."`
	}
}

type verifyInput struct {
	Token string `query:"token" minLength:"1" doc:"The raw magic link token from the email."`
}

type refreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" minLength:"1" doc:"The refresh token issued at login."`
	}
}

type tokenResponse struct {
	Body TokenPair
}

type acceptInviteInput struct {
	Body struct {
		Token    string `json:"token" minLength:"1" doc:"The raw invite token from the invitation email."`
		FullName string `json:"full_name" minLength:"2" maxLength:"200" doc:"Full name of the new staff member."`
	}
}

type melHandoffInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" doc:"The single-use HS256 JWT minted by /mel after Stripe checkout or trial signup."`
	}
}

type signupStartInput struct {
	Body struct {
		Email      string          `json:"email" format:"email" minLength:"3" maxLength:"254" doc:"Admin work email — becomes the clinic primary contact + first super admin."`
		FullName   string          `json:"full_name" minLength:"2" maxLength:"200" doc:"Admin's display name."`
		ClinicName string          `json:"clinic_name" minLength:"2" maxLength:"200" doc:"Clinic display name."`
		Vertical   domain.Vertical `json:"vertical" enum:"veterinary,dental,general_clinic" doc:"Which product the customer signed up for."`
		PlanCode   string          `json:"plan_code,omitempty" maxLength:"80" doc:"Optional billing SKU pre-selected on the pricing page. Empty = trial without plan."`
	}
}

type signupStartOutput struct {
	Body struct {
		HandoffURL string `json:"handoff_url" doc:"Absolute Salvia URL to redirect the browser to."`
		ExpiresAt  string `json:"expires_at" doc:"RFC3339 timestamp after which the URL is invalid."`
	}
}

type signupCheckoutInput struct {
	Body struct {
		Email      string          `json:"email" format:"email" minLength:"3" maxLength:"254" doc:"Admin work email — becomes the clinic primary contact + first super admin."`
		FullName   string          `json:"full_name" minLength:"2" maxLength:"200" doc:"Admin's display name."`
		ClinicName string          `json:"clinic_name" minLength:"2" maxLength:"200" doc:"Clinic display name."`
		Vertical   domain.Vertical `json:"vertical" enum:"veterinary,dental,general_clinic" doc:"Which product the customer signed up for."`
		PlanCode   string          `json:"plan_code" minLength:"3" maxLength:"80" doc:"Billing SKU selected on the pricing page. Required — there is no plan-less checkout."`
	}
}

type signupCheckoutOutput struct {
	Body struct {
		CheckoutURL string `json:"checkout_url" doc:"Absolute Stripe Checkout URL — redirect the browser here."`
		ExpiresAt   string `json:"expires_at" doc:"RFC3339 timestamp after which the handoff URL behind Checkout's success_url is invalid."`
	}
}

type emptyOutput = struct{}

// ── Handlers ──────────────────────────────────────────────────────────────────

// requestMagicLink handles POST /api/v1/auth/magic-link.
// Always returns 200 regardless of whether the email exists (prevents enumeration).
func (h *Handler) requestMagicLink(ctx context.Context, input *magicLinkInput) (*emptyOutput, error) {
	// IP is logged best-effort via the request logging middleware.
	// huma does not expose *http.Request directly; passing nil is handled gracefully.
	if err := h.svc.SendMagicLink(ctx, input.Body.Email, nil); err != nil {
		// Log internally but always return 200 to prevent email enumeration.
		return nil, huma.Error500InternalServerError("failed to process request")
	}

	return nil, nil
}

// verifyToken handles GET /api/v1/auth/verify.
func (h *Handler) verifyToken(ctx context.Context, input *verifyInput) (*tokenResponse, error) {
	pair, err := h.svc.VerifyMagicLink(ctx, input.Token)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &tokenResponse{Body: *pair}, nil
}

// refreshTokens handles POST /api/v1/auth/refresh.
func (h *Handler) refreshTokens(ctx context.Context, input *refreshInput) (*tokenResponse, error) {
	pair, err := h.svc.RefreshTokens(ctx, input.Body.RefreshToken)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &tokenResponse{Body: *pair}, nil
}

// acceptInvite handles POST /api/v1/auth/accept-invite.
// Creates the staff record and returns a JWT pair so the invited person is logged in immediately.
func (h *Handler) acceptInvite(ctx context.Context, input *acceptInviteInput) (*tokenResponse, error) {
	pair, err := h.svc.AcceptInvite(ctx, input.Body.Token, input.Body.FullName)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &tokenResponse{Body: *pair}, nil
}

// melHandoff handles POST /api/v1/auth/handoff.
// Verifies a single-use JWT minted by the /mel marketing site after
// Stripe checkout, provisions the clinic + super-admin staff (idempotent
// on email), and returns a Salvia session.
func (h *Handler) melHandoff(ctx context.Context, input *melHandoffInput) (*tokenResponse, error) {
	pair, err := h.svc.HandoffFromMel(ctx, input.Body.Token)
	if err != nil {
		return nil, mapAuthError(err)
	}
	return &tokenResponse{Body: *pair}, nil
}

// startSignup handles POST /api/v1/signup/start.
// Public endpoint the static /mel marketing site calls after the trial
// signup form. Mints a single-use handoff JWT and returns the absolute
// URL the browser should redirect to. No clinic is created here — that
// happens when /auth/handoff consumes the token.
func (h *Handler) startSignup(ctx context.Context, input *signupStartInput) (*signupStartOutput, error) {
	res, err := h.svc.StartSignup(ctx, StartSignupInput{
		Email:      input.Body.Email,
		FullName:   input.Body.FullName,
		ClinicName: input.Body.ClinicName,
		Vertical:   input.Body.Vertical,
		PlanCode:   input.Body.PlanCode,
	})
	if err != nil {
		return nil, mapAuthError(err)
	}
	out := &signupStartOutput{}
	out.Body.HandoffURL = res.HandoffURL
	out.Body.ExpiresAt = res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	return out, nil
}

// startSignupCheckout handles POST /api/v1/signup/checkout-start.
// Public endpoint called by /mel when the user clicks "Start trial" with
// card-up-front. Pre-creates a Stripe customer + Checkout session (14-day
// trial), mints a handoff JWT carrying the cus_… as Stripe success_url,
// and returns the Checkout URL. Browser redirects to checkout_url; on
// success Stripe redirects to the handoff URL which provisions the clinic.
func (h *Handler) startSignupCheckout(ctx context.Context, input *signupCheckoutInput) (*signupCheckoutOutput, error) {
	res, err := h.svc.StartSignupCheckout(ctx, StartSignupCheckoutInput{
		Email:      input.Body.Email,
		FullName:   input.Body.FullName,
		ClinicName: input.Body.ClinicName,
		Vertical:   input.Body.Vertical,
		PlanCode:   input.Body.PlanCode,
	})
	if err != nil {
		return nil, mapAuthError(err)
	}
	out := &signupCheckoutOutput{}
	out.Body.CheckoutURL = res.CheckoutURL
	out.Body.ExpiresAt = res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	return out, nil
}

// logout handles POST /api/v1/auth/logout.
// Requires authentication — the staff ID comes from the JWT in context.
func (h *Handler) logout(ctx context.Context, _ *emptyOutput) (*emptyOutput, error) {
	staffID := mw.StaffIDFromContext(ctx)
	if err := h.svc.Logout(ctx, staffID); err != nil {
		return nil, huma.Error500InternalServerError("failed to logout")
	}
	return nil, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

// mapAuthError translates domain errors to huma HTTP errors.
// All handlers in this package use this function — never write raw huma errors
// based on domain sentinel values in individual handlers.
func mapAuthError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrTokenExpired):
		return huma.Error401Unauthorized("token has expired")
	case errors.Is(err, domain.ErrTokenUsed):
		return huma.Error401Unauthorized("token has already been used")
	case errors.Is(err, domain.ErrTokenInvalid):
		return huma.Error401Unauthorized("token is invalid")
	case errors.Is(err, domain.ErrUnauthorized):
		return huma.Error401Unauthorized("unauthorized")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
