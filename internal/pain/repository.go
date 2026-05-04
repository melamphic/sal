// Package pain owns per-subject pain scores. Universal across every
// (vertical, country) combo: vet uses NRS for cats/dogs, dental uses VAS
// for procedure pain, GP uses NRS or Wong-Baker, aged care uses PainAD /
// FLACC for non-verbal residents. The DB column already lists the scales.
package pain

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

// PainScoreRecord is the raw DB row. The pain.PainScoreRecord stutter is
// deliberate: huma's OpenAPI registry needs globally-unique type names
// across all packages, and a generic ScoreRecord would collide with any
// other "score" domain (audit scores, alignment scores, etc.).
//
//nolint:revive
type PainScoreRecord struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	SubjectID     uuid.UUID
	NoteID        *uuid.UUID
	NoteFieldID   *uuid.UUID
	Score         int
	Note          *string
	Method        string
	PainScaleUsed string
	AssessedBy    uuid.UUID
	AssessedAt    time.Time
	// 4-mode witness shape (00079) — most pain scores are routine
	// observations and run with these all nil; PRN-driven assessments
	// that gate controlled-drug administration use the same shape as
	// drugs/consent/incidents.
	WitnessID           *uuid.UUID
	WitnessKind         *string
	ExternalWitnessName *string
	ExternalWitnessRole *string
	WitnessAttestation  *string
	CreatedAt           time.Time
}

type CreatePainScoreParams struct {
	ID                  uuid.UUID
	ClinicID            uuid.UUID
	SubjectID           uuid.UUID
	NoteID              *uuid.UUID
	NoteFieldID         *uuid.UUID
	Score               int
	Note                *string
	Method              string
	PainScaleUsed       string
	AssessedBy          uuid.UUID
	AssessedAt          time.Time
	WitnessID           *uuid.UUID
	WitnessKind         *string
	ExternalWitnessName *string
	ExternalWitnessRole *string
	WitnessAttestation  *string
}

type ListPainScoresParams struct {
	Limit     int
	Offset    int
	SubjectID *uuid.UUID
	Since     *time.Time
	Until     *time.Time
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const painCols = `id, clinic_id, subject_id, note_id, note_field_id,
	score, note,
	method, pain_scale_used, assessed_by, assessed_at,
	witness_id, witness_kind, external_witness_name, external_witness_role, witness_attestation,
	created_at`

func (r *Repository) CreatePainScore(ctx context.Context, p CreatePainScoreParams) (*PainScoreRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO pain_scores (
			id, clinic_id, subject_id, note_id, note_field_id,
			score, note, method, pain_scale_used,
			assessed_by, assessed_at,
			witness_id, witness_kind, external_witness_name, external_witness_role, witness_attestation
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING %s`, painCols),
		p.ID, p.ClinicID, p.SubjectID, p.NoteID, p.NoteFieldID,
		p.Score, p.Note, p.Method, p.PainScaleUsed,
		p.AssessedBy, p.AssessedAt,
		p.WitnessID, p.WitnessKind, p.ExternalWitnessName, p.ExternalWitnessRole, p.WitnessAttestation,
	)
	rec, err := scanPain(row)
	if err != nil {
		return nil, fmt.Errorf("pain.repo.CreatePainScore: %w", err)
	}
	return rec, nil
}

func (r *Repository) GetPainScore(ctx context.Context, id, clinicID uuid.UUID) (*PainScoreRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM pain_scores WHERE id = $1 AND clinic_id = $2`, painCols),
		id, clinicID,
	)
	rec, err := scanPain(row)
	if err != nil {
		return nil, fmt.Errorf("pain.repo.GetPainScore: %w", err)
	}
	return rec, nil
}

