package verticals

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers verticals routes onto the provided Chi router.
// All routes require a valid JWT (enforced by AuthenticateHuma).
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "get-vertical-schema",
		Method:      http.MethodGet,
		Path:        "/api/v1/verticals/schema",
		Summary:     "Get vertical form schema",
		Description: "Returns the form schema for the authenticated clinic's vertical. Clients render Create/View/Edit patient forms generically by iterating the schema's fields.",
		Tags:        []string{"Verticals"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getSchema)
}
