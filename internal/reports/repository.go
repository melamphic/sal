package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// ── Record types ──────────────────────────────────────────────────────────────

// AuditEventRecord is a flattened row from note_events used in report queries.
type AuditEventRecord struct {
	ID         uuid.UUID
	NoteID     uuid.UUID
	SubjectID  *uuid.UUID
	ClinicID   uuid.UUID
	EventType  string
	FieldID    *uuid.UUID
	OldValue   *string
	NewValue   *string
	ActorID    uuid.UUID
	ActorRole  string
	Reason     *string
	OccurredAt time.Time
}

// ComplianceReportRecord is the raw DB representation of a row in the new
// `reports` table introduced by migration 00061. Distinct from the older
// ReportJobRecord (which queues simple CSV exports off the audit log) — this
// row backs the regulator-facing PDF / ZIP outputs (audit pack,
// VCNZ-controlled-drugs, etc.).
type ComplianceReportRecord struct {
	ID                  uuid.UUID
	ClinicID            uuid.UUID
	Type                string
	Vertical            string
	Country             string
	PeriodStart         time.Time
	PeriodEnd           time.Time
	Status              string
	FileKey             *string
	FileSizeBytes       *int64
	FileFormat          string
	ReportHash          *string
	RequestedBy         uuid.UUID
	RequestedAt         time.Time
	StartedAt           *time.Time
	CompletedAt         *time.Time
	ErrorMessage        *string
	GenerationMetadata  *string // JSONB as text
	DeliveredAt         *time.Time
	DeliveredToEmails   []string
}

// ReportScheduleRecord — one row of report_schedules. Recurring trigger
// for compliance-report generation + email delivery.
type ReportScheduleRecord struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	ReportType   string
	Frequency    string // daily | weekly | monthly | quarterly
	Recipients   []string
	Paused       bool
	NextRunAt    time.Time
	LastRunAt    *time.Time
	LastReportID *uuid.UUID
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ReportAuditRecord is one row of the report_audit append-only log.
type ReportAuditRecord struct {
	ID       uuid.UUID
	ReportID uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Action   string
	At       time.Time
	Details  *string
}

// ReportJobRecord is the raw DB representation of a report_jobs row.
type ReportJobRecord struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	ReportType  string
	Format      string
	Status      string
	Filters     *string // JSONB as text
	StorageKey  *string
	ContentHash *string
	ErrorMsg    *string
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// ── Param types ───────────────────────────────────────────────────────────────

// ReportFilters holds optional query filters shared across report types.
type ReportFilters struct {
	From      *time.Time `json:"from,omitempty"`
	To        *time.Time `json:"to,omitempty"`
	StaffID   *uuid.UUID `json:"staff_id,omitempty"`
	SubjectID *uuid.UUID `json:"subject_id,omitempty"`
	NoteID    *uuid.UUID `json:"note_id,omitempty"`
}

// ListParams holds pagination for report queries.
type ListParams struct {
	Limit  int
	Offset int
}

// CreateComplianceReportParams holds values for creating a new compliance
// report row (the regulator-facing kind, not the simple CSV export).
type CreateComplianceReportParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Type        string
	Vertical    string
	Country     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	FileFormat  string // "pdf" | "zip" | "csv"
	RequestedBy uuid.UUID
}

// ListComplianceReportsParams holds pagination + filters for listing
// compliance reports for a clinic.
type ListComplianceReportsParams struct {
	Limit  int
	Offset int
	Type   *string
	Status *string
	From   *time.Time
	To     *time.Time
}

// CreateReportScheduleParams holds values for creating a recurring schedule.
type CreateReportScheduleParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	ReportType string
	Frequency  string
	Recipients []string
	NextRunAt  time.Time
	CreatedBy  uuid.UUID
}

// UpdateReportScheduleParams — caller can pause / resume + edit recipients.
// Frequency is immutable once set; pause + delete + recreate to switch.
type UpdateReportScheduleParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	Recipients *[]string
	Paused     *bool
}

// LogReportAuditParams holds values for logging a report audit action.
type LogReportAuditParams struct {
	ID       uuid.UUID
	ReportID uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Action   string  // "generated" | "downloaded" | "shared_externally" | "deleted"
	Details  *string // JSON-encoded
}

// InsertReportJobParams holds values for creating a new report job row.
type InsertReportJobParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	ReportType string
	Format     string
	Filters    *string // JSON-encoded ReportFilters
	CreatedBy  uuid.UUID
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the PostgreSQL implementation for the reports module.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a reports Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── Report job CRUD ───────────────────────────────────────────────────────────