func (r *Repository) ListPainScores(ctx context.Context, clinicID uuid.UUID, p ListPainScoresParams) ([]*PainScoreRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"
	if p.SubjectID != nil {
		args = append(args, *p.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}
	if p.Since != nil {
		args = append(args, *p.Since)
		where += fmt.Sprintf(" AND assessed_at >= $%d", len(args))
	}
	if p.Until != nil {
		args = append(args, *p.Until)
		where += fmt.Sprintf(" AND assessed_at <= $%d", len(args))
	}
	// Hide draft-bound pain scores from list views (patient trend,
	// timeline). Per-id GET still works so the note review surface
	// can show its own pending materialisations.
	where += " AND (note_id IS NULL OR note_id IN (SELECT id FROM notes WHERE status = 'submitted'))"

	var total int
	if err := r.db.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM pain_scores WHERE %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("pain.repo.ListPainScores: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	q := fmt.Sprintf(`
		SELECT %s FROM pain_scores WHERE %s
		ORDER BY assessed_at DESC
		LIMIT $%d OFFSET $%d`, painCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("pain.repo.ListPainScores: %w", err)
	}
	defer rows.Close()
	var out []*PainScoreRecord
	for rows.Next() {
		rec, err := scanPain(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("pain.repo.ListPainScores: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("pain.repo.ListPainScores: rows: %w", err)
	}
	return out, total, nil
}

// UpdateReviewStatus stamps the review_status snapshot column on a
// pain_scores row. Idempotent — invoked by the approvals service when
// an async review transitions states.
func (r *Repository) UpdateReviewStatus(ctx context.Context, id, clinicID uuid.UUID, status domain.EntityReviewStatus) error {
	const q = `UPDATE pain_scores
	           SET review_status = $3
	           WHERE id = $1 AND clinic_id = $2`
	if _, err := r.db.Exec(ctx, q, id, clinicID, string(status)); err != nil {
		return fmt.Errorf("pain.repo.UpdateReviewStatus: %w", err)
	}
	return nil
}

// SubjectTrend holds a compact (count, avg, latest) summary for the
// subject hub. Period-bounded so a "last 30 days" view doesn't have to
// pull the entire history.
type SubjectTrend struct {
	SubjectID  uuid.UUID
	Count      int
	AvgScore   float64
	LatestAt   *time.Time
	LatestScore *int
	HighestScore *int
	Since      time.Time
	Until      time.Time
}

func (r *Repository) SubjectTrend(ctx context.Context, clinicID, subjectID uuid.UUID, since, until time.Time) (*SubjectTrend, error) {
	// Same submitted-or-standalone gate as ListPainScores so the
	// patient trend doesn't include scores still pending a note submit.
	const submittedFilter = `AND (note_id IS NULL OR note_id IN (SELECT id FROM notes WHERE status = 'submitted'))`
	row := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COALESCE(AVG(score)::float, 0),
			MAX(assessed_at),
			MAX(score)
		FROM pain_scores
		WHERE clinic_id = $1 AND subject_id = $2
		  AND assessed_at >= $3 AND assessed_at <= $4
		  `+submittedFilter,
		clinicID, subjectID, since, until,
	)
	var (
		count   int
		avg     float64
		latest  *time.Time
		highest *int
	)
	if err := row.Scan(&count, &avg, &latest, &highest); err != nil {
		return nil, fmt.Errorf("pain.repo.SubjectTrend: %w", err)
	}
	out := &SubjectTrend{
		SubjectID:    subjectID,
		Count:        count,
		AvgScore:     avg,
		LatestAt:     latest,
		HighestScore: highest,
		Since:        since,
		Until:        until,
	}
	if count > 0 {
		// Need the score at LatestAt — small follow-up query.
		var latestScore int
		err := r.db.QueryRow(ctx, `
			SELECT score FROM pain_scores
			WHERE clinic_id = $1 AND subject_id = $2 AND assessed_at = $3
			  `+submittedFilter+`
			ORDER BY created_at DESC LIMIT 1`,
			clinicID, subjectID, latest,
		).Scan(&latestScore)
		if err == nil {
			out.LatestScore = &latestScore
		}
	}
	return out, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanPain(row scannable) (*PainScoreRecord, error) {
	var p PainScoreRecord
	err := row.Scan(
		&p.ID, &p.ClinicID, &p.SubjectID, &p.NoteID, &p.NoteFieldID,
		&p.Score, &p.Note, &p.Method, &p.PainScaleUsed,
		&p.AssessedBy, &p.AssessedAt,
		&p.WitnessID, &p.WitnessKind, &p.ExternalWitnessName, &p.ExternalWitnessRole, &p.WitnessAttestation,
		&p.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanPain: %w", err)
	}
	return &p, nil
}
