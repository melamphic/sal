package notifications

import (
	"github.com/go-chi/chi/v5"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers the SSE endpoint on the Chi router.
// Uses raw Chi (not huma) because SSE requires full control of the response writer.
func (h *Handler) Mount(r chi.Router, jwtSecret []byte) {
	authMw := mw.Authenticate(jwtSecret)

	r.Group(func(r chi.Router) {
		r.Use(authMw)
		r.Get("/api/v1/events", h.ServeSSE)
	})
}
