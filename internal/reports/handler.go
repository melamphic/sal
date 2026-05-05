package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
	"github.com/melamphic/sal/internal/platform/storage"
)

// Handler wires report HTTP endpoints to the Service.
type Handler struct {
	svc   *Service
	store *storage.Store
}

// NewHandler creates a new reports Handler.
func NewHandler(svc *Service, store *storage.Store) *Handler {
	return &Handler{svc: svc, store: store}
}

// ── Shared types ──────────────────────────────────────────────────────────────

type reportPaginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20"`
	Offset int `query:"offset" minimum:"0" default:"0"`
}

type reportFilterInput struct {
	From      string `query:"from"       doc:"ISO 8601 start datetime (inclusive)."`
	To        string `query:"to"         doc:"ISO 8601 end datetime (inclusive)."`
	StaffID   string `query:"staff_id"   doc:"Filter by actor staff UUID."`
	SubjectID string `query:"subject_id" doc:"Filter by subject UUID."`
}

type auditHTTPResponse struct {
	Body *AuditReportResponse
}

type jobHTTPResponse struct {
	Body *ReportJobResponse
}

// ── Handlers ──────────────────────────────────────────────────────────────────

type clinicalAuditInput struct {
	reportPaginationInput
	reportFilterInput
}

// getClinicalAudit handles GET /api/v1/reports/clinical-audit.
func (h *Handler) getClinicalAudit(ctx context.Context, input *clinicalAuditInput) (*auditHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	f, err := parseFilters(input.reportFilterInput)
	if err != nil {
		return nil, err
	}

	resp, err := h.svc.GetClinicalAudit(ctx, QueryInput{
		ClinicID: clinicID,
		Filters:  f,
		Limit:    input.Limit,
		Offset:   input.Offset,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &auditHTTPResponse{Body: resp}, nil
}

type staffActionsInput struct {
	reportPaginationInput
	reportFilterInput
	// Note: StaffID is already on reportFilterInput (query:"staff_id").
	// staffActionsInput requires it — validated in the handler.
}

// getStaffActions handles GET /api/v1/reports/staff-actions.
func (h *Handler) getStaffActions(ctx context.Context, input *staffActionsInput) (*auditHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	if input.StaffID == "" {
		return nil, huma.Error400BadRequest("staff_id is required")
	}
	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}

	f, err := parseFilters(input.reportFilterInput)
	if err != nil {
		return nil, err
	}

	resp, err := h.svc.GetStaffActions(ctx, clinicID, staffID, f, input.Limit, input.Offset)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &auditHTTPResponse{Body: resp}, nil
}

type noteHistoryInput struct {
	NoteID string `path:"note_id" doc:"Note UUID."`
}

// getNoteHistory handles GET /api/v1/reports/note-history/{note_id}.
func (h *Handler) getNoteHistory(ctx context.Context, input *noteHistoryInput) (*auditHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.GetNoteHistory(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &auditHTTPResponse{Body: resp}, nil
}

type consentLogInput struct {
	reportPaginationInput
	reportFilterInput
}

// getConsentLog handles GET /api/v1/reports/consent-log.
func (h *Handler) getConsentLog(ctx context.Context, input *consentLogInput) (*auditHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	f, err := parseFilters(input.reportFilterInput)
	if err != nil {
		return nil, err
	}

	resp, err := h.svc.GetConsentLog(ctx, QueryInput{
		ClinicID: clinicID,
		Filters:  f,
		Limit:    input.Limit,
		Offset:   input.Offset,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &auditHTTPResponse{Body: resp}, nil
}

// ── Export ────────────────────────────────────────────────────────────────────

type exportBodyInput struct {
	Body struct {
		ReportType string        `json:"report_type" enum:"clinical_audit,staff_actions,note_history,consent_log" doc:"Report type to generate."`
		Format     string        `json:"format"      enum:"csv"                                                   doc:"Export format. Currently only csv is supported."`
		Filters    exportFilters `json:"filters,omitempty"`
	}
}

type exportFilters struct {
	From      *string `json:"from,omitempty"       doc:"ISO 8601 start datetime."`
	To        *string `json:"to,omitempty"         doc:"ISO 8601 end datetime."`
	StaffID   *string `json:"staff_id,omitempty"   doc:"Filter by actor staff UUID."`
	SubjectID *string `json:"subject_id,omitempty" doc:"Filter by subject UUID."`
	NoteID    *string `json:"note_id,omitempty"    doc:"Filter by note UUID."`
}

// requestExport handles POST /api/v1/reports/export.
func (h *Handler) requestExport(ctx context.Context, input *exportBodyInput) (*jobHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	f, err := parseExportFilters(input.Body.Filters)
	if err != nil {
		return nil, err
	}

	resp, err := h.svc.RequestExport(ctx, ExportInput{
		ClinicID:   clinicID,
		StaffID:    staffID,
		ReportType: input.Body.ReportType,
		Format:     input.Body.Format,
		Filters:    f,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &jobHTTPResponse{Body: resp}, nil
}

type exportJobInput struct {
	JobID string `path:"job_id" doc:"Export job UUID."`
}

// getExportJob handles GET /api/v1/reports/export/{job_id}.
func (h *Handler) getExportJob(ctx context.Context, input *exportJobInput) (*jobHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	jobID, err := uuid.Parse(input.JobID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid job_id")
	}

	// Pre-fetch the job to get the storage key (if complete) before building the response.
	rec, err := h.svc.GetReportJobRecord(ctx, jobID, clinicID)
	if err != nil {
		return nil, mapReportError(err)
	}

	var downloadURL *string
	if rec.Status == "complete" && rec.StorageKey != nil {
		url, err := h.store.PresignDownload(ctx, *rec.StorageKey, 1*time.Hour)
		if err != nil {
			return nil, mapReportError(err)
		}
		downloadURL = &url
	}

	resp, err := h.svc.GetExportJob(ctx, jobID, clinicID, downloadURL)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &jobHTTPResponse{Body: resp}, nil
}

// ── Compliance report handlers ───────────────────────────────────────────────

type complianceReportHTTPResponse struct {
	Body *ComplianceReportResponse
}

type complianceReportListHTTPResponse struct {
	Body *ComplianceReportListResponse
}

type requestComplianceReportInput struct {
	Body struct {
		Type        string `json:"type"         doc:"Report type slug — see SupportedComplianceReportTypes."`
		PeriodStart string `json:"period_start" doc:"RFC3339 inclusive."`
		PeriodEnd   string `json:"period_end"   doc:"RFC3339 inclusive."`
	}
}

// requestComplianceReport handles POST /api/v1/reports/compliance.
func (h *Handler) requestComplianceReport(ctx context.Context, input *requestComplianceReportInput) (*complianceReportHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	start, err := time.Parse(time.RFC3339, input.Body.PeriodStart)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid period_start: use RFC3339")
	}
	end, err := time.Parse(time.RFC3339, input.Body.PeriodEnd)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid period_end: use RFC3339")
	}

	resp, err := h.svc.RequestComplianceReport(ctx, RequestComplianceReportInput{
		ClinicID:    clinicID,
		StaffID:     staffID,
		Type:        input.Body.Type,
		PeriodStart: start,
		PeriodEnd:   end,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &complianceReportHTTPResponse{Body: resp}, nil
}

// previewComplianceReportInput drives POST /api/v1/reports/compliance/preview.
// Defaults the period to the last 7 days when caller omits one.
type previewComplianceReportInput struct {
	Body struct {
		Type        string `json:"type"                   doc:"Report type slug. Must have a v2 renderer wired (audit_pack today)."`
		PeriodStart string `json:"period_start,omitempty" doc:"RFC3339 inclusive. Defaults to 7 days ago when omitted."`
		PeriodEnd   string `json:"period_end,omitempty"   doc:"RFC3339 inclusive. Defaults to now when omitted."`
	}
}

// previewComplianceReportHTTPResponse streams PDF bytes back. No DB
// row, no S3, no email — pure read.
type previewComplianceReportHTTPResponse struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// previewComplianceReport handles POST /api/v1/reports/compliance/preview.
// Renders the supplied report type for the supplied period (or last 7
// days by default) and returns the PDF bytes inline. Used by the
// reports-catalog Preview drawer.
func (h *Handler) previewComplianceReport(ctx context.Context, input *previewComplianceReportInput) (*previewComplianceReportHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	now := time.Now().UTC()
	start := now.AddDate(0, 0, -7)
	end := now
	if input.Body.PeriodStart != "" {
		s, err := time.Parse(time.RFC3339, input.Body.PeriodStart)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid period_start: use RFC3339")
		}
		start = s
	}
	if input.Body.PeriodEnd != "" {
		e, err := time.Parse(time.RFC3339, input.Body.PeriodEnd)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid period_end: use RFC3339")
		}
		end = e
	}

	bytesOut, err := h.svc.PreviewComplianceReport(ctx, PreviewComplianceReportInput{
		ClinicID:    clinicID,
		Type:        input.Body.Type,
		PeriodStart: start,
		PeriodEnd:   end,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &previewComplianceReportHTTPResponse{
		ContentType: "application/pdf",
		ContentDisposition: fmt.Sprintf(
			`inline; filename="preview-%s.pdf"`, input.Body.Type),
		Body: bytesOut,
	}, nil
}

type listComplianceReportsInput struct {
	reportPaginationInput
	Type   string `query:"type"`
	Status string `query:"status"`
	From   string `query:"from"`
	To     string `query:"to"`
}

// listComplianceReports handles GET /api/v1/reports/compliance.
func (h *Handler) listComplianceReports(ctx context.Context, input *listComplianceReportsInput) (*complianceReportListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	in := ListComplianceReportsInput{
		Limit:  input.Limit,
		Offset: input.Offset,
	}
	if input.Type != "" {
		in.Type = &input.Type
	}
	if input.Status != "" {
		in.Status = &input.Status
	}
	if input.From != "" {
		t, err := time.Parse(time.RFC3339, input.From)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid from: use RFC3339")
		}
		in.From = &t
	}
	if input.To != "" {
		t, err := time.Parse(time.RFC3339, input.To)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid to: use RFC3339")
		}
		in.To = &t
	}

	resp, err := h.svc.ListComplianceReports(ctx, clinicID, in)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &complianceReportListHTTPResponse{Body: resp}, nil
}

type complianceReportIDPath struct {
	ID string `path:"id" doc:"The report UUID."`
}

// getComplianceReport handles GET /api/v1/reports/compliance/{id}.
func (h *Handler) getComplianceReport(ctx context.Context, input *complianceReportIDPath) (*complianceReportHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetComplianceReport(ctx, id, clinicID)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &complianceReportHTTPResponse{Body: resp}, nil
}

// downloadComplianceReport handles GET /api/v1/reports/compliance/{id}/download.
// Returns the report row enriched with a fresh presigned URL valid for 1h,
// and writes a `downloaded` row to report_audit.
func (h *Handler) downloadComplianceReport(ctx context.Context, input *complianceReportIDPath) (*complianceReportHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}

	rec, err := h.svc.GetComplianceReportRecord(ctx, id, clinicID)
	if err != nil {
		return nil, mapReportError(err)
	}
	if rec.Status != "done" || rec.FileKey == nil {
		return nil, huma.Error409Conflict("report is not ready yet")
	}

	url, err := h.store.PresignDownload(ctx, *rec.FileKey, time.Hour)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not mint download URL")
	}

	if err := h.svc.LogComplianceReportDownload(ctx, id, clinicID, staffID); err != nil {
		// Audit failure is loud-logged in repo; don't block the user.
		_ = err
	}

	return &complianceReportHTTPResponse{
		Body: complianceRecordToResponse(rec, &url),
	}, nil
}

// ── Schedule handlers ────────────────────────────────────────────────────────

type scheduleHTTPResponse struct {
	Body *ReportScheduleResponse
}

type scheduleListHTTPResponse struct {
	Body *ReportScheduleListResponse
}

type createScheduleBody struct {
	Body struct {
		ReportType string   `json:"report_type" doc:"Slug — see SupportedComplianceReportTypes."`
		Frequency  string   `json:"frequency"   enum:"daily,weekly,monthly,quarterly"`
		Recipients []string `json:"recipients"  minItems:"1" doc:"At least one email address."`
	}
}

func (h *Handler) createSchedule(ctx context.Context, input *createScheduleBody) (*scheduleHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	resp, err := h.svc.CreateReportSchedule(ctx, CreateReportScheduleInput{
		ClinicID:   clinicID,
		StaffID:    staffID,
		ReportType: input.Body.ReportType,
		Frequency:  input.Body.Frequency,
		Recipients: input.Body.Recipients,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &scheduleHTTPResponse{Body: resp}, nil
}

type schedulesEmptyInput struct{}

func (h *Handler) listSchedules(ctx context.Context, _ *schedulesEmptyInput) (*scheduleListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListReportSchedules(ctx, clinicID)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &scheduleListHTTPResponse{Body: resp}, nil
}

type scheduleIDPath struct {
	ID string `path:"id"`
}

func (h *Handler) getSchedule(ctx context.Context, input *scheduleIDPath) (*scheduleHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetReportSchedule(ctx, id, clinicID)
	if err != nil {
		return nil, mapReportError(err)
	}
	return &scheduleHTTPResponse{Body: resp}, nil
}

type updateScheduleBody struct {
	ID   string `path:"id"`
	Body struct {
		Recipients *[]string `json:"recipients,omitempty"`
		Paused     *bool     `json:"paused,omitempty"`
	}
}

func (h *Handler) updateSchedule(ctx context.Context, input *updateScheduleBody) (*scheduleHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.UpdateReportSchedule(ctx, UpdateReportScheduleInput{
		ID:         id,
		ClinicID:   clinicID,
		Recipients: input.Body.Recipients,
		Paused:     input.Body.Paused,
	})
	if err != nil {
		return nil, mapReportError(err)
	}
	return &scheduleHTTPResponse{Body: resp}, nil
}

type deleteScheduleEmpty struct{}

func (h *Handler) deleteSchedule(ctx context.Context, input *scheduleIDPath) (*deleteScheduleEmpty, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := h.svc.DeleteReportSchedule(ctx, id, clinicID); err != nil {
		return nil, mapReportError(err)
	}
	return &deleteScheduleEmpty{}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapReportError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}

// ── Filter parsing ────────────────────────────────────────────────────────────

func parseFilters(f reportFilterInput) (ReportFilters, error) {
	out := ReportFilters{}

	if f.From != "" {
		t, err := time.Parse(time.RFC3339, f.From)
		if err != nil {
			return out, huma.Error400BadRequest("invalid from: use RFC3339 format")
		}
		out.From = &t
	}
	if f.To != "" {
		t, err := time.Parse(time.RFC3339, f.To)
		if err != nil {
			return out, huma.Error400BadRequest("invalid to: use RFC3339 format")
		}
		out.To = &t
	}
	if f.StaffID != "" {
		id, err := uuid.Parse(f.StaffID)
		if err != nil {
			return out, huma.Error400BadRequest("invalid staff_id")
		}
		out.StaffID = &id
	}
	if f.SubjectID != "" {
		id, err := uuid.Parse(f.SubjectID)
		if err != nil {
			return out, huma.Error400BadRequest("invalid subject_id")
		}
		out.SubjectID = &id
	}
	return out, nil
}

func parseExportFilters(f exportFilters) (ReportFilters, error) {
	out := ReportFilters{}

	if f.From != nil {
		t, err := time.Parse(time.RFC3339, *f.From)
		if err != nil {
			return out, huma.Error400BadRequest("invalid from: use RFC3339 format")
		}
		out.From = &t
	}
	if f.To != nil {
		t, err := time.Parse(time.RFC3339, *f.To)
		if err != nil {
			return out, huma.Error400BadRequest("invalid to: use RFC3339 format")
		}
		out.To = &t
	}
	if f.StaffID != nil {
		id, err := uuid.Parse(*f.StaffID)
		if err != nil {
			return out, huma.Error400BadRequest("invalid staff_id")
		}
		out.StaffID = &id
	}
	if f.SubjectID != nil {
		id, err := uuid.Parse(*f.SubjectID)
		if err != nil {
			return out, huma.Error400BadRequest("invalid subject_id")
		}
		out.SubjectID = &id
	}
	if f.NoteID != nil {
		id, err := uuid.Parse(*f.NoteID)
		if err != nil {
			return out, huma.Error400BadRequest("invalid note_id")
		}
		out.NoteID = &id
	}
	return out, nil
}
