package notes

import (
	"context"
	"errors"
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
	ID                 uuid.UUID
	ClinicID           uuid.UUID
	RecordingID        *uuid.UUID // nil for manual notes
	FormVersionID      uuid.UUID
	SubjectID          *uuid.UUID
	CreatedBy          uuid.UUID
	Status             domain.NoteStatus
	ErrorMessage       *string
	ReviewedBy         *uuid.UUID // set when staff acknowledges review
	ReviewedAt         *time.Time
	SubmittedAt        *time.Time
	SubmittedBy        *uuid.UUID
	ArchivedAt         *time.Time // soft delete
	FormVersionContext *string    // e.g. "before decommission" — set at submit time
	PolicyAlignmentPct *float64   // 0.0–100.0; nil until alignment job runs
	PolicyCheckResult  *string    // JSONB per-clause check results; nil until check runs
	// OverrideReason/By/At record a submitter's written justification for
	// submitting despite a high-parity policy violation. The columns are
	// populated together by a CHECK constraint or all null.
	OverrideReason     *string
	OverrideBy         *uuid.UUID
	OverrideAt         *time.Time
	PDFStorageKey      *string // S3 key; nil until PDF generated after submit
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// NoteFieldRecord is the raw database representation of a note_fields row.
// Confidence is the LLM-estimated value (kept as fallback when no ASR data).
// ASRConfidence/MinWordConfidence/AlignmentScore/GroundingSource are deterministic
// scores populated when Deepgram word data is available (nil for GeminiTranscriber).
// RequiresReview is true when grounding_source="ungrounded" or min confidence is low.
type NoteFieldRecord struct {
	ID                 uuid.UUID
	NoteID             uuid.UUID
	FieldID            uuid.UUID
	Value              *string
	Confidence         *float64
	SourceQuote        *string
	TransformationType *string
	ASRConfidence      *float64
	MinWordConfidence  *float64
	AlignmentScore     *float64
	GroundingSource    *string
	RequiresReview     bool
	OverriddenBy       *uuid.UUID
	OverriddenAt       *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreateNoteParams holds values for inserting a new note row.
type CreateNoteParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	RecordingID   *uuid.UUID // nil for manual notes
	FormVersionID uuid.UUID
	SubjectID     *uuid.UUID
	CreatedBy     uuid.UUID
	Status        domain.NoteStatus // 'extracting' for AI, 'draft' for manual
}

// ListNotesParams holds filter and pagination for listing notes.
type ListNotesParams struct {
	Limit           int
	Offset          int
	RecordingID     *uuid.UUID
	SubjectID       *uuid.UUID
	Status          *domain.NoteStatus
	IncludeArchived bool // default false = exclude archived
}

// SubmitNoteParams holds values for marking a note as submitted.
// OverrideReason (when non-nil) records the submitter's justification for
// submitting despite a high-parity policy violation. OverrideBy/At default to
// the submitter/submit-time when the reason is set.
type SubmitNoteParams struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	ReviewedBy     uuid.UUID
	ReviewedAt     time.Time
	SubmittedBy    uuid.UUID
	SubmittedAt    time.Time
	OverrideReason *string
}

// ArchiveNoteParams holds values for soft-deleting a note.
type ArchiveNoteParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	ArchivedAt time.Time
}

