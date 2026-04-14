package notes

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

// NoteRecord is the raw database representation of a notes row.
type NoteRecord struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	RecordingID   uuid.UUID
	FormVersionID uuid.UUID
	SubjectID     *uuid.UUID
	CreatedBy     uuid.UUID
	Status        domain.NoteStatus
	ErrorMessage  *string
	SubmittedAt   *time.Time
	SubmittedBy   *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NoteFieldRecord is the raw database representation of a note_fields row.
type NoteFieldRecord struct {
	ID           uuid.UUID
	NoteID       uuid.UUID
	FieldID      uuid.UUID
	Value        *string  // JSON-encoded
	Confidence   *float64 // 0.0–1.0
	SourceQuote  *string
	OverriddenBy *uuid.UUID
	OverriddenAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreateNoteParams holds values for inserting a new note row.
type CreateNoteParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	RecordingID   uuid.UUID
	FormVersionID uuid.UUID
	SubjectID     *uuid.UUID
	CreatedBy     uuid.UUID
}

// ListNotesParams holds filter and pagination for listing notes.
type ListNotesParams struct {
	Limit       int
	Offset      int
	RecordingID *uuid.UUID
	SubjectID   *uuid.UUID
	Status      *domain.NoteStatus
}

// SubmitNoteParams holds values for marking a note as submitted.
type SubmitNoteParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	SubmittedBy uuid.UUID
	SubmittedAt time.Time
}

// UpsertFieldParams holds values for inserting or updating a note_field row.
// Used by the extraction job to write AI results in bulk.
type UpsertFieldParams struct {
	ID          uuid.UUID
	NoteID      uuid.UUID
	FieldID     uuid.UUID
	Value       *string
	Confidence  *float64
	SourceQuote *string
}

// UpdateNoteFieldParams holds values for a staff override of a single field.
type UpdateNoteFieldParams struct {
	NoteID       uuid.UUID
	FieldID      uuid.UUID
	ClinicID     uuid.UUID
	Value        *string
	OverriddenBy uuid.UUID
	OverriddenAt time.Time
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the PostgreSQL implementation of the notes repo interface.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a notes Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── Notes ─────────────────────────────────────────────────────────────────────

const noteCols = `id, clinic_id, recording_id, form_version_id, subject_id, created_by,
	status, error_message, submitted_at, submitted_by, created_at, updated_at`

// CreateNote inserts a new note in 'extracting' status.
func (r *Repository) CreateNote(ctx context.Context, p CreateNoteParams) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO notes (id, clinic_id, recording_id, form_version_id, subject_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.RecordingID, p.FormVersionID, p.SubjectID, p.CreatedBy,
	)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.CreateNote: %w", err)
	}
	return rec, nil
}

// GetNoteByID fetches a note by ID scoped to the clinic.
func (r *Repository) GetNoteByID(ctx context.Context, id, clinicID uuid.UUID) (*NoteRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM notes WHERE id = $1 AND clinic_id = $2`, noteCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.GetNoteByID: %w", err)
	}
	return rec, nil
}

// ListNotes returns a paginated list of notes for a clinic.
func (r *Repository) ListNotes(ctx context.Context, clinicID uuid.UUID, p ListNotesParams) ([]*NoteRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"

	if p.RecordingID != nil {
		args = append(args, *p.RecordingID)
		where += fmt.Sprintf(" AND recording_id = $%d", len(args))
	}
	if p.SubjectID != nil {
		args = append(args, *p.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}
	if p.Status != nil {
		args = append(args, string(*p.Status))
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	var total int
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM notes WHERE %s", where)
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("notes.repo.ListNotes: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	listQ := fmt.Sprintf(`
		SELECT %s FROM notes WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, noteCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("notes.repo.ListNotes: %w", err)
	}
	defer rows.Close()

	var list []*NoteRecord
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("notes.repo.ListNotes: %w", err)
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("notes.repo.ListNotes: rows: %w", err)
	}
	return list, total, nil
}

// UpdateNoteStatus transitions a note to a new status.
func (r *Repository) UpdateNoteStatus(ctx context.Context, id uuid.UUID, status domain.NoteStatus, errMsg *string) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		UPDATE notes SET status = $2, error_message = $3
		WHERE id = $1
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q, id, string(status), errMsg)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.UpdateNoteStatus: %w", err)
	}
	return rec, nil
}

