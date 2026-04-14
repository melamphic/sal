package reports

import (
	"context"
	"errors"
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
	StaffIDPath string `query:"staff_id" doc:"Staff UUID to filter actions for. Required."`
}

// getStaffActions handles GET /api/v1/reports/staff-actions.
func (h *Handler) getStaffActions(ctx context.Context, input *staffActionsInput) (*auditHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	if input.StaffIDPath == "" {
		return nil, huma.Error400BadRequest("staff_id is required")
	}
	staffID, err := uuid.Parse(input.StaffIDPath)
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
	rec, err := h.svc.repo.GetReportJob(ctx, jobID, clinicID)
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

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapReportError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}

// ── Filter parsing ────────────────────────────────────────────────────────────

func parseFilters(f reportFilterInput) (ReportFilters, error) {
	out := ReportFilters{}
	var err error

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
	return out, err
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