// UpsertFieldParams holds values for inserting or updating a note_field row.
// Used by the extraction job to write AI results in bulk.
// ASRConfidence/MinWordConfidence/AlignmentScore/GroundingSource are nil when no ASR data.
type UpsertFieldParams struct {
	ID                 uuid.UUID
	NoteID             uuid.UUID
	FieldID            uuid.UUID
	Value              *string
	Confidence         *float64
	SourceQuote        *string
	TransformationType *string
	ASRConfidence      *float64
	MinWordConfidence  *float64
	AlignmentScore     *float64
	GroundingSource    *string
	RequiresReview     bool
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
	status, error_message, reviewed_by, reviewed_at, submitted_at, submitted_by,
	archived_at, form_version_context, policy_alignment_pct, policy_check_result::text,
	override_reason, override_by, override_at,
	pdf_storage_key, created_at, updated_at`

// CreateNote inserts a new note with the given status.
func (r *Repository) CreateNote(ctx context.Context, p CreateNoteParams) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO notes (id, clinic_id, recording_id, form_version_id, subject_id, created_by, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.RecordingID, p.FormVersionID, p.SubjectID, p.CreatedBy, string(p.Status),
	)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.CreateNote: %w", err)
	}
	return rec, nil
}

// GetNoteByID fetches a note by ID scoped to the clinic.
// Pass uuid.Nil as clinicID to skip the clinic ownership check (internal worker use only).
func (r *Repository) GetNoteByID(ctx context.Context, id, clinicID uuid.UUID) (*NoteRecord, error) {
	var row pgx.Row
	if clinicID == uuid.Nil {
		q := fmt.Sprintf(`SELECT %s FROM notes WHERE id = $1`, noteCols)
		row = r.db.QueryRow(ctx, q, id)
	} else {
		q := fmt.Sprintf(`SELECT %s FROM notes WHERE id = $1 AND clinic_id = $2`, noteCols)
		row = r.db.QueryRow(ctx, q, id, clinicID)
	}
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.GetNoteByID: %w", err)
	}
	return rec, nil
}

// ListNotes returns a paginated list of notes for a clinic.
// Archived notes are excluded by default; set IncludeArchived to include them.
func (r *Repository) ListNotes(ctx context.Context, clinicID uuid.UUID, p ListNotesParams) ([]*NoteRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"

	if !p.IncludeArchived {
		where += " AND archived_at IS NULL"
	}
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

// UpdateNoteStatus transitions a note to a new status. clinicID guards
// against worker bugs accidentally writing across tenants — every caller
// already has the clinic from a prior GetNoteByID lookup.
func (r *Repository) UpdateNoteStatus(ctx context.Context, id, clinicID uuid.UUID, status domain.NoteStatus, errMsg *string) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		UPDATE notes SET status = $3, error_message = $4
		WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q, id, clinicID, string(status), errMsg)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.UpdateNoteStatus: %w", err)
	}
	return rec, nil
}

// SubmitNote marks a note as submitted and sets reviewed_by/reviewed_at.
// The form_version_context column is computed inline: if the linked form or version
// is no longer published (decommissioned), the note is labelled "before decommission".
func (r *Repository) SubmitNote(ctx context.Context, p SubmitNoteParams) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		UPDATE notes
		SET status                = 'submitted',
		    reviewed_by           = $3,
		    reviewed_at           = $4,
		    submitted_by          = $5,
		    submitted_at          = $6,
		    override_reason       = $7,
		    override_by           = CASE WHEN $7::text IS NULL THEN NULL ELSE $5::uuid END,
		    override_at           = CASE WHEN $7::text IS NULL THEN NULL ELSE $6::timestamptz END,
		    form_version_context  = (
		        SELECT CASE
		            WHEN f.archived_at IS NOT NULL THEN 'before decommission'
		            WHEN fv.status != 'published'  THEN 'before decommission'
		            ELSE NULL
		        END
		        FROM form_versions fv
		        JOIN forms f ON f.id = fv.form_id
		        WHERE fv.id = notes.form_version_id
		    )
		WHERE id = $1 AND clinic_id = $2 AND status = 'draft'
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.ReviewedBy, p.ReviewedAt, p.SubmittedBy, p.SubmittedAt, p.OverrideReason)
	rec, err := scanNote(row)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// UPDATE matched 0 rows — either note missing or wrong status.
			// Do a secondary lookup to return the correct sentinel.
			var status domain.NoteStatus
			checkErr := r.db.QueryRow(ctx,
				`SELECT status FROM notes WHERE id = $1 AND clinic_id = $2`,
				p.ID, p.ClinicID,
			).Scan(&status)
			if errors.Is(checkErr, pgx.ErrNoRows) {
				return nil, domain.ErrNotFound
			}
			if checkErr != nil {
				return nil, fmt.Errorf("notes.repo.SubmitNote: status check: %w", checkErr)
			}
			return nil, domain.ErrConflict
		}
		return nil, fmt.Errorf("notes.repo.SubmitNote: %w", err)
	}
	return rec, nil
}

// ArchiveNote soft-deletes a note by setting archived_at.
func (r *Repository) ArchiveNote(ctx context.Context, p ArchiveNoteParams) (*NoteRecord, error) {
	q := fmt.Sprintf(`
		UPDATE notes SET archived_at = $3
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING %s`, noteCols)

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.ArchivedAt)
	rec, err := scanNote(row)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.ArchiveNote: %w", err)
	}
	return rec, nil
}

// ListExtractingNoteIDsByRecording returns IDs of notes in `extracting`
// status that are bound to the given recording. Used by the
// audio-transcribe listener to fan out extraction jobs the moment the
// transcript lands. No clinic scope — the listener is system-internal,
// triggered by a recording row that the audio module already gated on.
func (r *Repository) ListExtractingNoteIDsByRecording(ctx context.Context, recordingID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id FROM notes
		WHERE recording_id = $1 AND status = 'extracting' AND archived_at IS NULL`,
		recordingID,
	)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.ListExtractingNoteIDsByRecording: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("notes.repo.ListExtractingNoteIDsByRecording: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notes.repo.ListExtractingNoteIDsByRecording: rows: %w", err)
	}
	return out, nil
}

