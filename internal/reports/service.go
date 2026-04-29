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
	data    ComplianceDataSource // optional — only required for compliance PDFs
}

// NewService constructs a reports Service. The compliance data source can be
// nil for callers that only need the legacy CSV exports; compliance PDF
// methods will return ErrValidation if invoked without it.
func NewService(repo *Repository, enqueue jobEnqueuer, data ComplianceDataSource) *Service {
	return &Service{repo: repo, enqueue: enqueue, data: data}
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
	ContentHash *string `json:"content_hash,omitempty"` // SHA-256 of exported file for integrity verification
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

// GetReportJobRecord fetches the raw job record for a clinic.
// Used by the handler to retrieve the storage key before generating a presigned URL.
func (s *Service) GetReportJobRecord(ctx context.Context, jobID, clinicID uuid.UUID) (*ReportJobRecord, error) {
	rec, err := s.repo.GetReportJob(ctx, jobID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetReportJobRecord: %w", err)
	}
	return rec, nil
}

// ── Compliance reports (regulator-facing PDFs) ───────────────────────────────
//
// These methods drive the new `reports` table introduced by migration 00061.
// One generic flow: clinic requests a typed report, a River job builds the
// PDF, the report row stores file_key + sha256 hash. Download endpoint mints
// a presigned URL and writes a row to report_audit.
//
// Report types are vertical- and country-agnostic by design — the (vertical,
// country) the report is generated against is a property of the clinic, not
// of the type. The PDF builder pulls regulator-specific labels from the
// regulatorContexts registry in pdf.go.

// SupportedComplianceReportTypes lists the report-type strings accepted by
// RequestComplianceReport. Adding a new type means: pick a slug here, add a
// case in the worker dispatch, optionally add a new builder. UI uses this
// list to render the "request a report" picker.
var SupportedComplianceReportTypes = []string{
	"audit_pack",
	"controlled_drugs_register",
	"evidence_pack",
	"records_audit",
	"incidents_log",
	"hipaa_disclosure_log",
	"dea_biennial_inventory",
	"sentinel_events_log",
}

// fileFormatForType — every type knows its native output format.
var fileFormatForType = map[string]string{
	"audit_pack":                "pdf",
	"controlled_drugs_register": "pdf",
	"evidence_pack":             "pdf",
	"records_audit":             "pdf",
	"incidents_log":             "pdf",
	"hipaa_disclosure_log":      "pdf",
	"dea_biennial_inventory":    "pdf",
	"sentinel_events_log":       "pdf",
}

// ComplianceReportResponse is the API-safe representation of a compliance
// report row.
//
//nolint:revive
type ComplianceReportResponse struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Vertical      string  `json:"vertical"`
	Country       string  `json:"country"`
	PeriodStart   string  `json:"period_start"`
	PeriodEnd     string  `json:"period_end"`
	Status        string  `json:"status"`
	FileFormat    string  `json:"file_format"`
	FileSizeBytes *int64  `json:"file_size_bytes,omitempty"`
	ReportHash    *string `json:"report_hash,omitempty"`
	RequestedBy   string  `json:"requested_by"`
	RequestedAt   string  `json:"requested_at"`
	StartedAt     *string `json:"started_at,omitempty"`
	CompletedAt   *string `json:"completed_at,omitempty"`
	ErrorMessage  *string `json:"error_message,omitempty"`
	DownloadURL   *string `json:"download_url,omitempty"` // present on download endpoint
}

// ComplianceReportListResponse — paginated.
//
//nolint:revive
type ComplianceReportListResponse struct {
	Items  []*ComplianceReportResponse `json:"items"`
	Total  int                         `json:"total"`
	Limit  int                         `json:"limit"`
	Offset int                         `json:"offset"`
}

