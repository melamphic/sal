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
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageStaff := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageStaff })

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
	huma.Register(api, huma.Operation{
		OperationID: "get-clinic",
		Method:      http.MethodGet,
		Path:        "/api/v1/clinic",
		Summary:     "Get clinic",
		Description: "Returns the authenticated clinic's profile and settings.",
		Tags:        []string{"Clinic"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "update-clinic",
		Method:      http.MethodPatch,
		Path:        "/api/v1/clinic",
		Summary:     "Update clinic settings",
		Description: "Updates mutable clinic settings. Requires manage_staff permission (admin or super_admin).",
		Tags:        []string{"Clinic"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.update)

	huma.Register(api, huma.Operation{
		OperationID: "submit-clinic-compliance",
		Method:      http.MethodPost,
		Path:        "/api/v1/clinic/compliance",
		Summary:     "Submit compliance attestation",
		Description: "Records the privacy-officer details and required attestations (cross-border, AI oversight, patient consent, DPA) for the onboarding compliance step. Idempotent — re-submission updates the existing record. Requires manage_staff.",
		Tags:        []string{"Clinic"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.submitCompliance)

	huma.Register(api, huma.Operation{
		OperationID: "upload-clinic-logo",
		Method:      http.MethodPost,
		Path:        "/api/v1/clinic/logo",
		Summary:     "Upload clinic logo",
		Description: "Multipart upload (field \"file\"). Stores the logo in object storage and returns the updated clinic with a freshly-signed logo_url. Requires manage_staff permission.",
		Tags:        []string{"Clinic"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.uploadLogo)
}
