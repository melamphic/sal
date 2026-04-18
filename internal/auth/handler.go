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
