package consent

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers consent endpoints.
//
// Permission matrix (v1):
//   - Read       → ViewAllPatients ∪ ViewOwnPatients ∪ ManagePatients
//   - Capture    → ManagePatients
//   - Update     → ManagePatients (limited fields — corrections + signature)
//   - Withdraw   → ManagePatients (with documented reason)
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	view := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ViewAllPatients || p.ViewOwnPatients || p.ManagePatients
	})
	manage := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID:   "capture-consent",
		Method:        http.MethodPost,
		Path:          "/api/v1/consent",
		Summary:       "Capture a new consent record",
		Description:   "Universal across all (vertical, country) combos. Per-type expiry defaults are applied automatically; clinics can override via expires_at.",
		Tags:          []string{"Consent"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manage},
		DefaultStatus: http.StatusCreated,
	}, h.captureConsent)

	huma.Register(api, huma.Operation{
		OperationID: "list-consents",
		Method:      http.MethodGet,
		Path:        "/api/v1/consent",
		Summary:     "List consent records (paginated, filterable)",
		Description: "Filters: subject_id, consent_type, only_active (excludes withdrawn + expired), expiring_within (Go duration).",
		Tags:        []string{"Consent"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.listConsents)

	huma.Register(api, huma.Operation{
		OperationID: "get-consent",
		Method:      http.MethodGet,
		Path:        "/api/v1/consent/{id}",
		Summary:     "Get a single consent record",
		Tags:        []string{"Consent"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.getConsent)

	huma.Register(api, huma.Operation{
		OperationID: "update-consent",
		Method:      http.MethodPatch,
		Path:        "/api/v1/consent/{id}",
		Summary:     "Update consent metadata (risks/alternatives/expiry/signature/witness)",
		Description: "Captured-at + consent-type + capture method are immutable post-creation; corrections live here and are reflected in updated_at.",
		Tags:        []string{"Consent"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manage},
	}, h.updateConsent)

	huma.Register(api, huma.Operation{
		OperationID: "withdraw-consent",
		Method:      http.MethodPost,
		Path:        "/api/v1/consent/{id}/withdraw",
		Summary:     "Withdraw a consent with a documented reason",
		Description: "Append-only against the row — original capture stays for audit; withdrawal_at + withdrawal_reason are stamped.",
		Tags:        []string{"Consent"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manage},
	}, h.withdrawConsent)
}
