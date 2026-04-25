package auth

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all auth routes onto the provided Chi router.
// Public routes (magic link, verify, refresh) require no authentication.
// The logout route requires a valid access token.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)

	// Rate limit middleware for public auth endpoints (nil-safe: skipped in tests).
	var rl huma.Middlewares
	if h.rateStore != nil {
		rl = huma.Middlewares{mw.RateLimitHuma(api, h.rateStore)}
	}

	// ── Public routes (no JWT required) ──────────────────────────────────────
	huma.Register(api, huma.Operation{
		OperationID:   "request-magic-link",
		Method:        http.MethodPost,
		Path:          "/api/v1/auth/magic-link",
		Summary:       "Request a magic link",
		Description:   "Sends a one-time login link to the provided email. Always returns 200 — does not reveal whether the email exists.",
		Tags:          []string{"Auth"},
		DefaultStatus: http.StatusOK,
		Middlewares:   rl,
	}, h.requestMagicLink)

	huma.Register(api, huma.Operation{
		OperationID: "verify-magic-link",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/verify",
		Summary:     "Verify a magic link token",
		Description: "Exchanges a one-time magic link token for an access and refresh token pair.",
		Tags:        []string{"Auth"},
		Middlewares: rl,
	}, h.verifyToken)

	huma.Register(api, huma.Operation{
		OperationID: "refresh-tokens",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/refresh",
		Summary:     "Refresh access token",
		Description: "Exchanges a refresh token for a new access and refresh token pair. The old refresh token is invalidated.",
		Tags:        []string{"Auth"},
		Middlewares: rl,
	}, h.refreshTokens)

	huma.Register(api, huma.Operation{
		OperationID: "accept-invite",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/accept-invite",
		Summary:     "Accept a staff invitation",
		Description: "Verifies the invite token, creates the staff record, and returns an access and refresh token pair. The invited person provides their full name.",
		Tags:        []string{"Auth"},
		Middlewares: rl,
	}, h.acceptInvite)

	huma.Register(api, huma.Operation{
		OperationID: "mel-handoff",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/handoff",
		Summary:     "Consume a /mel handoff token",
		Description: "Verifies the single-use JWT minted by the /mel marketing site after Stripe checkout (or trial signup), provisions the clinic + super-admin (idempotent on email), and returns a Salvia session.",
		Tags:        []string{"Auth"},
		Middlewares: rl,
	}, h.melHandoff)

	huma.Register(api, huma.Operation{
		OperationID: "start-signup",
		Method:      http.MethodPost,
		Path:        "/api/v1/signup/start",
		Summary:     "Mint a handoff URL for a new trial signup",
		Description: "Public endpoint called by the /mel marketing site. Mints a single-use handoff JWT and returns an absolute URL the browser should redirect to. No clinic is created here — provisioning happens when /api/v1/auth/handoff consumes the token.",
		Tags:        []string{"Auth"},
		Middlewares: rl,
	}, h.startSignup)

	huma.Register(api, huma.Operation{
		OperationID: "start-signup-checkout",
		Method:      http.MethodPost,
		Path:        "/api/v1/signup/checkout-start",
		Summary:     "Start a card-up-front signup via Stripe Checkout",
		Description: "Public endpoint called by /mel when the user picks a paid plan with card-up-front. Pre-creates a Stripe customer + Checkout session (14-day trial) and returns the hosted Checkout URL. On success, Stripe redirects to the Salvia handoff URL embedded in success_url, which provisions the clinic.",
		Tags:        []string{"Auth"},
		Middlewares: rl,
	}, h.startSignupCheckout)

	// ── Authenticated routes ──────────────────────────────────────────────────
	huma.Register(api, huma.Operation{
		OperationID: "logout",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/logout",
		Summary:     "Logout",
		Description: "Invalidates all refresh tokens for the current staff member.",
		Tags:        []string{"Auth"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.logout)
}
