// Package aidrafts orchestrates audio → transcribe → AI extraction for
// any target domain that wants prefilled fields (incidents, consent,
// future pain + pre-encounter brief). Drugs are deliberately excluded —
// regulator stakes are too high to surface AI-suggested values.
//
// The package owns one table (ai_drafts) and one River worker. The
// audio TranscribeAudioWorker fans out via the TranscriptListener
// interface; the listener walks pending_transcript rows for the
// finished recording and enqueues the extraction worker.
package aidrafts

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

// DraftRecord is the raw DB row.
type DraftRecord struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	TargetType     string
	RecordingID    *uuid.UUID
	ContextPayload *string // JSONB as text
	DraftPayload   *string // JSONB as text
	Status         string
	ErrorMessage   *string
	AIProvider     *string
	AIModel        *string
	PromptHash     *string
	RequestedBy    uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

type CreateDraftParams struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	TargetType     string
	RecordingID    *uuid.UUID
	ContextPayload *string
	RequestedBy    uuid.UUID
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const draftCols = `id, clinic_id, target_type, recording_id,
	context_payload::text, draft_payload::text, status, error_message,
	ai_provider, ai_model, prompt_hash,
	requested_by, created_at, updated_at, completed_at`

func (r *Repository) CreateDraft(ctx context.Context, p CreateDraftParams) (*DraftRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO ai_drafts (
			id, clinic_id, target_type, recording_id, context_payload, requested_by
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING %s`, draftCols),
		p.ID, p.ClinicID, p.TargetType, p.RecordingID, p.ContextPayload, p.RequestedBy,
	)
	rec, err := scanDraft(row)
	if err != nil {
		return nil, fmt.Errorf("aidrafts.repo.CreateDraft: %w", err)
	}
	return rec, nil
}

// GetDraft — clinic-scoped read.
func (r *Repository) GetDraft(ctx context.Context, id, clinicID uuid.UUID) (*DraftRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM ai_drafts WHERE id = $1 AND clinic_id = $2`, draftCols),
		id, clinicID,
	)
	rec, err := scanDraft(row)
	if err != nil {
		return nil, fmt.Errorf("aidrafts.repo.GetDraft: %w", err)
	}
	return rec, nil
}

// GetDraftInternal — no clinic scope. Used by the worker.
func (r *Repository) GetDraftInternal(ctx context.Context, id uuid.UUID) (*DraftRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM ai_drafts WHERE id = $1`, draftCols),
		id,
	)
	rec, err := scanDraft(row)
	if err != nil {
		return nil, fmt.Errorf("aidrafts.repo.GetDraftInternal: %w", err)
	}
	return rec, nil
}

// ListPendingByRecording — used by the audio-transcript listener to
// find every draft waiting on a freshly-completed recording.
func (r *Repository) ListPendingByRecording(ctx context.Context, recordingID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id FROM ai_drafts
		WHERE recording_id = $1 AND status = 'pending_transcript'`,
		recordingID,
	)
	if err != nil {
		return nil, fmt.Errorf("aidrafts.repo.ListPendingByRecording: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("aidrafts.repo.ListPendingByRecording: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aidrafts.repo.ListPendingByRecording: rows: %w", err)
	}
	return out, nil
}

func (r *Repository) MarkExtracting(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE ai_drafts
		SET status = 'extracting', updated_at = NOW()
		WHERE id = $1 AND status = 'pending_transcript'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("aidrafts.repo.MarkExtracting: %w", err)
	}
	return nil
}

type MarkDoneParams struct {
	ID            uuid.UUID
	DraftPayload  string // JSONB as text
	AIProvider    string
	AIModel       string
	PromptHash    string
}

func (r *Repository) MarkDone(ctx context.Context, p MarkDoneParams) error {
	_, err := r.db.Exec(ctx, `
		UPDATE ai_drafts
		SET status = 'done',
		    draft_payload = $2::jsonb,
		    ai_provider = $3,
		    ai_model = $4,
		    prompt_hash = $5,
		    completed_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1`,
		p.ID, p.DraftPayload, p.AIProvider, p.AIModel, p.PromptHash,
	)
	if err != nil {
		return fmt.Errorf("aidrafts.repo.MarkDone: %w", err)
	}
	return nil
}

func (r *Repository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE ai_drafts
		SET status = 'failed',
		    error_message = $2,
		    completed_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1`,
		id, errMsg,
	)
	if err != nil {
		return fmt.Errorf("aidrafts.repo.MarkFailed: %w", err)
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanDraft(row scannable) (*DraftRecord, error) {
	var d DraftRecord
	err := row.Scan(
		&d.ID, &d.ClinicID, &d.TargetType, &d.RecordingID,
		&d.ContextPayload, &d.DraftPayload, &d.Status, &d.ErrorMessage,
		&d.AIProvider, &d.AIModel, &d.PromptHash,
		&d.RequestedBy, &d.CreatedAt, &d.UpdatedAt, &d.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanDraft: %w", err)
	}
	return &d, nil
}
