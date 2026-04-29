package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/platform/storage"
	"github.com/riverqueue/river"
)

// GenerateCompliancePDFArgs is the River job payload for async compliance
// PDF generation. Distinct from GenerateReportArgs — that one builds CSV
// exports of audit events; this one builds the regulator-facing PDFs
// described in migration 00061's `reports` table.
type GenerateCompliancePDFArgs struct {
	ReportID uuid.UUID `json:"report_id"`
	ClinicID uuid.UUID `json:"clinic_id"`
}

// Kind returns the unique job type string used by River.
func (GenerateCompliancePDFArgs) Kind() string { return "generate_compliance_pdf" }

// GenerateCompliancePDFWorker dispatches to the right PDF builder by type
// (read off the report row), uploads the resulting bytes, and stamps the
// report row with file metadata + sha256.
type GenerateCompliancePDFWorker struct {
	river.WorkerDefaults[GenerateCompliancePDFArgs]
	repo  *Repository
	store *storage.Store
	data  ComplianceDataSource
}

// NewGenerateCompliancePDFWorker constructs the worker.
func NewGenerateCompliancePDFWorker(repo *Repository, store *storage.Store, data ComplianceDataSource) *GenerateCompliancePDFWorker {
	return &GenerateCompliancePDFWorker{repo: repo, store: store, data: data}
}

// Work runs the compliance PDF generation pipeline:
//  1. Load the report row (no clinic scope — internal worker).
//  2. Mark running.
//  3. Resolve clinic snapshot, fetch domain data via the data source.
//  4. Dispatch to the right builder by type.
//  5. Upload PDF to storage.
//  6. Mark done with file_key + size + sha256.
func (w *GenerateCompliancePDFWorker) Work(ctx context.Context, job *river.Job[GenerateCompliancePDFArgs]) error {
	args := job.Args

	rec, err := w.repo.GetComplianceReportInternal(ctx, args.ReportID)
	if err != nil {
		return fmt.Errorf("generate_compliance_pdf: load: %w", err)
	}

	if err := w.repo.MarkComplianceReportRunning(ctx, args.ReportID); err != nil {
		return fmt.Errorf("generate_compliance_pdf: mark running: %w", err)
	}

	buf, hash, err := w.buildPDF(ctx, rec)
	if err != nil {
		errMsg := err.Error()
		_ = w.repo.MarkComplianceReportFailed(ctx, args.ReportID, errMsg)
		return fmt.Errorf("generate_compliance_pdf: build: %w", err)
	}

	key := fmt.Sprintf("compliance-reports/%s/%s.%s",
		rec.ClinicID, rec.ID, rec.FileFormat)
	// S3's PutObject hashes the payload before sending and requires a
	// seekable reader. *bytes.Buffer is not seekable; *bytes.Reader is.
	payload := bytes.NewReader(buf.Bytes())
	size := int64(payload.Len())
	if err := w.store.Upload(ctx, key, contentTypeFor(rec.FileFormat), payload, size); err != nil {
		errMsg := fmt.Sprintf("upload: %v", err)
		_ = w.repo.MarkComplianceReportFailed(ctx, args.ReportID, errMsg)
		return fmt.Errorf("generate_compliance_pdf: %w", err)
	}

	if err := w.repo.MarkComplianceReportDone(ctx, args.ReportID, key, size, hash); err != nil {
		return fmt.Errorf("generate_compliance_pdf: mark done: %w", err)
	}
	return nil
}

// buildPDF dispatches by report type. New types: add a case here + register
// a builder in pdf.go. Vertical/country come off the row; the builder reads
// regulator labels from regulatorContexts inside.
func (w *GenerateCompliancePDFWorker) buildPDF(ctx context.Context, rec *ComplianceReportRecord) (*bytes.Buffer, string, error) {
	clinic, err := w.data.GetClinic(ctx, rec.ClinicID)
	if err != nil {
		return nil, "", fmt.Errorf("clinic: %w", err)
	}

	switch rec.Type {
	case "controlled_drugs_register":
		ops, err := w.data.ListControlledDrugOps(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("ops: %w", err)
		}
		recons, err := w.data.ListReconciliationsInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("recons: %w", err)
		}
		return BuildControlledDrugsRegisterPDF(clinic, rec.PeriodStart, rec.PeriodEnd, ops, recons, rec.ID.String())

	case "audit_pack":
		ops, err := w.data.ListControlledDrugOps(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("ops: %w", err)
		}
		recons, err := w.data.ListReconciliationsInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("recons: %w", err)
		}
		counts, err := w.data.CountNotesByStatus(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("note counts: %w", err)
		}
		return BuildAuditPackPDF(clinic, rec.PeriodStart, rec.PeriodEnd, ops, recons, counts, rec.ID.String())

	default:
		return nil, "", fmt.Errorf("unknown compliance report type: %s", rec.Type)
	}
}

func contentTypeFor(format string) string {
	switch format {
	case "pdf":
		return "application/pdf"
	case "zip":
		return "application/zip"
	case "csv":
		return "text/csv"
	default:
		return "application/octet-stream"
	}
}

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

	// Compute SHA-256 of the CSV content for integrity verification.
	hash := sha256.Sum256(buf.Bytes())
	contentHash := "sha256:" + hex.EncodeToString(hash[:])

	// Upload to S3. PutObject hashes the payload before sending and needs
	// a seekable reader; *bytes.Buffer is not seekable, *bytes.Reader is.
	key := fmt.Sprintf("reports/%s/%s.csv", args.ClinicID, args.JobID)
	payload := bytes.NewReader(buf.Bytes())
	size := int64(payload.Len())
	if err := w.store.Upload(ctx, key, "text/csv", payload, size); err != nil {
		errMsg := fmt.Sprintf("upload: %v", err)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %w", err)
	}

	// Mark complete — handler generates presigned URL on demand from the key.
	if err := w.repo.MarkComplete(ctx, args.JobID, key, contentHash); err != nil {
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