const jobCols = `id, clinic_id, report_type, format, status,
	filters::text, storage_key, content_hash, error_msg, created_by, created_at, completed_at`

// InsertReportJob creates a new pending report job row.
func (r *Repository) InsertReportJob(ctx context.Context, p InsertReportJobParams) (*ReportJobRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO report_jobs (id, clinic_id, report_type, format, filters, created_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING %s`, jobCols),
		p.ID, p.ClinicID, p.ReportType, p.Format, p.Filters, p.CreatedBy,
	)
	rec, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.InsertReportJob: %w", err)
	}
	return rec, nil
}

// GetReportJob fetches a job by ID scoped to the clinic.
func (r *Repository) GetReportJob(ctx context.Context, jobID, clinicID uuid.UUID) (*ReportJobRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM report_jobs WHERE id = $1 AND clinic_id = $2`, jobCols),
		jobID, clinicID,
	)
	rec, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.GetReportJob: %w", err)
	}
	return rec, nil
}

// GetReportJobInternal fetches a job by ID without clinic scope (used by the worker).
func (r *Repository) GetReportJobInternal(ctx context.Context, jobID uuid.UUID) (*ReportJobRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM report_jobs WHERE id = $1`, jobCols),
		jobID,
	)
	rec, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.GetReportJobInternal: %w", err)
	}
	return rec, nil
}

// MarkComplete sets a job to complete with its S3 storage key and content hash.
func (r *Repository) MarkComplete(ctx context.Context, jobID uuid.UUID, storageKey, contentHash string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE report_jobs
		SET status = 'complete', storage_key = $2, content_hash = $3, completed_at = NOW()
		WHERE id = $1`,
		jobID, storageKey, contentHash,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkComplete: %w", err)
	}
	return nil
}

