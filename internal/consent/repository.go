// Package consent owns per-subject consent records — verbal, written,
// electronic, or guardian-captured. Vertical-agnostic: every clinic
// captures consent for procedures, sedation, photography, telemedicine,
// data sharing, and (in aged care) substitute decision-making by EPOA.
package consent

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

// ConsentRecord is the raw DB row for consent_records.
//
//nolint:revive
type ConsentRecord struct {
	ID                          uuid.UUID
	ClinicID                    uuid.UUID
	SubjectID                   uuid.UUID
	NoteID                      *uuid.UUID
	ConsentType                 string
	Scope                       string
	ProcedureOrFormID           *uuid.UUID
	RisksDiscussed              *string
	AlternativesDiscussed       *string
	CapturedVia                 string
	SignatureImageKey           *string
	TranscriptRecordingID       *uuid.UUID
	ConsentingPartyRelationship *string
	ConsentingPartyName         *string
	CapacityAssessmentID        *uuid.UUID
	CapturedBy                  uuid.UUID
	CapturedAt                  time.Time
	WitnessID                   *uuid.UUID
	ExpiresAt                   *time.Time
	RenewalDueAt                *time.Time
	WithdrawalAt                *time.Time
	WithdrawalReason            *string
	AIAssistanceMetadata        *string // JSONB as text
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

// CreateConsentParams holds values for capturing a new consent record.
type CreateConsentParams struct {
	ID                          uuid.UUID
	ClinicID                    uuid.UUID
	SubjectID                   uuid.UUID
	NoteID                      *uuid.UUID
	ConsentType                 string
	Scope                       string
	ProcedureOrFormID           *uuid.UUID
	RisksDiscussed              *string
	AlternativesDiscussed       *string
	CapturedVia                 string
	SignatureImageKey           *string
	TranscriptRecordingID       *uuid.UUID
	ConsentingPartyRelationship *string
	ConsentingPartyName         *string
	CapacityAssessmentID        *uuid.UUID
	CapturedBy                  uuid.UUID
	CapturedAt                  time.Time
	WitnessID                   *uuid.UUID
	ExpiresAt                   *time.Time
	RenewalDueAt                *time.Time
	AIAssistanceMetadata        *string
}

// UpdateConsentParams — limited PATCH (capture is append-only in spirit;
// these are the corrections / late-captured fields the regulator allows).
type UpdateConsentParams struct {
	ID                    uuid.UUID
	ClinicID              uuid.UUID
	RisksDiscussed        *string
	AlternativesDiscussed *string
	ExpiresAt             *time.Time
	RenewalDueAt          *time.Time
	SignatureImageKey     *string
	WitnessID             *uuid.UUID
}

// WithdrawConsentParams marks a consent withdrawn with a reason. Append-
// only against the row — the original capture stays for audit.
type WithdrawConsentParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	Reason     string
	WithdrawnAt time.Time
}

// ListConsentParams holds filters + pagination.
type ListConsentParams struct {
	Limit          int
	Offset         int
	SubjectID      *uuid.UUID
	ConsentType    *string
	OnlyActive     bool // hide withdrawn + expired
	ExpiringWithin *time.Duration
}

// Repository wraps the consent_records table.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const consentCols = `id, clinic_id, subject_id, note_id, consent_type, scope,
	procedure_or_form_id, risks_discussed, alternatives_discussed,
	captured_via, signature_image_key, transcript_recording_id,
	consenting_party_relationship, consenting_party_name,
	capacity_assessment_id, captured_by, captured_at, witness_id,
	expires_at, renewal_due_at, withdrawal_at, withdrawal_reason,
	ai_assistance_metadata::text, created_at, updated_at`

func (r *Repository) CreateConsent(ctx context.Context, p CreateConsentParams) (*ConsentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO consent_records (
			id, clinic_id, subject_id, note_id,
			consent_type, scope, procedure_or_form_id,
			risks_discussed, alternatives_discussed,
			captured_via, signature_image_key, transcript_recording_id,
			consenting_party_relationship, consenting_party_name,
			capacity_assessment_id, captured_by, captured_at, witness_id,
			expires_at, renewal_due_at, ai_assistance_metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		        $13, $14, $15, $16, $17, $18, $19, $20, $21::jsonb)
		RETURNING %s`, consentCols),
		p.ID, p.ClinicID, p.SubjectID, p.NoteID,
		p.ConsentType, p.Scope, p.ProcedureOrFormID,
		p.RisksDiscussed, p.AlternativesDiscussed,
		p.CapturedVia, p.SignatureImageKey, p.TranscriptRecordingID,
		p.ConsentingPartyRelationship, p.ConsentingPartyName,
		p.CapacityAssessmentID, p.CapturedBy, p.CapturedAt, p.WitnessID,
		p.ExpiresAt, p.RenewalDueAt, p.AIAssistanceMetadata,
	)
	rec, err := scanConsent(row)
	if err != nil {
		return nil, fmt.Errorf("consent.repo.CreateConsent: %w", err)
	}
	return rec, nil
}

