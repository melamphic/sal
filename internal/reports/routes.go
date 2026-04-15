package reports

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all report routes on the router.
// All report endpoints require generate_audit_export permission.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	authMw := mw.Authenticate(jwtSecret)
	auditExport := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.GenerateAuditExport
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	r.Group(func(r chi.Router) {
		r.Use(authMw)

		huma.Register(api, huma.Operation{
			OperationID: "get-clinical-audit",
			Method:      http.MethodGet,
			Path:        "/api/v1/reports/clinical-audit",
			Summary:     "Clinical audit report",
			Description: "Returns a paginated audit trail of all note events for the clinic. Filter by date range, staff member, subject, or note. Requires generate_audit_export permission.",
			Tags:        []string{"Reports"},
			Security:    security,
			Middlewares: huma.Middlewares{auditExport},
		}, h.getClinicalAudit)

		huma.Register(api, huma.Operation{
			OperationID: "get-staff-actions",
			Method:      http.MethodGet,
			Path:        "/api/v1/reports/staff-actions",
			Summary:     "Staff actions report",
			Description: "Returns all note events performed by a specific staff member. staff_id query parameter is required. Requires generate_audit_export permission.",
			Tags:        []string{"Reports"},
			Security:    security,
			Middlewares: huma.Middlewares{auditExport},
		}, h.getStaffActions)

		huma.Register(api, huma.Operation{
			OperationID: "get-note-history",
			Method:      http.MethodGet,
			Path:        "/api/v1/reports/note-history/{note_id}",
			Summary:     "Note history report",
			Description: "Returns the complete event trail for a single clinical note, oldest first. Requires generate_audit_export permission.",
			Tags:        []string{"Reports"},
			Security:    security,
			Middlewares: huma.Middlewares{auditExport},
		}, h.getNoteHistory)

		huma.Register(api, huma.Operation{
			OperationID: "get-consent-log",
			Method:      http.MethodGet,
			Path:        "/api/v1/reports/consent-log",
			Summary:     "Consent log report",
			Description: "Returns all note submission events (reviews and sign-offs) for the clinic. Filter by date range or subject. Requires generate_audit_export permission.",
			Tags:        []string{"Reports"},
			Security:    security,
			Middlewares: huma.Middlewares{auditExport},
		}, h.getConsentLog)

		huma.Register(api, huma.Operation{
			OperationID:   "request-export",
			Method:        http.MethodPost,
			Path:          "/api/v1/reports/export",
			Summary:       "Request async export",
			Description:   "Enqueues a background job to generate a downloadable report file. Returns a job_id to poll. When complete, the response includes a presigned download URL valid for 1 hour. Requires generate_audit_export permission.",
			Tags:          []string{"Reports"},
			Security:      security,
			Middlewares:   huma.Middlewares{auditExport},
			DefaultStatus: http.StatusAccepted,
		}, h.requestExport)

		huma.Register(api, huma.Operation{
			OperationID: "get-export-job",
			Method:      http.MethodGet,
			Path:        "/api/v1/reports/export/{job_id}",
			Summary:     "Get export job status",
			Description: "Returns the status of an async export job. When status=complete, download_url contains a presigned S3 URL valid for 1 hour. Requires generate_audit_export permission.",
			Tags:        []string{"Reports"},
			Security:    security,
			Middlewares: huma.Middlewares{auditExport},
		}, h.getExportJob)
	})
}