// MarkFailed sets a job to failed with an error message.
func (r *Repository) MarkFailed(ctx context.Context, jobID uuid.UUID, errMsg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE report_jobs
		SET status = 'failed', error_msg = $2, completed_at = NOW()
		WHERE id = $1`,
		jobID, errMsg,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkFailed: %w", err)
	}
	return nil
}

// ── Report queries ────────────────────────────────────────────────────────────

const auditCols = `id, note_id, subject_id, clinic_id, event_type, field_id,
	old_value::text, new_value::text, actor_id, actor_role, reason, occurred_at`

// QueryClinicalAudit returns paginated note_events for a clinic with optional filters.
func (r *Repository) QueryClinicalAudit(ctx context.Context, clinicID uuid.UUID, f ReportFilters, p ListParams) ([]*AuditEventRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"

	args, where = applyTimeFilters(args, where, f.From, f.To)
	if f.StaffID != nil {
		args = append(args, *f.StaffID)
		where += fmt.Sprintf(" AND actor_id = $%d", len(args))
	}
	if f.SubjectID != nil {
		args = append(args, *f.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}
	if f.NoteID != nil {
		args = append(args, *f.NoteID)
		where += fmt.Sprintf(" AND note_id = $%d", len(args))
	}

	return r.queryAuditEvents(ctx, where, args, p)
}

// QueryStaffActions returns paginated note_events for a specific staff member.
func (r *Repository) QueryStaffActions(ctx context.Context, clinicID, staffID uuid.UUID, f ReportFilters, p ListParams) ([]*AuditEventRecord, int, error) {
	args := []any{clinicID, staffID}
	where := "clinic_id = $1 AND actor_id = $2"
	args, where = applyTimeFilters(args, where, f.From, f.To)
	return r.queryAuditEvents(ctx, where, args, p)
}

// QueryNoteHistory returns all events for a single note, oldest first.
func (r *Repository) QueryNoteHistory(ctx context.Context, noteID, clinicID uuid.UUID) ([]*AuditEventRecord, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM note_events
		WHERE note_id = $1 AND clinic_id = $2
		ORDER BY occurred_at ASC`, auditCols),
		noteID, clinicID,
	)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.QueryNoteHistory: %w", err)
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// QueryConsentLog returns submitted-note events (event_type = 'note.submitted') for a clinic.
func (r *Repository) QueryConsentLog(ctx context.Context, clinicID uuid.UUID, f ReportFilters, p ListParams) ([]*AuditEventRecord, int, error) {
	args := []any{clinicID, "note.submitted"}
	where := "clinic_id = $1 AND event_type = $2"

	args, where = applyTimeFilters(args, where, f.From, f.To)
	if f.SubjectID != nil {
		args = append(args, *f.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}

	return r.queryAuditEvents(ctx, where, args, p)
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (r *Repository) queryAuditEvents(ctx context.Context, where string, args []any, p ListParams) ([]*AuditEventRecord, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM note_events WHERE %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("reports.repo.queryAuditEvents: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	q := fmt.Sprintf(`
		SELECT %s FROM note_events WHERE %s
		ORDER BY occurred_at ASC
		LIMIT $%d OFFSET $%d`, auditCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("reports.repo.queryAuditEvents: %w", err)
	}
	defer rows.Close()

	events, err := scanAuditRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

func applyTimeFilters(args []any, where string, from, to *time.Time) ([]any, string) {
	if from != nil {
		args = append(args, *from)
		where += fmt.Sprintf(" AND occurred_at >= $%d", len(args))
	}
	if to != nil {
		args = append(args, *to)
		where += fmt.Sprintf(" AND occurred_at <= $%d", len(args))
	}
	return args, where
}

func scanAuditRows(rows pgx.Rows) ([]*AuditEventRecord, error) {
	var list []*AuditEventRecord
	for rows.Next() {
		e, err := scanAuditEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("reports.repo.scanAuditRows: %w", err)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reports.repo.scanAuditRows: rows: %w", err)
	}
	return list, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanAuditEvent(row scannable) (*AuditEventRecord, error) {
	var e AuditEventRecord
	err := row.Scan(
		&e.ID, &e.NoteID, &e.SubjectID, &e.ClinicID, &e.EventType, &e.FieldID,
		&e.OldValue, &e.NewValue, &e.ActorID, &e.ActorRole, &e.Reason, &e.OccurredAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanAuditEvent: %w", err)
	}
	return &e, nil
}

// ── Compliance reports CRUD (new `reports` + `report_audit` tables) ─────────

const complianceReportCols = `id, clinic_id, type, vertical, country,
	period_start, period_end, status,
	file_key, file_size_bytes, file_format, report_hash,
	requested_by, requested_at, started_at, completed_at,
	error_message, generation_metadata::text,
	delivered_at, delivered_to_emails`

// CreateComplianceReport inserts a queued compliance report row.
func (r *Repository) CreateComplianceReport(ctx context.Context, p CreateComplianceReportParams) (*ComplianceReportRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO reports (
			id, clinic_id, type, vertical, country,
			period_start, period_end, file_format, requested_by, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued')
		RETURNING %s`, complianceReportCols),
		p.ID, p.ClinicID, p.Type, p.Vertical, p.Country,
		p.PeriodStart, p.PeriodEnd, p.FileFormat, p.RequestedBy,
	)
	rec, err := scanComplianceReport(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.CreateComplianceReport: %w", err)
	}
	return rec, nil
}

// GetComplianceReport fetches one report scoped to clinic.
func (r *Repository) GetComplianceReport(ctx context.Context, id, clinicID uuid.UUID) (*ComplianceReportRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM reports WHERE id = $1 AND clinic_id = $2`, complianceReportCols),
		id, clinicID,
	)
	rec, err := scanComplianceReport(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.GetComplianceReport: %w", err)
	}
	return rec, nil
}

// GetComplianceReportInternal — without clinic scope; used by the worker.
func (r *Repository) GetComplianceReportInternal(ctx context.Context, id uuid.UUID) (*ComplianceReportRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM reports WHERE id = $1`, complianceReportCols),
		id,
	)
	rec, err := scanComplianceReport(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.GetComplianceReportInternal: %w", err)
	}
	return rec, nil
}