func (r *Repository) GetConsent(ctx context.Context, id, clinicID uuid.UUID) (*ConsentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM consent_records WHERE id = $1 AND clinic_id = $2`, consentCols),
		id, clinicID,
	)
	rec, err := scanConsent(row)
	if err != nil {
		return nil, fmt.Errorf("consent.repo.GetConsent: %w", err)
	}
	return rec, nil
}

func (r *Repository) ListConsents(ctx context.Context, clinicID uuid.UUID, p ListConsentParams) ([]*ConsentRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"
	if p.SubjectID != nil {
		args = append(args, *p.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}
	if p.ConsentType != nil {
		args = append(args, *p.ConsentType)
		where += fmt.Sprintf(" AND consent_type = $%d", len(args))
	}
	if p.OnlyActive {
		where += " AND withdrawal_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())"
	}
	if p.ExpiringWithin != nil {
		args = append(args, *p.ExpiringWithin)
		where += fmt.Sprintf(" AND withdrawal_at IS NULL AND expires_at IS NOT NULL AND expires_at <= NOW() + $%d::interval", len(args))
	}

	var total int
	if err := r.db.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM consent_records WHERE %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("consent.repo.ListConsents: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	q := fmt.Sprintf(`
		SELECT %s FROM consent_records WHERE %s
		ORDER BY captured_at DESC
		LIMIT $%d OFFSET $%d`, consentCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("consent.repo.ListConsents: %w", err)
	}
	defer rows.Close()

	var list []*ConsentRecord
	for rows.Next() {
		rec, err := scanConsent(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("consent.repo.ListConsents: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("consent.repo.ListConsents: rows: %w", err)
	}
	return list, total, nil
}

func (r *Repository) UpdateConsent(ctx context.Context, p UpdateConsentParams) (*ConsentRecord, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{p.ID, p.ClinicID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.RisksDiscussed != nil {
		add("risks_discussed", *p.RisksDiscussed)
	}
	if p.AlternativesDiscussed != nil {
		add("alternatives_discussed", *p.AlternativesDiscussed)
	}
	if p.ExpiresAt != nil {
		add("expires_at", *p.ExpiresAt)
	}
	if p.RenewalDueAt != nil {
		add("renewal_due_at", *p.RenewalDueAt)
	}
	if p.SignatureImageKey != nil {
		add("signature_image_key", *p.SignatureImageKey)
	}
	if p.WitnessID != nil {
		add("witness_id", *p.WitnessID)
	}

	q := fmt.Sprintf(`
		UPDATE consent_records SET %s
		WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, joinSets(sets), consentCols)
	row := r.db.QueryRow(ctx, q, args...)
	rec, err := scanConsent(row)
	if err != nil {
		return nil, fmt.Errorf("consent.repo.UpdateConsent: %w", err)
	}
	return rec, nil
}

func (r *Repository) WithdrawConsent(ctx context.Context, p WithdrawConsentParams) (*ConsentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE consent_records
		SET withdrawal_at = $3,
		    withdrawal_reason = $4,
		    updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND withdrawal_at IS NULL
		RETURNING %s`, consentCols),
		p.ID, p.ClinicID, p.WithdrawnAt, p.Reason,
	)
	rec, err := scanConsent(row)
	if err != nil {
		return nil, fmt.Errorf("consent.repo.WithdrawConsent: %w", err)
	}
	return rec, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanConsent(row scannable) (*ConsentRecord, error) {
	var c ConsentRecord
	err := row.Scan(
		&c.ID, &c.ClinicID, &c.SubjectID, &c.NoteID, &c.ConsentType, &c.Scope,
		&c.ProcedureOrFormID, &c.RisksDiscussed, &c.AlternativesDiscussed,
		&c.CapturedVia, &c.SignatureImageKey, &c.TranscriptRecordingID,
		&c.ConsentingPartyRelationship, &c.ConsentingPartyName,
		&c.CapacityAssessmentID, &c.CapturedBy, &c.CapturedAt, &c.WitnessID,
		&c.ExpiresAt, &c.RenewalDueAt, &c.WithdrawalAt, &c.WithdrawalReason,
		&c.AIAssistanceMetadata, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanConsent: %w", err)
	}
	return &c, nil
}

func joinSets(sets []string) string {
	out := ""
	for i, s := range sets {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
