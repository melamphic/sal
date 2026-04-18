package timeline

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all timeline routes on the router.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "get-note-timeline",
		Method:      http.MethodGet,
		Path:        "/api/v1/notes/{note_id}/timeline",
		Summary:     "Get note timeline",
		Description: "Returns the ordered audit trail for a single clinical note, oldest event first.",
		Tags:        []string{"Timeline"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getNoteTimeline)

	huma.Register(api, huma.Operation{
		OperationID: "get-subject-timeline",
		Method:      http.MethodGet,
		Path:        "/api/v1/subjects/{subject_id}/timeline",
		Summary:     "Get subject timeline",
		Description: "Returns all note lifecycle events for a subject, ordered chronologically.",
		Tags:        []string{"Timeline"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getSubjectTimeline)

	huma.Register(api, huma.Operation{
		OperationID: "get-clinic-audit-log",
		Method:      http.MethodGet,
		Path:        "/api/v1/timeline",
		Summary:     "Clinic audit log",
		Description: "Returns the clinic-wide audit event stream. Requires generate_audit_export permission.",
		Tags:        []string{"Timeline"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getClinicAuditLog)
}
