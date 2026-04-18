package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// maxWebhookBody caps incoming Stripe payloads. Real webhooks are a few
// KB — a 1 MiB ceiling is generous and blocks signature-bomb attacks.
const maxWebhookBody = 1 << 20 // 1 MiB

// Handler wires billing HTTP endpoints to the billing Service.
// The webhook route is mounted via raw Chi (not huma) so the handler
// can read the exact request bytes Stripe signed.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler creates a new billing Handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// ServeWebhook implements POST /api/v1/billing/webhook.
//
// Returns 200 for both success AND replayed events (idempotent).
// Returns 400 for bad signatures (stops Stripe's retry). Returns 500
// for DB or adapter failures so Stripe retries.
func (h *Handler) ServeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, `{"error":"payload_too_large"}`, http.StatusRequestEntityTooLarge)
		return
	}

	sig := r.Header.Get("Stripe-Signature")
	if sig == "" {
		http.Error(w, `{"error":"missing_signature"}`, http.StatusBadRequest)
		return
	}

	if err := h.svc.HandleWebhook(r.Context(), body, sig); err != nil {
		if errors.Is(err, domain.ErrTokenInvalid) {
			h.log.WarnContext(r.Context(), "stripe webhook signature verify failed", "error", err)
			http.Error(w, `{"error":"invalid_signature"}`, http.StatusBadRequest)
			return
		}
		h.log.ErrorContext(r.Context(), "stripe webhook processing failed", "error", err)
		http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"received": true})
}

// ── Billing portal ────────────────────────────────────────────────────────

type portalSessionInput struct{}

type portalSessionResponse struct {
	Body struct {
		URL string `json:"url" doc:"One-shot hosted URL for the Stripe customer portal."`
	}
}

// createPortalSession handles POST /api/v1/billing/portal. Returns 400 when
// the clinic is still on trial (no stripe_customer_id) or when the portal
// feature is disabled at startup (no STRIPE_API_KEY).
func (h *Handler) createPortalSession(ctx context.Context, _ *portalSessionInput) (*portalSessionResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	url, err := h.svc.CreatePortalSession(ctx, clinicID)
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			return nil, huma.Error400BadRequest("billing portal unavailable — clinic has no active subscription")
		}
		h.log.ErrorContext(ctx, "billing portal session failed", "error", err)
		return nil, huma.Error500InternalServerError("internal server error")
	}

	resp := &portalSessionResponse{}
	resp.Body.URL = url
	return resp, nil
}
