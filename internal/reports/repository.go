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
