package clinic

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all clinic routes onto the provided Chi router.
// Registration is public. All other clinic endpoints require authentication.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	// ── Public ────────────────────────────────────────────────────────────────
	huma.Register(api, huma.Operation{
		OperationID:   "register-clinic",
		Method:        http.MethodPost,
		Path:          "/api/v1/clinic/register",
		Summary:       "Register a new clinic",
		Description:   "Creates a new clinic account in trial status. The registrant becomes the super admin.",
		Tags:          []string{"Clinic"},
		DefaultStatus: http.StatusCreated,
	}, h.register)

	// ── Authenticated ─────────────────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(mw.Authenticate(jwtSecret))

		huma.Register(api, huma.Operation{
			OperationID: "get-clinic",
			Method:      http.MethodGet,
			Path:        "/api/v1/clinic",
			Summary:     "Get clinic",
			Description: "Returns the authenticated clinic's profile and settings.",
			Tags:        []string{"Clinic"},
			Security:    []map[string][]string{{"bearerAuth": {}}},
		}, h.get)

		huma.Register(api, huma.Operation{
			OperationID: "update-clinic",
			Method:      http.MethodPatch,
			Path:        "/api/v1/clinic",
			Summary:     "Update clinic settings",
			Description: "Updates mutable clinic settings. Requires manage_staff permission (admin or super_admin).",
			Tags:        []string{"Clinic"},
			Security:    []map[string][]string{{"bearerAuth": {}}},
			Middlewares: huma.Middlewares{
				mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageStaff }),
			},
		}, h.update)
	})
}