// RequestComplianceReportInput — service input for creating a new report.
type RequestComplianceReportInput struct {
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	Type        string
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// ListComplianceReportsInput — filters.
type ListComplianceReportsInput struct {
	Limit  int
	Offset int
	Type   *string
	Status *string
	From   *time.Time
	To     *time.Time
}

// RequestComplianceReport validates input, inserts a queued report row, and
// enqueues a River job to generate the PDF asynchronously.
func (s *Service) RequestComplianceReport(ctx context.Context, in RequestComplianceReportInput) (*ComplianceReportResponse, error) {
	if s.data == nil {
		return nil, fmt.Errorf("reports.service.RequestComplianceReport: compliance data source not configured: %w", domain.ErrValidation)
	}
	if !isSupportedComplianceType(in.Type) {
		return nil, fmt.Errorf("reports.service.RequestComplianceReport: unsupported type %q: %w", in.Type, domain.ErrValidation)
	}
	if !in.PeriodEnd.After(in.PeriodStart) {
		return nil, fmt.Errorf("reports.service.RequestComplianceReport: period_end must be after period_start: %w", domain.ErrValidation)
	}

	clinic, err := s.data.GetClinic(ctx, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.RequestComplianceReport: clinic lookup: %w", err)
	}

	id := domain.NewID()
	rec, err := s.repo.CreateComplianceReport(ctx, CreateComplianceReportParams{
		ID:          id,
		ClinicID:    in.ClinicID,
		Type:        in.Type,
		Vertical:    clinic.Vertical,
		Country:     clinic.Country,
		PeriodStart: in.PeriodStart,
		PeriodEnd:   in.PeriodEnd,
		FileFormat:  fileFormatFor(in.Type),
		RequestedBy: in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("reports.service.RequestComplianceReport: insert: %w", err)
	}

	if _, err := s.enqueue.Insert(ctx, GenerateCompliancePDFArgs{
		ReportID: id,
		ClinicID: in.ClinicID,
	}, nil); err != nil {
		return nil, fmt.Errorf("reports.service.RequestComplianceReport: enqueue: %w", err)
	}

	// Append-only audit: who requested what.
	if err := s.repo.LogReportAudit(ctx, LogReportAuditParams{
		ID:       domain.NewID(),
		ReportID: id,
		ClinicID: in.ClinicID,
		StaffID:  in.StaffID,
		Action:   "generated",
	}); err != nil {
		// Don't fail the request — audit failure is loud-logged in repo.
		_ = err
	}

	return complianceRecordToResponse(rec, nil), nil
}

// GetComplianceReport — single row read, clinic-scoped.
func (s *Service) GetComplianceReport(ctx context.Context, id, clinicID uuid.UUID) (*ComplianceReportResponse, error) {
	rec, err := s.repo.GetComplianceReport(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetComplianceReport: %w", err)
	}
	return complianceRecordToResponse(rec, nil), nil
}

// GetComplianceReportRecord — raw row for the handler download flow.
func (s *Service) GetComplianceReportRecord(ctx context.Context, id, clinicID uuid.UUID) (*ComplianceReportRecord, error) {
	rec, err := s.repo.GetComplianceReport(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetComplianceReportRecord: %w", err)
	}
	return rec, nil
}

// ListComplianceReports — paginated list for a clinic.
func (s *Service) ListComplianceReports(ctx context.Context, clinicID uuid.UUID, in ListComplianceReportsInput) (*ComplianceReportListResponse, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	recs, total, err := s.repo.ListComplianceReports(ctx, clinicID, ListComplianceReportsParams(in))
	if err != nil {
		return nil, fmt.Errorf("reports.service.ListComplianceReports: %w", err)
	}
	out := make([]*ComplianceReportResponse, len(recs))
	for i, r := range recs {
		out[i] = complianceRecordToResponse(r, nil)
	}
	return &ComplianceReportListResponse{
		Items:  out,
		Total:  total,
		Limit:  in.Limit,
		Offset: in.Offset,
	}, nil
}

// LogComplianceReportDownload appends a `downloaded` row to report_audit.
// Handler calls this just before returning the presigned URL.
func (s *Service) LogComplianceReportDownload(ctx context.Context, reportID, clinicID, staffID uuid.UUID) error {
	if err := s.repo.LogReportAudit(ctx, LogReportAuditParams{
		ID:       domain.NewID(),
		ReportID: reportID,
		ClinicID: clinicID,
		StaffID:  staffID,
		Action:   "downloaded",
	}); err != nil {
		return fmt.Errorf("reports.service.LogComplianceReportDownload: %w", err)
	}
	return nil
}

// ── Compliance helpers ────────────────────────────────────────────────────────

func isSupportedComplianceType(t string) bool {
	for _, s := range SupportedComplianceReportTypes {
		if s == t {
			return true
		}
	}
	return false
}

func fileFormatFor(t string) string {
	if f, ok := fileFormatForType[t]; ok {
		return f
	}
	return "pdf"
}

func complianceRecordToResponse(r *ComplianceReportRecord, downloadURL *string) *ComplianceReportResponse {
	// Suppress stale error_message on done rows. Older rows that succeeded
	// after a prior failure still carry the failure text in the column;
	// once a row is done the message is no longer meaningful.
	var errMsg *string
	if r.Status != "done" {
		errMsg = r.ErrorMessage
	}
	resp := &ComplianceReportResponse{
		ID:            r.ID.String(),
		Type:          r.Type,
		Vertical:      r.Vertical,
		Country:       r.Country,
		PeriodStart:   r.PeriodStart.Format(time.RFC3339),
		PeriodEnd:     r.PeriodEnd.Format(time.RFC3339),
		Status:        r.Status,
		FileFormat:    r.FileFormat,
		FileSizeBytes: r.FileSizeBytes,
		ReportHash:    r.ReportHash,
		RequestedBy:   r.RequestedBy.String(),
		RequestedAt:   r.RequestedAt.Format(time.RFC3339),
		ErrorMessage:  errMsg,
	}
	if r.StartedAt != nil {
		s := r.StartedAt.Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if r.CompletedAt != nil {
		s := r.CompletedAt.Format(time.RFC3339)
		resp.CompletedAt = &s
	}
	if downloadURL != nil {
		resp.DownloadURL = downloadURL
	}
	return resp
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
		ID:          j.ID.String(),
		ReportType:  j.ReportType,
		Format:      j.Format,
		Status:      j.Status,
		ContentHash: j.ContentHash,
		ErrorMsg:    j.ErrorMsg,
		CreatedAt:   j.CreatedAt.Format(time.RFC3339),
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
