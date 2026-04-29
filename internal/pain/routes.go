package pain

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers pain-score endpoints.
//
// Permission matrix (v1):
//   - Read    → ViewAllPatients ∪ ViewOwnPatients ∪ ManagePatients
//   - Record  → ManagePatients ∪ Dispense (caregivers / nurses already
//               carry these for the medication path; pain scoring is
//               part of the same workflow)
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	view := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ViewAllPatients || p.ViewOwnPatients || p.ManagePatients
	})
	record := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients || p.Dispense
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID:   "record-pain-score",
		Method:        http.MethodPost,
		Path:          "/api/v1/pain-scores",
		Summary:       "Record a pain score (0-10)",
		Description:   "Universal across all (vertical, country) combos. Pick a scale appropriate to the population: NRS for verbal, FLACC for non-verbal, PainAD for dementia, Wong-Baker for paediatric, VAS for procedure pain.",
		Tags:          []string{"Pain"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, record},
		DefaultStatus: http.StatusCreated,
	}, h.recordPain)

	huma.Register(api, huma.Operation{
		OperationID: "list-pain-scores",
		Method:      http.MethodGet,
		Path:        "/api/v1/pain-scores",
		Summary:     "List pain scores (paginated, filterable by subject + period)",
		Tags:        []string{"Pain"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.listPain)

	huma.Register(api, huma.Operation{
		OperationID: "get-pain-score",
		Method:      http.MethodGet,
		Path:        "/api/v1/pain-scores/{id}",
		Summary:     "Get one pain score",
		Tags:        []string{"Pain"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.getPain)

	huma.Register(api, huma.Operation{
		OperationID: "subject-pain-trend",
		Method:      http.MethodGet,
		Path:        "/api/v1/pain-scores/subjects/{subject_id}/trend",
		Summary:     "Aggregate pain trend for a subject (count, avg, latest, peak)",
		Description: "Defaults to the last 30 days. Used by the patient-hub trend chip and the pre-encounter brief.",
		Tags:        []string{"Pain"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, view},
	}, h.subjectTrend)
}