// SubmitNote marks a note as submitted.
func (r *Repository) SubmitNote(ctx context.Context, p SubmitNoteParams) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		UPDATE notes
		SET status = 'submitted', submitted_by = $3, submitted_at = $4
		WHERE id = $1 AND clinic_id = $2 AND status = 'draft'
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.SubmittedBy, p.SubmittedAt)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.SubmitNote: %w", err)
	}
	return rec, nil
}

// CountNotesByRecording returns how many notes exist for a recording.
func (r *Repository) CountNotesByRecording(ctx context.Context, recordingID uuid.UUID) (int, error) {
	var count int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM notes WHERE recording_id = $1`, recordingID).Scan(&count); err != nil {
		return 0, fmt.Errorf("notes.repo.CountNotesByRecording: %w", err)
	}
	return count, nil
}

// ── Note fields ───────────────────────────────────────────────────────────────

const fieldCols = `id, note_id, field_id, value, confidence, source_quote,
	overridden_by, overridden_at, created_at, updated_at`

// UpsertNoteFields inserts or replaces note_field rows in bulk (extraction job output).
func (r *Repository) UpsertNoteFields(ctx context.Context, noteID uuid.UUID, fields []UpsertFieldParams) ([]*NoteFieldRecord, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	q := fmt.Sprintf(`
		INSERT INTO note_fields (id, note_id, field_id, value, confidence, source_quote)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (note_id, field_id) DO UPDATE
		SET value = EXCLUDED.value,
		    confidence = EXCLUDED.confidence,
		    source_quote = EXCLUDED.source_quote
		RETURNING %s`, fieldCols)

	result := make([]*NoteFieldRecord, 0, len(fields))
	for _, p := range fields {
		row := r.db.QueryRow(ctx, q, p.ID, noteID, p.FieldID, p.Value, p.Confidence, p.SourceQuote)
		f, err := scanField(row)
		if err != nil {
			return nil, fmt.Errorf("notes.repo.UpsertNoteFields: %w", err)
		}
		result = append(result, f)
	}
	return result, nil
}

// GetNoteFields returns all fields for a note, ordered by their form field position.
func (r *Repository) GetNoteFields(ctx context.Context, noteID uuid.UUID) ([]*NoteFieldRecord, error) {
	q := fmt.Sprintf(`
		SELECT nf.%s FROM note_fields nf
		JOIN form_fields ff ON ff.id = nf.field_id
		WHERE nf.note_id = $1
		ORDER BY ff.position`, fieldCols)

	rows, err := r.db.Query(ctx, q, noteID)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.GetNoteFields: %w", err)
	}
	defer rows.Close()

	var list []*NoteFieldRecord
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, fmt.Errorf("notes.repo.GetNoteFields: %w", err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notes.repo.GetNoteFields: rows: %w", err)
	}
	return list, nil
}

// UpdateNoteField records a staff override on a single field.
func (r *Repository) UpdateNoteField(ctx context.Context, p UpdateNoteFieldParams) (*NoteFieldRecord, error) {
	// Verify note belongs to clinic before updating.
	var noteClinic uuid.UUID
	if err := r.db.QueryRow(ctx, `SELECT clinic_id FROM notes WHERE id = $1`, p.NoteID).Scan(&noteClinic); err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("notes.repo.UpdateNoteField: clinic check: %w", err)
	}
	if noteClinic != p.ClinicID {
		return nil, domain.ErrForbidden
	}

	q := fmt.Sprintf(`
		UPDATE note_fields
		SET value = $3, overridden_by = $4, overridden_at = $5
		WHERE note_id = $1 AND field_id = $2
		RETURNING %s`, fieldCols)

	row := r.db.QueryRow(ctx, q, p.NoteID, p.FieldID, p.Value, p.OverriddenBy, p.OverriddenAt)
	f, err := scanField(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.UpdateNoteField: %w", err)
	}
	return f, nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanNote(row scannable) (*NoteRecord, error) {
	var n NoteRecord
	err := row.Scan(
		&n.ID, &n.ClinicID, &n.RecordingID, &n.FormVersionID, &n.SubjectID,
		&n.CreatedBy, &n.Status, &n.ErrorMessage,
		&n.SubmittedAt, &n.SubmittedBy, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanNote: %w", err)
	}
	return &n, nil
}

func scanField(row scannable) (*NoteFieldRecord, error) {
	var f NoteFieldRecord
	err := row.Scan(
		&f.ID, &f.NoteID, &f.FieldID, &f.Value, &f.Confidence, &f.SourceQuote,
		&f.OverriddenBy, &f.OverriddenAt, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanField: %w", err)
	}
	return &f, nil
}