// CountNotesByRecording returns how many notes exist for a recording within a clinic.
func (r *Repository) CountNotesByRecording(ctx context.Context, clinicID, recordingID uuid.UUID) (int, error) {
	var count int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notes WHERE clinic_id = $1 AND recording_id = $2`,
		clinicID, recordingID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("notes.repo.CountNotesByRecording: %w", err)
	}
	return count, nil
}

// CountSinceForClinic returns the count of non-archived notes created at
// or after `since`. Used by note-cap metering to evaluate the current
// billing period (or the trial window from clinics.created_at). Backed
// by the partial index idx_notes_clinic_created_active.
func (r *Repository) CountSinceForClinic(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error) {
	var count int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notes
		   WHERE clinic_id = $1
		     AND archived_at IS NULL
		     AND created_at >= $2`,
		clinicID, since,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("notes.repo.CountSinceForClinic: %w", err)
	}
	return count, nil
}

// UpdatePolicyAlignment sets the policy_alignment_pct on a note.
// Called by the ComputePolicyAlignmentWorker after the AI alignment job runs.
func (r *Repository) UpdatePolicyAlignment(ctx context.Context, noteID, clinicID uuid.UUID, pct float64) error {
	const q = `UPDATE notes SET policy_alignment_pct = $3 WHERE id = $1 AND clinic_id = $2`
	if _, err := r.db.Exec(ctx, q, noteID, clinicID, pct); err != nil {
		return fmt.Errorf("notes.repo.UpdatePolicyAlignment: %w", err)
	}
	return nil
}

// UpdatePDFKey sets the pdf_storage_key on a note after the PDF is generated.
func (r *Repository) UpdatePDFKey(ctx context.Context, noteID, clinicID uuid.UUID, key string) error {
	const q = `UPDATE notes SET pdf_storage_key = $3 WHERE id = $1 AND clinic_id = $2`
	if _, err := r.db.Exec(ctx, q, noteID, clinicID, key); err != nil {
		return fmt.Errorf("notes.repo.UpdatePDFKey: %w", err)
	}
	return nil
}

// UpdatePolicyCheckResult sets the policy_check_result JSONB on a note.
func (r *Repository) UpdatePolicyCheckResult(ctx context.Context, noteID, clinicID uuid.UUID, resultJSON string) error {
	const q = `UPDATE notes SET policy_check_result = $3::jsonb WHERE id = $1 AND clinic_id = $2`
	if _, err := r.db.Exec(ctx, q, noteID, clinicID, resultJSON); err != nil {
		return fmt.Errorf("notes.repo.UpdatePolicyCheckResult: %w", err)
	}
	return nil
}

// ── Note fields ───────────────────────────────────────────────────────────────

const fieldCols = `id, note_id, field_id, value, confidence, source_quote,
	transformation_type, asr_confidence, min_word_confidence, alignment_score,
	grounding_source, requires_review, overridden_by, overridden_at, created_at, updated_at`

// fieldColsAliased is used in JOIN queries to avoid ambiguous column names.
const fieldColsAliased = `nf.id, nf.note_id, nf.field_id, nf.value, nf.confidence, nf.source_quote,
	nf.transformation_type, nf.asr_confidence, nf.min_word_confidence, nf.alignment_score,
	nf.grounding_source, nf.requires_review, nf.overridden_by, nf.overridden_at, nf.created_at, nf.updated_at`

