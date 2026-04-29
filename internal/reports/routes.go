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
	auth := mw.AuthenticateHuma(api, jwtSecret)
	auditExport := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.GenerateAuditExport
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "get-clinical-audit",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/clinical-audit",
		Summary:     "Clinical audit report",
		Description: "Returns a paginated audit trail of all note events for the clinic. Filter by date range, staff member, subject, or note. Requires generate_audit_export permission.",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getClinicalAudit)

	huma.Register(api, huma.Operation{
		OperationID: "get-staff-actions",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/staff-actions",
		Summary:     "Staff actions report",
		Description: "Returns all note events performed by a specific staff member. staff_id query parameter is required. Requires generate_audit_export permission.",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getStaffActions)

	huma.Register(api, huma.Operation{
		OperationID: "get-note-history",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/note-history/{note_id}",
		Summary:     "Note history report",
		Description: "Returns the complete event trail for a single clinical note, oldest first. Requires generate_audit_export permission.",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getNoteHistory)

	huma.Register(api, huma.Operation{
		OperationID: "get-consent-log",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/consent-log",
		Summary:     "Consent log report",
		Description: "Returns all note submission events (reviews and sign-offs) for the clinic. Filter by date range or subject. Requires generate_audit_export permission.",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getConsentLog)

	huma.Register(api, huma.Operation{
		OperationID:   "request-export",
		Method:        http.MethodPost,
		Path:          "/api/v1/reports/export",
		Summary:       "Request async export",
		Description:   "Enqueues a background job to generate a downloadable report file. Returns a job_id to poll. When complete, the response includes a presigned download URL valid for 1 hour. Requires generate_audit_export permission.",
		Tags:          []string{"Reports"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, auditExport},
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
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getExportJob)

	// ── Compliance reports (regulator-facing PDFs) ────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "request-compliance-report",
		Method:        http.MethodPost,
		Path:          "/api/v1/reports/compliance",
		Summary:       "Request a compliance report (PDF / ZIP)",
		Description:   "Vertical- and country-agnostic regulator-facing report. Type slugs: audit_pack, controlled_drugs_register. The clinic's vertical + country select the right regulator template inside the PDF builder. Returns a queued report row; poll GET /api/v1/reports/compliance/{id} for status.",
		Tags:          []string{"Reports"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, auditExport},
		DefaultStatus: http.StatusAccepted,
	}, h.requestComplianceReport)

	huma.Register(api, huma.Operation{
		OperationID: "list-compliance-reports",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/compliance",
		Summary:     "List compliance reports",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.listComplianceReports)

	huma.Register(api, huma.Operation{
		OperationID: "get-compliance-report",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/compliance/{id}",
		Summary:     "Get one compliance report",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getComplianceReport)

	huma.Register(api, huma.Operation{
		OperationID: "download-compliance-report",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/compliance/{id}/download",
		Summary:     "Download a completed compliance report",
		Description: "Returns the report row enriched with a fresh presigned URL valid for 1 hour. Writes an audit row to report_audit.",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.downloadComplianceReport)

	// ── Recurring schedules (D2) ──────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID:   "create-report-schedule",
		Method:        http.MethodPost,
		Path:          "/api/v1/reports/schedules",
		Summary:       "Schedule a recurring compliance report",
		Description:   "Creates a recurring trigger that fires (daily/weekly/monthly/quarterly), generates the configured report, and emails the recipients. The first fire is at the next period boundary after creation.",
		Tags:          []string{"Reports"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, auditExport},
		DefaultStatus: http.StatusCreated,
	}, h.createSchedule)

	huma.Register(api, huma.Operation{
		OperationID: "list-report-schedules",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/schedules",
		Summary:     "List recurring report schedules",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.listSchedules)

	huma.Register(api, huma.Operation{
		OperationID: "get-report-schedule",
		Method:      http.MethodGet,
		Path:        "/api/v1/reports/schedules/{id}",
		Summary:     "Get one schedule",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.getSchedule)

	huma.Register(api, huma.Operation{
		OperationID: "update-report-schedule",
		Method:      http.MethodPatch,
		Path:        "/api/v1/reports/schedules/{id}",
		Summary:     "Update recipients and/or paused state",
		Description: "Frequency is immutable; pause + delete + recreate to switch.",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.updateSchedule)

	huma.Register(api, huma.Operation{
		OperationID: "delete-report-schedule",
		Method:      http.MethodDelete,
		Path:        "/api/v1/reports/schedules/{id}",
		Summary:     "Delete a recurring schedule",
		Tags:        []string{"Reports"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, auditExport},
	}, h.deleteSchedule)
}
