package billing

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers the billing webhook + authenticated portal endpoint on
// the provided router. The webhook goes through raw Chi (Stripe signature
// needs byte-exact body access — huma's input binding consumes it); the
// portal endpoint goes through huma with JWT auth + manage_billing perm.
//
// The portal endpoint is a no-op at the mount level when STRIPE_API_KEY is
// unset — the service returns domain.ErrValidation so the handler 400s,
// which is clearer than hiding the route.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	r.Post("/api/v1/billing/webhook", h.ServeWebhook)

	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageBilling := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManageBilling
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-billing-portal-session",
		Method:      http.MethodPost,
		Path:        "/api/v1/billing/portal",
		Summary:     "Create a Stripe customer portal session",
		Description: "Returns a one-shot URL to the Stripe-hosted portal for managing the clinic's subscription, payment method and invoices. Requires manage_billing. Fails with 400 if the clinic is still on a trial with no Stripe customer attached.",
		Tags:        []string{"Billing"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageBilling},
	}, h.createPortalSession)
}