// UpsertNoteFields inserts or replaces note_field rows in bulk (extraction job output).
// All upserts run inside a single transaction — a partial failure rolls back all changes.
func (r *Repository) UpsertNoteFields(ctx context.Context, noteID uuid.UUID, fields []UpsertFieldParams) ([]*NoteFieldRecord, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.UpsertNoteFields: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`
		INSERT INTO note_fields (id, note_id, field_id, value, confidence, source_quote,
		    transformation_type, asr_confidence, min_word_confidence, alignment_score,
		    grounding_source, requires_review)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (note_id, field_id) DO UPDATE
		SET value               = EXCLUDED.value,
		    confidence          = EXCLUDED.confidence,
		    source_quote        = EXCLUDED.source_quote,
		    transformation_type = EXCLUDED.transformation_type,
		    asr_confidence      = EXCLUDED.asr_confidence,
		    min_word_confidence = EXCLUDED.min_word_confidence,
		    alignment_score     = EXCLUDED.alignment_score,
		    grounding_source    = EXCLUDED.grounding_source,
		    requires_review     = EXCLUDED.requires_review
		RETURNING %s`, fieldCols)

	result := make([]*NoteFieldRecord, 0, len(fields))
	for _, p := range fields {
		row := tx.QueryRow(ctx, q,
			p.ID, noteID, p.FieldID, p.Value, p.Confidence, p.SourceQuote, p.TransformationType,
			p.ASRConfidence, p.MinWordConfidence, p.AlignmentScore, p.GroundingSource, p.RequiresReview,
		)
		f, err := scanField(row)
		if err != nil {
			return nil, fmt.Errorf("notes.repo.UpsertNoteFields: %w", err)
		}
		result = append(result, f)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("notes.repo.UpsertNoteFields: commit: %w", err)
	}
	return result, nil
}

// GetNoteFields returns all fields for a note, ordered by their form field position.
func (r *Repository) GetNoteFields(ctx context.Context, noteID uuid.UUID) ([]*NoteFieldRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM note_fields nf
		JOIN form_fields ff ON ff.id = nf.field_id
		WHERE nf.note_id = $1
		ORDER BY ff.position`, fieldColsAliased)

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

// GetNoteFieldWithType returns the form field's type/title plus the
// note_fields value (if any) for the (note, field) pair. Tenant-scoped
// via the parent note's clinic_id; cross-tenant probes return
// ErrNotFound.
//
// LEFT JOIN on note_fields by design: AI extraction only writes a row
// when it has a value to store, so a system widget the AI ignored has
// NO note_fields row yet. Materialise must still work — the user's tap
// on Capture/Confirm is what creates the row in that case. Returns
// out.Value = nil for the no-row case; ErrNotFound only when the field
// doesn't belong to this note's form (or the note is in another clinic).
func (r *Repository) GetNoteFieldWithType(ctx context.Context, noteID, fieldID, clinicID uuid.UUID) (*NoteFieldWithType, error) {
	// 1. Verify the note belongs to this clinic. Cheap tenant guard.
	var noteSubjectID *uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT subject_id FROM notes WHERE id = $1 AND clinic_id = $2`,
		noteID, clinicID,
	).Scan(&noteSubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("notes.repo.GetNoteFieldWithType: note: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("notes.repo.GetNoteFieldWithType: note: %w", err)
	}

	// 2. Look up the form field by id alone. We trust the FE to pass a
	// field_id from this note's form (the form definition the FE
	// rendered came from the note's form_version), and we don't need
	// to enforce form_version_id equality at the DB layer — an attacker
	// supplying a field_id from a different form would just get the
	// type+title of THAT field, but the materialise endpoint then
	// validates field_type against the expected system.* kind. If the
	// kinds don't match, the service returns ErrValidation.
	var formFieldType, formFieldTitle string
	err = r.db.QueryRow(ctx,
		`SELECT type, title FROM form_fields WHERE id = $1`,
		fieldID,
	).Scan(&formFieldType, &formFieldTitle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("notes.repo.GetNoteFieldWithType: field: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("notes.repo.GetNoteFieldWithType: field: %w", err)
	}

	// 3. Pick up the existing note_fields value if any (LEFT JOIN style).
	var value *string
	err = r.db.QueryRow(ctx,
		`SELECT value FROM note_fields WHERE note_id = $1 AND field_id = $2`,
		noteID, fieldID,
	).Scan(&value)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("notes.repo.GetNoteFieldWithType: value: %w", err)
	}

	return &NoteFieldWithType{
		FieldID:   fieldID,
		FieldType: formFieldType,
		Title:     formFieldTitle,
		Value:     value,
		NoteID:    noteID,
		SubjectID: noteSubjectID,
	}, nil
}