// ListComplianceReports — paginated + filterable listing for a clinic.
func (r *Repository) ListComplianceReports(ctx context.Context, clinicID uuid.UUID, p ListComplianceReportsParams) ([]*ComplianceReportRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"
	if p.Type != nil {
		args = append(args, *p.Type)
		where += fmt.Sprintf(" AND type = $%d", len(args))
	}
	if p.Status != nil {
		args = append(args, *p.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if p.From != nil {
		args = append(args, *p.From)
		where += fmt.Sprintf(" AND requested_at >= $%d", len(args))
	}
	if p.To != nil {
		args = append(args, *p.To)
		where += fmt.Sprintf(" AND requested_at <= $%d", len(args))
	}

	var total int
	if err := r.db.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM reports WHERE %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("reports.repo.ListComplianceReports: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	q := fmt.Sprintf(`
		SELECT %s FROM reports WHERE %s
		ORDER BY requested_at DESC
		LIMIT $%d OFFSET $%d`, complianceReportCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("reports.repo.ListComplianceReports: %w", err)
	}
	defer rows.Close()

	var list []*ComplianceReportRecord
	for rows.Next() {
		rec, err := scanComplianceReport(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("reports.repo.ListComplianceReports: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reports.repo.ListComplianceReports: rows: %w", err)
	}
	return list, total, nil
}

// MarkComplianceReportRunning flips status to running and stamps started_at.
func (r *Repository) MarkComplianceReportRunning(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE reports
		SET status = 'running', started_at = NOW()
		WHERE id = $1 AND status = 'queued'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkComplianceReportRunning: %w", err)
	}
	return nil
}

// MarkComplianceReportDone — successful generation; stores file metadata.
// Also nulls error_message so a row that retried after a prior failure
// doesn't keep showing the stale error in the UI.
func (r *Repository) MarkComplianceReportDone(ctx context.Context, id uuid.UUID, fileKey string, fileSize int64, hash string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE reports
		SET status = 'done',
		    file_key = $2,
		    file_size_bytes = $3,
		    report_hash = $4,
		    error_message = NULL,
		    completed_at = NOW()
		WHERE id = $1`,
		id, fileKey, fileSize, hash,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkComplianceReportDone: %w", err)
	}
	return nil
}

// MarkComplianceReportFailed — generation failure path.
func (r *Repository) MarkComplianceReportFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE reports
		SET status = 'failed',
		    error_message = $2,
		    completed_at = NOW()
		WHERE id = $1`,
		id, errMsg,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkComplianceReportFailed: %w", err)
	}
	return nil
}

// SetReportRecipients writes the delivered_to_emails column on a queued
// report. Used by the schedule-fire worker to stamp recipients before
// generation; the email worker reads them on completion.
func (r *Repository) SetReportRecipients(ctx context.Context, id uuid.UUID, recipients []string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE reports
		SET delivered_to_emails = $2
		WHERE id = $1`,
		id, recipients,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.SetReportRecipients: %w", err)
	}
	return nil
}

// GetReportRecipients returns the email list staged for a report's
// scheduled delivery, or an empty slice for ad-hoc reports.
func (r *Repository) GetReportRecipients(ctx context.Context, id uuid.UUID) ([]string, error) {
	var recipients []string
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(delivered_to_emails, ARRAY[]::TEXT[])
		FROM reports WHERE id = $1`,
		id,
	).Scan(&recipients)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.GetReportRecipients: %w", err)
	}
	return recipients, nil
}

// MarkReportDelivered stamps the delivered_at timestamp on a report after
// the email worker has sent the link to every recipient.
func (r *Repository) MarkReportDelivered(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE reports SET delivered_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkReportDelivered: %w", err)
	}
	return nil
}

// LogReportAudit appends a row to report_audit. Append-only by design.
func (r *Repository) LogReportAudit(ctx context.Context, p LogReportAuditParams) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO report_audit (id, report_id, clinic_id, staff_id, action, details)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		p.ID, p.ReportID, p.ClinicID, p.StaffID, p.Action, p.Details,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.LogReportAudit: %w", err)
	}
	return nil
}

// ── Report schedules CRUD (recurring trigger) ───────────────────────────────

const reportScheduleCols = `id, clinic_id, report_type, frequency,
	recipients, paused, next_run_at, last_run_at, last_report_id,
	created_by, created_at, updated_at`

func (r *Repository) CreateReportSchedule(ctx context.Context, p CreateReportScheduleParams) (*ReportScheduleRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO report_schedules (
			id, clinic_id, report_type, frequency, recipients, next_run_at, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING %s`, reportScheduleCols),
		p.ID, p.ClinicID, p.ReportType, p.Frequency, p.Recipients, p.NextRunAt, p.CreatedBy,
	)
	rec, err := scanReportSchedule(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.CreateReportSchedule: %w", err)
	}
	return rec, nil
}

func (r *Repository) GetReportSchedule(ctx context.Context, id, clinicID uuid.UUID) (*ReportScheduleRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM report_schedules WHERE id = $1 AND clinic_id = $2`, reportScheduleCols),
		id, clinicID,
	)
	rec, err := scanReportSchedule(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.GetReportSchedule: %w", err)
	}
	return rec, nil
}

func (r *Repository) ListReportSchedules(ctx context.Context, clinicID uuid.UUID) ([]*ReportScheduleRecord, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM report_schedules WHERE clinic_id = $1
		ORDER BY created_at DESC`, reportScheduleCols),
		clinicID,
	)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.ListReportSchedules: %w", err)
	}
	defer rows.Close()
	var out []*ReportScheduleRecord
	for rows.Next() {
		rec, err := scanReportSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("reports.repo.ListReportSchedules: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reports.repo.ListReportSchedules: rows: %w", err)
	}
	return out, nil
}

