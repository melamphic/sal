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
	// ── Public routes (no JWT required) ──────────────────────────────────────
	huma.Register(api, huma.Operation{
		OperationID: "request-magic-link",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/magic-link",
		Summary:     "Request a magic link",
		Description: "Sends a one-time login link to the provided email. Always returns 200 — does not reveal whether the email exists.",
		Tags:        []string{"Auth"},
		DefaultStatus: http.StatusOK,
	}, h.requestMagicLink)

	huma.Register(api, huma.Operation{
		OperationID: "verify-magic-link",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/verify",
		Summary:     "Verify a magic link token",
		Description: "Exchanges a one-time magic link token for an access and refresh token pair.",
		Tags:        []string{"Auth"},
	}, h.verifyToken)

	huma.Register(api, huma.Operation{
		OperationID: "refresh-tokens",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/refresh",
		Summary:     "Refresh access token",
		Description: "Exchanges a refresh token for a new access and refresh token pair. The old refresh token is invalidated.",
		Tags:        []string{"Auth"},
	}, h.refreshTokens)

	// ── Authenticated routes ──────────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(mw.Authenticate(jwtSecret))

		huma.Register(api, huma.Operation{
			OperationID: "logout",
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/logout",
			Summary:     "Logout",
			Description: "Invalidates all refresh tokens for the current staff member.",
			Tags:        []string{"Auth"},
			Security:    []map[string][]string{{"bearerAuth": {}}},
		}, h.logout)
	})
}