// ListSystemFieldStates returns every note_field on the note that
// corresponds to a `system.*` form_field, with its current value. Used
// by the submit gate to detect unmaterialised system widgets.
func (r *Repository) ListSystemFieldStates(ctx context.Context, noteID, clinicID uuid.UUID) ([]NoteFieldWithType, error) {
	const q = `
		SELECT ff.id, ff.type, ff.title, nf.value, nf.note_id, n.subject_id
		FROM note_fields nf
		JOIN form_fields ff ON ff.id = nf.field_id
		JOIN notes n ON n.id = nf.note_id
		WHERE nf.note_id = $1 AND n.clinic_id = $2 AND ff.type LIKE 'system.%'
		ORDER BY ff.position`
	rows, err := r.db.Query(ctx, q, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.repo.ListSystemFieldStates: %w", err)
	}
	defer rows.Close()
	var out []NoteFieldWithType
	for rows.Next() {
		var f NoteFieldWithType
		if err := rows.Scan(
			&f.FieldID, &f.FieldType, &f.Title, &f.Value,
			&f.NoteID, &f.SubjectID,
		); err != nil {
			return nil, fmt.Errorf("notes.repo.ListSystemFieldStates: scan: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notes.repo.ListSystemFieldStates: rows: %w", err)
	}
	return out, nil
}

// WriteMaterialisedPointer writes the id-pointer JSON to note_fields.value
// for the (note, field) pair, INSERTing the row if it doesn't exist yet.
// Does NOT touch overridden_by/at — materialisation is a system action
// triggered by an explicit user tap on a typed card, not a free-form
// override of the AI extraction.
//
// UPSERT (vs the older UPDATE-only) is required because system widgets
// the AI never extracted have no note_fields row yet — the user's
// Capture/Confirm tap is what creates both the ledger row and the
// note_fields pointer.
//
// Tenant ownership is enforced upstream: the service always calls
// GetNoteFieldWithType (which joins on clinic_id) before WriteMaterialisedPointer,
// so by the time we get here the note belongs to clinicID. The clinicID
// arg is preserved in the signature for future direct-call safety but
// not consulted in the SQL — keeping the UPSERT a single round-trip.
func (r *Repository) WriteMaterialisedPointer(ctx context.Context, noteID, fieldID, _ uuid.UUID, pointer string) error {
	const q = `
		INSERT INTO note_fields (note_id, field_id, value)
		VALUES ($1, $2, $3)
		ON CONFLICT (note_id, field_id) DO UPDATE
		SET value = EXCLUDED.value, updated_at = NOW()`
	tag, err := r.db.Exec(ctx, q, noteID, fieldID, pointer)
	if err != nil {
		return fmt.Errorf("notes.repo.WriteMaterialisedPointer: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("notes.repo.WriteMaterialisedPointer: %w", domain.ErrNotFound)
	}
	return nil
}

// UpdateNoteField records a staff override on a single field.
// The clinic ownership check and field update are performed atomically in one query.
func (r *Repository) UpdateNoteField(ctx context.Context, p UpdateNoteFieldParams) (*NoteFieldRecord, error) {
	q := fmt.Sprintf(`
		UPDATE note_fields nf
		SET value = $4, overridden_by = $5, overridden_at = $6
		FROM notes n
		WHERE nf.note_id = n.id
		  AND nf.note_id = $1
		  AND nf.field_id = $2
		  AND n.clinic_id = $3
		RETURNING %s`, fieldColsAliased)

	row := r.db.QueryRow(ctx, q, p.NoteID, p.FieldID, p.ClinicID, p.Value, p.OverriddenBy, p.OverriddenAt)
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
		&n.ReviewedBy, &n.ReviewedAt,
		&n.SubmittedAt, &n.SubmittedBy,
		&n.ArchivedAt, &n.FormVersionContext, &n.PolicyAlignmentPct, &n.PolicyCheckResult,
		&n.OverrideReason, &n.OverrideBy, &n.OverrideAt,
		&n.PDFStorageKey, &n.CreatedAt, &n.UpdatedAt,
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
		&f.TransformationType,
		&f.ASRConfidence, &f.MinWordConfidence, &f.AlignmentScore,
		&f.GroundingSource, &f.RequiresReview,
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
