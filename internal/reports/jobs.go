package reports

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/platform/storage"
	"github.com/riverqueue/river"
)

// GenerateReportArgs is the River job payload for async report export.
type GenerateReportArgs struct {
	JobID      uuid.UUID     `json:"job_id"`
	ClinicID   uuid.UUID     `json:"clinic_id"`
	ReportType string        `json:"report_type"`
	Format     string        `json:"format"`
	Filters    ReportFilters `json:"filters"`
}

// Kind returns the unique job type string used by River.
func (GenerateReportArgs) Kind() string { return "generate_report" }

// GenerateReportWorker produces the report file, uploads it to S3, and marks the job complete.
type GenerateReportWorker struct {
	river.WorkerDefaults[GenerateReportArgs]
	repo  *Repository
	store *storage.Store
}

// NewGenerateReportWorker constructs a GenerateReportWorker.
func NewGenerateReportWorker(repo *Repository, store *storage.Store) *GenerateReportWorker {
	return &GenerateReportWorker{repo: repo, store: store}
}

// Work executes the report generation job.
func (w *GenerateReportWorker) Work(ctx context.Context, job *river.Job[GenerateReportArgs]) error {
	args := job.Args

	// Fetch all data for the report (no pagination — export is the full result set).
	events, err := w.fetchAll(ctx, args)
	if err != nil {
		errMsg := fmt.Sprintf("fetch data: %v", err)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %w", err)
	}

	// Generate file in memory.
	var buf bytes.Buffer
	ext := "csv"
	contentType := "text/csv"

	switch args.Format {
	case "csv", "":
		if err := writeCSV(&buf, args.ReportType, events); err != nil {
			errMsg := fmt.Sprintf("generate csv: %v", err)
			_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
			return fmt.Errorf("generate_report: %w", err)
		}
	default:
		errMsg := fmt.Sprintf("unsupported format: %s", args.Format)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %s", errMsg)
	}

	// Upload to S3.
	key := fmt.Sprintf("reports/%s/%s.%s", args.ClinicID, args.JobID, ext)
	size := int64(buf.Len())
	if err := w.store.Upload(ctx, key, contentType, &buf, size); err != nil {
		errMsg := fmt.Sprintf("upload: %v", err)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %w", err)
	}

	// Mark complete — handler generates presigned URL on demand from the key.
	if err := w.repo.MarkComplete(ctx, args.JobID, key); err != nil {
		return fmt.Errorf("generate_report: mark complete: %w", err)
	}

	return nil
}

// fetchAll retrieves the full (un-paginated) dataset for the report type.
func (w *GenerateReportWorker) fetchAll(ctx context.Context, args GenerateReportArgs) ([]*AuditEventRecord, error) {
	const maxRows = 50_000
	p := ListParams{Limit: maxRows, Offset: 0}

	switch args.ReportType {
	case "clinical_audit":
		events, _, err := w.repo.QueryClinicalAudit(ctx, args.ClinicID, args.Filters, p)
		return events, err
	case "staff_actions":
		if args.Filters.StaffID == nil {
			return nil, fmt.Errorf("staff_id required for staff_actions report")
		}
		events, _, err := w.repo.QueryStaffActions(ctx, args.ClinicID, *args.Filters.StaffID, args.Filters, p)
		return events, err
	case "note_history":
		if args.Filters.NoteID == nil {
			return nil, fmt.Errorf("note_id required for note_history report")
		}
		return w.repo.QueryNoteHistory(ctx, *args.Filters.NoteID, args.ClinicID)
	case "consent_log":
		events, _, err := w.repo.QueryConsentLog(ctx, args.ClinicID, args.Filters, p)
		return events, err
	default:
		return nil, fmt.Errorf("unknown report_type: %s", args.ReportType)
	}
}

// ── CSV renderer ──────────────────────────────────────────────────────────────

var csvHeaders = []string{
	"occurred_at", "event_type", "note_id", "subject_id",
	"actor_id", "actor_role", "field_id", "old_value", "new_value", "reason",
}

func writeCSV(buf *bytes.Buffer, _ string, events []*AuditEventRecord) error {
	w := csv.NewWriter(buf)
	if err := w.Write(csvHeaders); err != nil {
		return fmt.Errorf("writeCSV: header: %w", err)
	}
	for _, e := range events {
		row := []string{
			e.OccurredAt.UTC().Format(time.RFC3339),
			e.EventType,
			e.NoteID.String(),
			nilUUID(e.SubjectID),
			e.ActorID.String(),
			e.ActorRole,
			nilUUID(e.FieldID),
			nilStr(e.OldValue),
			nilStr(e.NewValue),
			nilStr(e.Reason),
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writeCSV: row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("writeCSV: flush: %w", err)
	}
	return nil
}

func nilUUID(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func nilStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
