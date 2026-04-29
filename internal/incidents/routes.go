package incidents

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all incident endpoints. Permission gating uses the
// existing domain.Permissions struct; granular incident perms are tracked
// for a later sprint (see migration 00062 for the column inventory).
//
// Permission matrix (v1):
//   - Read endpoints     → ViewAllPatients ∪ ViewOwnPatients
//   - Create / update    → ManagePatients
//   - Escalate / notify regulator → GenerateAuditExport (privacy-officer flow)
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	view := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ViewAllPatients || p.ViewOwnPatients || p.ManagePatients
	})
	manage := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients
	})
	escalate := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.GenerateAuditExport
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID:   "create-incident",
		Method:        http.MethodPost,
		Path:          "/api/v1/incidents",
		Summary:       "Log a new incident",
		Description:   "Auto-classifies SIRS priority (AU aged care) + CQC notifiable (UK aged care) and stamps the regulator-notification deadline.",
		Tags:          []string{"Incidents"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manage},
		DefaultStatus: http.StatusCreated,
	}, h.createIncident)

	huma.Register(api, huma.Operation{
		OperationID: "list-incidents",
		Method:      http.MethodGet,
		Path:        "/api/v1/incidents",
		Summary:     "List incidents (paginated, filterable)",
		Tags:        []string{"Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.listIncidents)

	huma.Register(api, huma.Operation{
		OperationID: "get-incident",
		Method:      http.MethodGet,
		Path:        "/api/v1/incidents/{id}",
		Summary:     "Get one incident with witnesses + addendums",
		Tags:        []string{"Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.getIncident)

	huma.Register(api, huma.Operation{
		OperationID: "update-incident",
		Method:      http.MethodPatch,
		Path:        "/api/v1/incidents/{id}",
		Summary:     "Update incident fields (severity, outcome, status, plan)",
		Tags:        []string{"Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manage},
	}, h.updateIncident)

	huma.Register(api, huma.Operation{
		OperationID: "escalate-incident",
		Method:      http.MethodPost,
		Path:        "/api/v1/incidents/{id}/escalate",
		Summary:     "Escalate an incident with a documented reason",
		Description: "Required for audit defensibility. Status flips to 'escalated'; the privacy officer follows up with the regulator-notification flow.",
		Tags:        []string{"Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, escalate},
	}, h.escalateIncident)

	huma.Register(api, huma.Operation{
		OperationID: "notify-regulator-incident",
		Method:      http.MethodPost,
		Path:        "/api/v1/incidents/{id}/notify-regulator",
		Summary:     "Mark an incident as reported to the regulator",
		Description: "Stamps regulator_notified_at and stores the reference number for audit citations.",
		Tags:        []string{"Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, escalate},
	}, h.notifyRegulator)

	huma.Register(api, huma.Operation{
		OperationID:   "add-incident-witness",
		Method:        http.MethodPost,
		Path:          "/api/v1/incidents/{id}/witnesses",
		Summary:       "Add a staff witness to an incident",
		Tags:          []string{"Incidents"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manage},
		DefaultStatus: http.StatusCreated,
	}, h.addWitness)

	huma.Register(api, huma.Operation{
		OperationID: "remove-incident-witness",
		Method:      http.MethodDelete,
		Path:        "/api/v1/incidents/{id}/witnesses/{staff_id}",
		Summary:     "Remove a staff witness from an incident",
		Tags:        []string{"Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manage},
	}, h.removeWitness)

	huma.Register(api, huma.Operation{
		OperationID:   "add-incident-addendum",
		Method:        http.MethodPost,
		Path:          "/api/v1/incidents/{id}/addendums",
		Summary:       "Append an addendum to an incident",
		Description:   "Append-only — the original incident description never changes; corrections + late information go here.",
		Tags:          []string{"Incidents"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manage},
		DefaultStatus: http.StatusCreated,
	}, h.addAddendum)
}
