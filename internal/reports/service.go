package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// jobEnqueuer is the subset of river.Client used by the service.
type jobEnqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Service handles report business logic.
type Service struct {
	repo    *Repository
	enqueue jobEnqueuer
}

// NewService constructs a reports Service.
func NewService(repo *Repository, enqueue jobEnqueuer) *Service {
	return &Service{repo: repo, enqueue: enqueue}
}

// ── Response types ────────────────────────────────────────────────────────────

// AuditEventResponse is the API-safe representation of a single note_events row.
//
//nolint:revive
type AuditEventResponse struct {
	ID         string  `json:"id"`
	NoteID     string  `json:"note_id"`
	SubjectID  *string `json:"subject_id,omitempty"`
	EventType  string  `json:"event_type"`
	FieldID    *string `json:"field_id,omitempty"`
	OldValue   *string `json:"old_value,omitempty"`
	NewValue   *string `json:"new_value,omitempty"`
	ActorID    string  `json:"actor_id"`
	ActorRole  string  `json:"actor_role"`
	Reason     *string `json:"reason,omitempty"`
	OccurredAt string  `json:"occurred_at"`
}

// AuditReportResponse is a paginated list of audit events.
//
//nolint:revive
type AuditReportResponse struct {
	Items      []*AuditEventResponse `json:"items"`
	Total      int                   `json:"total"`
	Limit      int                   `json:"limit"`
	Offset     int                   `json:"offset"`
	ReportType string                `json:"report_type"`
}

// ReportJobResponse is the API-safe representation of a report_jobs row.
//
//nolint:revive
type ReportJobResponse struct {
	ID          string  `json:"id"`
	ReportType  string  `json:"report_type"`
	Format      string  `json:"format"`
	Status      string  `json:"status"`
	DownloadURL *string `json:"download_url,omitempty"` // set when status=complete
	ErrorMsg    *string `json:"error_msg,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// QueryInput holds filters + pagination for report query endpoints.
type QueryInput struct {
	ClinicID uuid.UUID
	Filters  ReportFilters
	Limit    int
	Offset   int
}

// ExportInput holds parameters for triggering an async export job.
type ExportInput struct {
	ClinicID   uuid.UUID
	StaffID    uuid.UUID
	ReportType string
	Format     string
	Filters    ReportFilters
}

// ── Service methods ───────────────────────────────────────────────────────────

// GetClinicalAudit returns a paginated clinical audit report.
func (s *Service) GetClinicalAudit(ctx context.Context, input QueryInput) (*AuditReportResponse, error) {
	input.Limit = clampLimit(input.Limit)
	events, total, err := s.repo.QueryClinicalAudit(ctx, input.ClinicID, input.Filters, ListParams{input.Limit, input.Offset})
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetClinicalAudit: %w", err)
	}
	return toAuditResponse(events, total, input.Limit, input.Offset, "clinical_audit"), nil
}

// GetStaffActions returns a paginated staff actions report for a specific actor.
func (s *Service) GetStaffActions(ctx context.Context, clinicID, staffID uuid.UUID, f ReportFilters, limit, offset int) (*AuditReportResponse, error) {
	limit = clampLimit(limit)
	events, total, err := s.repo.QueryStaffActions(ctx, clinicID, staffID, f, ListParams{limit, offset})
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetStaffActions: %w", err)
	}
	return toAuditResponse(events, total, limit, offset, "staff_actions"), nil
}

// GetNoteHistory returns the full event trail for a single note.
func (s *Service) GetNoteHistory(ctx context.Context, noteID, clinicID uuid.UUID) (*AuditReportResponse, error) {
	events, err := s.repo.QueryNoteHistory(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetNoteHistory: %w", err)
	}
	return toAuditResponse(events, len(events), len(events), 0, "note_history"), nil
}

// GetConsentLog returns a paginated consent (note.submitted) event log.
func (s *Service) GetConsentLog(ctx context.Context, input QueryInput) (*AuditReportResponse, error) {
	input.Limit = clampLimit(input.Limit)
	events, total, err := s.repo.QueryConsentLog(ctx, input.ClinicID, input.Filters, ListParams{input.Limit, input.Offset})
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetConsentLog: %w", err)
	}
	return toAuditResponse(events, total, input.Limit, input.Offset, "consent_log"), nil
}

// RequestExport enqueues an async export job and returns the job ID.
func (s *Service) RequestExport(ctx context.Context, input ExportInput) (*ReportJobResponse, error) {
	filtersJSON, err := json.Marshal(input.Filters)
	if err != nil {
		return nil, fmt.Errorf("reports.service.RequestExport: marshal filters: %w", err)
	}
	filtersStr := string(filtersJSON)

	jobID := domain.NewID()
	rec, err := s.repo.InsertReportJob(ctx, InsertReportJobParams{
		ID:         jobID,
		ClinicID:   input.ClinicID,
		ReportType: input.ReportType,
		Format:     input.Format,
		Filters:    &filtersStr,
		CreatedBy:  input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("reports.service.RequestExport: insert job: %w", err)
	}

	if _, err := s.enqueue.Insert(ctx, GenerateReportArgs{
		JobID:      jobID,
		ClinicID:   input.ClinicID,
		ReportType: input.ReportType,
		Format:     input.Format,
		Filters:    input.Filters,
	}, nil); err != nil {
		return nil, fmt.Errorf("reports.service.RequestExport: enqueue: %w", err)
	}

	return toJobResponse(rec, nil), nil
}

// GetExportJob returns the status of an export job.
// When complete, downloadURL is a fresh presigned URL generated by the caller.
func (s *Service) GetExportJob(ctx context.Context, jobID, clinicID uuid.UUID, downloadURL *string) (*ReportJobResponse, error) {
	rec, err := s.repo.GetReportJob(ctx, jobID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetExportJob: %w", err)
	}
	return toJobResponse(rec, downloadURL), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func toAuditResponse(events []*AuditEventRecord, total, limit, offset int, reportType string) *AuditReportResponse {
	items := make([]*AuditEventResponse, len(events))
	for i, e := range events {
		items[i] = toEventResponse(e)
	}
	return &AuditReportResponse{
		Items:      items,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		ReportType: reportType,
	}
}

func toEventResponse(e *AuditEventRecord) *AuditEventResponse {
	r := &AuditEventResponse{
		ID:         e.ID.String(),
		NoteID:     e.NoteID.String(),
		EventType:  e.EventType,
		ActorID:    e.ActorID.String(),
		ActorRole:  e.ActorRole,
		Reason:     e.Reason,
		OccurredAt: e.OccurredAt.Format(time.RFC3339),
		OldValue:   e.OldValue,
		NewValue:   e.NewValue,
	}
	if e.SubjectID != nil {
		s := e.SubjectID.String()
		r.SubjectID = &s
	}
	if e.FieldID != nil {
		s := e.FieldID.String()
		r.FieldID = &s
	}
	return r
}

func toJobResponse(j *ReportJobRecord, downloadURL *string) *ReportJobResponse {
	r := &ReportJobResponse{
		ID:         j.ID.String(),
		ReportType: j.ReportType,
		Format:     j.Format,
		Status:     j.Status,
		ErrorMsg:   j.ErrorMsg,
		CreatedAt:  j.CreatedAt.Format(time.RFC3339),
	}
	if j.CompletedAt != nil {
		s := j.CompletedAt.Format(time.RFC3339)
		r.CompletedAt = &s
	}
	if downloadURL != nil {
		r.DownloadURL = downloadURL
	}
	return r
}
