package library

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all library routes onto the provided Chi router.
// Requires a valid JWT (enforced by AuthenticateHuma).
// Browsing requires ManageForms; import requires ManageForms too.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageForms := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageForms })
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "list-library-forms",
		Method:      http.MethodGet,
		Path:        "/api/v1/library/forms",
		Summary:     "List Salvia library forms",
		Description: "Returns all Salvia-authored form templates available for this clinic's vertical and country, each with its import status (not_imported | imported | retired).",
		Tags:        []string{"Library"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listLibraryForms)

	huma.Register(api, huma.Operation{
		OperationID: "list-library-policies",
		Method:      http.MethodGet,
		Path:        "/api/v1/library/policies",
		Summary:     "List Salvia library policies",
		Description: "Returns all Salvia-authored policy templates available for this clinic's vertical and country.",
		Tags:        []string{"Library"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.listLibraryPolicies)

	huma.Register(api, huma.Operation{
		OperationID: "get-library-form",
		Method:      http.MethodGet,
		Path:        "/api/v1/library/forms/{template_id}",
		Summary:     "Get library form detail",
		Description: "Returns the full template detail including field previews and recommended policies.",
		Tags:        []string{"Library"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.getLibraryForm)

	huma.Register(api, huma.Operation{
		OperationID: "get-library-policy",
		Method:      http.MethodGet,
		Path:        "/api/v1/library/policies/{template_id}",
		Summary:     "Get library policy detail",
		Description: "Returns the full template detail including clause previews.",
		Tags:        []string{"Library"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.getLibraryPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "import-library-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/library/forms/{template_id}/import",
		Summary:     "Import a form from the library",
		Description: "Promotes a Salvia form template into the clinic's draft list. Writes the YAML fields into a real draft. Idempotent: if the template was already imported, returns the existing form ID without changes.",
		Tags:        []string{"Library"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.importLibraryForm)

	huma.Register(api, huma.Operation{
		OperationID: "import-library-policy",
		Method:      http.MethodPost,
		Path:        "/api/v1/library/policies/{template_id}/import",
		Summary:     "Import a policy from the library",
		Description: "Promotes a Salvia policy template into the clinic's active policy list. Writes the YAML clauses into a real draft. Idempotent: if already imported, returns the existing policy ID.",
		Tags:        []string{"Library"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manageForms},
	}, h.importLibraryPolicy)
}
