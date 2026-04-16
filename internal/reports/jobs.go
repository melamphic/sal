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

	switch args.Format {
	case "csv", "":
	default:
		errMsg := fmt.Sprintf("unsupported format: %s", args.Format)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %s", errMsg)
	}

	// Build the CSV in memory using paginated fetches to avoid loading tens of
	// thousands of rows at once.
	var buf bytes.Buffer
	if err := w.buildCSV(ctx, args, &buf); err != nil {
		errMsg := fmt.Sprintf("build csv: %v", err)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %w", err)
	}

	// Upload to S3.
	key := fmt.Sprintf("reports/%s/%s.csv", args.ClinicID, args.JobID)
	size := int64(buf.Len())
	if err := w.store.Upload(ctx, key, "text/csv", &buf, size); err != nil {
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

const pageSize = 1_000

// buildCSV writes the full report as CSV to buf, fetching rows in pages of
// pageSize to avoid loading the entire result set into memory at once.
func (w *GenerateReportWorker) buildCSV(ctx context.Context, args GenerateReportArgs, buf *bytes.Buffer) error {
	cw := csv.NewWriter(buf)
	if err := cw.Write(csvHeaders); err != nil {
		return fmt.Errorf("header: %w", err)
	}

	// note_history is bounded and does not use the pagination path.
	if args.ReportType == "note_history" {
		if args.Filters.NoteID == nil {
			return fmt.Errorf("note_id required for note_history report")
		}
		events, err := w.repo.QueryNoteHistory(ctx, *args.Filters.NoteID, args.ClinicID)
		if err != nil {
			return err
		}
		return writeRows(cw, events)
	}

	for offset := 0; ; offset += pageSize {
		p := ListParams{Limit: pageSize, Offset: offset}
		var (
			events []*AuditEventRecord
			err    error
		)
		switch args.ReportType {
		case "clinical_audit":
			events, _, err = w.repo.QueryClinicalAudit(ctx, args.ClinicID, args.Filters, p)
		case "staff_actions":
			if args.Filters.StaffID == nil {
				return fmt.Errorf("staff_id required for staff_actions report")
			}
			events, _, err = w.repo.QueryStaffActions(ctx, args.ClinicID, *args.Filters.StaffID, args.Filters, p)
		case "consent_log":
			events, _, err = w.repo.QueryConsentLog(ctx, args.ClinicID, args.Filters, p)
		default:
			return fmt.Errorf("unknown report_type: %s", args.ReportType)
		}
		if err != nil {
			return err
		}
		if err := writeRows(cw, events); err != nil {
			return err
		}
		if len(events) < pageSize {
			break
		}
	}

	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("csv flush: %w", err)
	}
	return nil
}

// ── CSV renderer ──────────────────────────────────────────────────────────────

var csvHeaders = []string{
	"occurred_at", "event_type", "note_id", "subject_id",
	"actor_id", "actor_role", "field_id", "old_value", "new_value", "reason",
}

func writeRows(w *csv.Writer, events []*AuditEventRecord) error {
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
			return fmt.Errorf("writeRows: %w", err)
		}
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