// ListDueReportSchedules returns every active schedule whose next_run_at
// has passed. The fire-loop calls this every hour and enqueues a report
// generation for each. Internal — no clinic scope.
func (r *Repository) ListDueReportSchedules(ctx context.Context, before time.Time) ([]*ReportScheduleRecord, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM report_schedules
		WHERE paused = FALSE AND next_run_at <= $1
		ORDER BY next_run_at ASC
		LIMIT 200`, reportScheduleCols),
		before,
	)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.ListDueReportSchedules: %w", err)
	}
	defer rows.Close()
	var out []*ReportScheduleRecord
	for rows.Next() {
		rec, err := scanReportSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("reports.repo.ListDueReportSchedules: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reports.repo.ListDueReportSchedules: rows: %w", err)
	}
	return out, nil
}

func (r *Repository) UpdateReportSchedule(ctx context.Context, p UpdateReportScheduleParams) (*ReportScheduleRecord, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{p.ID, p.ClinicID}
	if p.Recipients != nil {
		args = append(args, *p.Recipients)
		sets = append(sets, fmt.Sprintf("recipients = $%d", len(args)))
	}
	if p.Paused != nil {
		args = append(args, *p.Paused)
		sets = append(sets, fmt.Sprintf("paused = $%d", len(args)))
	}
	q := fmt.Sprintf(`
		UPDATE report_schedules SET %s
		WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, joinScheduleSets(sets), reportScheduleCols)
	row := r.db.QueryRow(ctx, q, args...)
	rec, err := scanReportSchedule(row)
	if err != nil {
		return nil, fmt.Errorf("reports.repo.UpdateReportSchedule: %w", err)
	}
	return rec, nil
}

func (r *Repository) DeleteReportSchedule(ctx context.Context, id, clinicID uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `
		DELETE FROM report_schedules WHERE id = $1 AND clinic_id = $2`,
		id, clinicID,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.DeleteReportSchedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("reports.repo.DeleteReportSchedule: %w", domain.ErrNotFound)
	}
	return nil
}

// MarkScheduleFired stamps the schedule with last_run_at + last_report_id
// and bumps next_run_at to the start of the next period. Called by the
// fire-loop right after enqueuing the report generation.
func (r *Repository) MarkScheduleFired(ctx context.Context, id uuid.UUID, lastReportID uuid.UUID, nextRunAt time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE report_schedules
		SET last_run_at = NOW(),
		    last_report_id = $2,
		    next_run_at = $3,
		    updated_at = NOW()
		WHERE id = $1`,
		id, lastReportID, nextRunAt,
	)
	if err != nil {
		return fmt.Errorf("reports.repo.MarkScheduleFired: %w", err)
	}
	return nil
}

func scanReportSchedule(row scannable) (*ReportScheduleRecord, error) {
	var s ReportScheduleRecord
	err := row.Scan(
		&s.ID, &s.ClinicID, &s.ReportType, &s.Frequency,
		&s.Recipients, &s.Paused, &s.NextRunAt, &s.LastRunAt, &s.LastReportID,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanReportSchedule: %w", err)
	}
	return &s, nil
}

func joinScheduleSets(sets []string) string {
	out := ""
	for i, s := range sets {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func scanComplianceReport(row scannable) (*ComplianceReportRecord, error) {
	var c ComplianceReportRecord
	err := row.Scan(
		&c.ID, &c.ClinicID, &c.Type, &c.Vertical, &c.Country,
		&c.PeriodStart, &c.PeriodEnd, &c.Status,
		&c.FileKey, &c.FileSizeBytes, &c.FileFormat, &c.ReportHash,
		&c.RequestedBy, &c.RequestedAt, &c.StartedAt, &c.CompletedAt,
		&c.ErrorMessage, &c.GenerationMetadata,
		&c.DeliveredAt, &c.DeliveredToEmails,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanComplianceReport: %w", err)
	}
	return &c, nil
}

func scanJob(row scannable) (*ReportJobRecord, error) {
	var j ReportJobRecord
	err := row.Scan(
		&j.ID, &j.ClinicID, &j.ReportType, &j.Format, &j.Status,
		&j.Filters, &j.StorageKey, &j.ContentHash, &j.ErrorMsg,
		&j.CreatedBy, &j.CreatedAt, &j.CompletedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanJob: %w", err)
	}
	return &j, nil
}
