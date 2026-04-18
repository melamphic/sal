package billing

import "github.com/go-chi/chi/v5"

// Mount registers the billing webhook on the provided Chi router.
// Uses raw Chi (not huma) because Stripe signature verification needs
// byte-exact access to the request body — huma's input binding
// consumes/rewraps it. No JWT middleware: Stripe's HMAC is the auth.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/v1/billing/webhook", h.ServeWebhook)
}
