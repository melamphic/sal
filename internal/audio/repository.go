package audio

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// RecordingRecord is the raw database representation of a recording row.
type RecordingRecord struct {
	ID              uuid.UUID
	ClinicID        uuid.UUID
	StaffID         uuid.UUID
	SubjectID       *uuid.UUID
	Status          domain.RecordingStatus
	FileKey         string
	ContentType     string
	DurationSeconds *int
	Transcript      *string
	ErrorMessage    *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreateRecordingParams holds values needed to insert a new recording row.
type CreateRecordingParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	SubjectID   *uuid.UUID
	FileKey     string
	ContentType string
}

// ListRecordingsParams holds filters and pagination for listing recordings.
type ListRecordingsParams struct {
	Limit     int
	Offset    int
	SubjectID *uuid.UUID
	StaffID   *uuid.UUID
	Status    *domain.RecordingStatus
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the PostgreSQL implementation of the audio repo interface.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs an audio Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateRecording inserts a new recording row in pending_upload status.
func (r *Repository) CreateRecording(ctx context.Context, p CreateRecordingParams) (*RecordingRecord, error) {
	const q = `
		INSERT INTO recordings (id, clinic_id, staff_id, subject_id, status, file_key, content_type)
		VALUES ($1, $2, $3, $4, 'pending_upload', $5, $6)
		RETURNING id, clinic_id, staff_id, subject_id, status, file_key, content_type,
		          duration_seconds, transcript, error_message, created_at, updated_at`

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.StaffID, p.SubjectID, p.FileKey, p.ContentType,
	)
	rec, err := scanRecording(row)
	if err != nil {
		return nil, fmt.Errorf("audio.repo.CreateRecording: %w", err)
	}
	return rec, nil
}

// GetRecordingByID fetches a single recording, scoped to the clinic.
// Returns domain.ErrNotFound if the recording does not exist or belongs to another clinic.
func (r *Repository) GetRecordingByID(ctx context.Context, id, clinicID uuid.UUID) (*RecordingRecord, error) {
	const q = `
		SELECT id, clinic_id, staff_id, subject_id, status, file_key, content_type,
		       duration_seconds, transcript, error_message, created_at, updated_at
		FROM recordings
		WHERE id = $1 AND clinic_id = $2`

	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanRecording(row)
	if err != nil {
		return nil, fmt.Errorf("audio.repo.GetRecordingByID: %w", err)
	}
	return rec, nil
}

// ListRecordings returns a paginated list of recordings for a clinic with optional filters.
func (r *Repository) ListRecordings(ctx context.Context, clinicID uuid.UUID, p ListRecordingsParams) ([]*RecordingRecord, int, error) {
	// Build a dynamic WHERE clause.
	args := []any{clinicID}
	where := "WHERE clinic_id = $1"

	if p.SubjectID != nil {
		args = append(args, *p.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}
	if p.StaffID != nil {
		args = append(args, *p.StaffID)
		where += fmt.Sprintf(" AND staff_id = $%d", len(args))
	}
	if p.Status != nil {
		args = append(args, string(*p.Status))
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	// Count total before pagination.
	var total int
	countQ := "SELECT COUNT(*) FROM recordings " + where
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audio.repo.ListRecordings: count: %w", err)
	}

	// Fetch page.
	args = append(args, p.Limit, p.Offset)
	listQ := fmt.Sprintf(`
		SELECT id, clinic_id, staff_id, subject_id, status, file_key, content_type,
		       duration_seconds, transcript, error_message, created_at, updated_at
		FROM recordings
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("audio.repo.ListRecordings: query: %w", err)
	}
	defer rows.Close()

	var recs []*RecordingRecord
	for rows.Next() {
		rec, err := scanRecording(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("audio.repo.ListRecordings: scan: %w", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("audio.repo.ListRecordings: rows: %w", err)
	}
	if recs == nil {
		recs = []*RecordingRecord{}
	}
	return recs, total, nil
}

// UpdateRecordingStatus transitions a recording to a new status and optionally
// records an error message (used on failure).
func (r *Repository) UpdateRecordingStatus(ctx context.Context, id uuid.UUID, status domain.RecordingStatus, errorMsg *string) (*RecordingRecord, error) {
	const q = `
		UPDATE recordings
		SET status = $2, error_message = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING id, clinic_id, staff_id, subject_id, status, file_key, content_type,
		          duration_seconds, transcript, error_message, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, id, string(status), errorMsg)
	rec, err := scanRecording(row)
	if err != nil {
		return nil, fmt.Errorf("audio.repo.UpdateRecordingStatus: %w", err)
	}
	return rec, nil
}

// UpdateRecordingTranscript stores the Deepgram transcript and duration after
// a successful transcription job. Also transitions status to transcribed.
func (r *Repository) UpdateRecordingTranscript(ctx context.Context, id uuid.UUID, transcript string, durationSeconds *int) (*RecordingRecord, error) {
	const q = `
		UPDATE recordings
		SET status = 'transcribed', transcript = $2, duration_seconds = $3,
		    error_message = NULL, updated_at = NOW()
		WHERE id = $1
		RETURNING id, clinic_id, staff_id, subject_id, status, file_key, content_type,
		          duration_seconds, transcript, error_message, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, id, transcript, durationSeconds)
	rec, err := scanRecording(row)
	if err != nil {
		return nil, fmt.Errorf("audio.repo.UpdateRecordingTranscript: %w", err)
	}
	return rec, nil
}

// LinkSubject associates a recording with a patient subject.
// Only updates if the recording belongs to the provided clinic.
func (r *Repository) LinkSubject(ctx context.Context, id, clinicID, subjectID uuid.UUID) (*RecordingRecord, error) {
	const q = `
		UPDATE recordings
		SET subject_id = $3, updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2
		RETURNING id, clinic_id, staff_id, subject_id, status, file_key, content_type,
		          duration_seconds, transcript, error_message, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, id, clinicID, subjectID)
	rec, err := scanRecording(row)
	if err != nil {
		return nil, fmt.Errorf("audio.repo.LinkSubject: %w", err)
	}
	return rec, nil
}

// GetTranscript returns the transcript for a recording by ID.
// No clinic_id check — for internal pipeline use only (River workers).
// Returns nil transcript when the recording has not been transcribed yet.
func (r *Repository) GetTranscript(ctx context.Context, id uuid.UUID) (*string, error) {
	var transcript *string
	err := r.db.QueryRow(ctx, `SELECT transcript FROM recordings WHERE id = $1`, id).Scan(&transcript)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("audio.repo.GetTranscript: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("audio.repo.GetTranscript: %w", err)
	}
	return transcript, nil
}

// ── Scan helper ───────────────────────────────────────────────────────────────

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanRecording(s scanner) (*RecordingRecord, error) {
	var rec RecordingRecord
	var status string
	err := s.Scan(
		&rec.ID,
		&rec.ClinicID,
		&rec.StaffID,
		&rec.SubjectID,
		&status,
		&rec.FileKey,
		&rec.ContentType,
		&rec.DurationSeconds,
		&rec.Transcript,
		&rec.ErrorMessage,
		&rec.CreatedAt,
		&rec.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("audio.scanRecording: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("audio.scanRecording: %w", err)
	}
	rec.Status = domain.RecordingStatus(status)
	return &rec, nil
}
