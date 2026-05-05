// Package incidents owns the incident-events workflow: logging, witnesses,
// addendums, escalation, regulator notification + audit trail. The package
// is thin glue around the incident_events / incident_witnesses /
// incident_addendums tables introduced by migration 00055.
package incidents

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

// ── Records ──────────────────────────────────────────────────────────────────

// IncidentRecord is the raw DB row.
type IncidentRecord struct {
	ID                       uuid.UUID
	ClinicID                 uuid.UUID
	SubjectID                uuid.UUID
	NoteID                   *uuid.UUID
	NoteFieldID              *uuid.UUID
	IncidentType             string
	SIRSPriority             *string
	CQCNotifiable            bool
	CQCNotificationType      *string
	Severity                 string
	OccurredAt               time.Time
	Location                 *string
	BriefDescription         string
	ImmediateActions         *string
	WitnessesText            *string
	SubjectOutcome           *string
	NOKNotifiedAt            *time.Time
	GPNotifiedAt             *time.Time
	RegulatorNotifiedAt      *time.Time
	RegulatorReferenceNumber *string
	NotificationDeadline     *time.Time
	EscalationReason         *string
	EscalatedAt              *time.Time
	EscalatedBy              *uuid.UUID
	ReportedBy               uuid.UUID
	ReviewedBy               *uuid.UUID
	ReviewedAt               *time.Time
	PreventivePlanSummary    *string
	CarePlanUpdatedAt        *time.Time
	Status                   string
	// 4-mode witness shape (00078) — matches drug_operations_log +
	// consent_records. WitnessID + WitnessKind=staff for sync; nil
	// for legacy rows.
	WitnessID           *uuid.UUID
	WitnessKind         *string
	ExternalWitnessName *string
	ExternalWitnessRole *string
	WitnessAttestation  *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// IncidentAddendumRecord is one row of incident_addendums.
type IncidentAddendumRecord struct {
	ID           uuid.UUID
	IncidentID   uuid.UUID
	AddendumText string
	AddedBy      uuid.UUID
	AddedAt      time.Time
}

// ── Param types ──────────────────────────────────────────────────────────────

type CreateIncidentParams struct {
	ID                   uuid.UUID
	ClinicID             uuid.UUID
	SubjectID            uuid.UUID
	NoteID               *uuid.UUID
	NoteFieldID          *uuid.UUID
	IncidentType         string
	Severity             string
	OccurredAt           time.Time
	Location             *string
	BriefDescription     string
	ImmediateActions     *string
	WitnessesText        *string
	SubjectOutcome       *string
	ReportedBy           uuid.UUID
	SIRSPriority         *string
	CQCNotifiable        bool
	CQCNotificationType  *string
	NotificationDeadline *time.Time
	WitnessID            *uuid.UUID
	WitnessKind          *string
	ExternalWitnessName  *string
	ExternalWitnessRole  *string
	WitnessAttestation   *string
}

type UpdateIncidentParams struct {
	ID                       uuid.UUID
	ClinicID                 uuid.UUID
	Severity                 *string
	Location                 *string
	BriefDescription         *string
	ImmediateActions         *string
	WitnessesText            *string
	SubjectOutcome           *string
	NOKNotifiedAt            *time.Time
	GPNotifiedAt             *time.Time
	PreventivePlanSummary    *string
	CarePlanUpdatedAt        *time.Time
	ReviewedBy               *uuid.UUID
	ReviewedAt               *time.Time
	Status                   *string
}

type ListIncidentsParams struct {
	Limit     int
	Offset    int
	SubjectID *uuid.UUID
	Status    *string
	Type      *string
	Since     *time.Time
	Until     *time.Time
	OnlyOpen  bool
}

type EscalateParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	StaffID    uuid.UUID
	Reason     string
	OccurredAt time.Time
}

type NotifyRegulatorParams struct {
	ID              uuid.UUID
	ClinicID        uuid.UUID
	StaffID         uuid.UUID
	ReferenceNumber *string
	NotifiedAt      time.Time
}

type CreateAddendumParams struct {
	ID         uuid.UUID
	IncidentID uuid.UUID
	ClinicID   uuid.UUID
	StaffID    uuid.UUID
	Text       string
}

// ── Repository ──────────────────────────────────────────────────────────────

const incidentCols = `id, clinic_id, subject_id, note_id, note_field_id,
	incident_type,
	sirs_priority, cqc_notifiable, cqc_notification_type, severity,
	occurred_at, location, brief_description, immediate_actions, witnesses_text,
	subject_outcome,
	nok_notified_at, gp_notified_at, regulator_notified_at, regulator_reference_number,
	notification_deadline, escalation_reason, escalated_at, escalated_by,
	reported_by, reviewed_by, reviewed_at,
	preventive_plan_summary, care_plan_updated_at, status,
	witness_id, witness_kind, external_witness_name, external_witness_role, witness_attestation,
	created_at, updated_at`

// Repository wraps the incidents tables behind hand-written pgx queries.
// Every read takes clinic_id; multi-tenant safety is non-negotiable.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateIncident inserts a new incident row + the SIRS / CQC stamps the
// service has already computed. We do not run the classifier in the repo
// — keeping the regulator-decision logic out of SQL.
func (r *Repository) CreateIncident(ctx context.Context, p CreateIncidentParams) (*IncidentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO incident_events (
			id, clinic_id, subject_id, note_id, note_field_id,
			incident_type, severity, occurred_at, location,
			brief_description, immediate_actions, witnesses_text, subject_outcome,
			reported_by,
			sirs_priority, cqc_notifiable, cqc_notification_type, notification_deadline,
			witness_id, witness_kind, external_witness_name, external_witness_role, witness_attestation
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
		        $19, $20, $21, $22, $23)
		RETURNING %s`, incidentCols),
		p.ID, p.ClinicID, p.SubjectID, p.NoteID, p.NoteFieldID,
		p.IncidentType, p.Severity, p.OccurredAt, p.Location,
		p.BriefDescription, p.ImmediateActions, p.WitnessesText, p.SubjectOutcome,
		p.ReportedBy,
		p.SIRSPriority, p.CQCNotifiable, p.CQCNotificationType, p.NotificationDeadline,
		p.WitnessID, p.WitnessKind, p.ExternalWitnessName, p.ExternalWitnessRole, p.WitnessAttestation,
	)
	rec, err := scanIncident(row)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.CreateIncident: %w", err)
	}
	return rec, nil
}

// GetIncident — single row, clinic-scoped.
func (r *Repository) GetIncident(ctx context.Context, id, clinicID uuid.UUID) (*IncidentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM incident_events WHERE id = $1 AND clinic_id = $2`, incidentCols),
		id, clinicID,
	)
	rec, err := scanIncident(row)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.GetIncident: %w", err)
	}
	return rec, nil
}

// ListIncidents — paginated, filterable.
func (r *Repository) ListIncidents(ctx context.Context, clinicID uuid.UUID, p ListIncidentsParams) ([]*IncidentRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"
	if p.SubjectID != nil {
		args = append(args, *p.SubjectID)
		where += fmt.Sprintf(" AND subject_id = $%d", len(args))
	}
	if p.Status != nil {
		args = append(args, *p.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if p.Type != nil {
		args = append(args, *p.Type)
		where += fmt.Sprintf(" AND incident_type = $%d", len(args))
	}
	if p.Since != nil {
		args = append(args, *p.Since)
		where += fmt.Sprintf(" AND occurred_at >= $%d", len(args))
	}
	if p.Until != nil {
		args = append(args, *p.Until)
		where += fmt.Sprintf(" AND occurred_at <= $%d", len(args))
	}
	if p.OnlyOpen {
		where += " AND status NOT IN ('closed','reported_to_regulator')"
	}
	// Hide draft-bound incidents from list views — patient timeline +
	// compliance inbox should only show finalized incidents. Per-id
	// GET stays unconditional for note review.
	where += " AND (note_id IS NULL OR note_id IN (SELECT id FROM notes WHERE status = 'submitted'))"

	var total int
	if err := r.db.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM incident_events WHERE %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("incidents.repo.ListIncidents: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	q := fmt.Sprintf(`
		SELECT %s FROM incident_events WHERE %s
		ORDER BY occurred_at DESC
		LIMIT $%d OFFSET $%d`, incidentCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("incidents.repo.ListIncidents: %w", err)
	}
	defer rows.Close()

	var list []*IncidentRecord
	for rows.Next() {
		rec, err := scanIncident(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("incidents.repo.ListIncidents: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("incidents.repo.ListIncidents: rows: %w", err)
	}
	return list, total, nil
}

// UpdateIncident applies a partial update. Only the fields whose pointer
// is non-nil are written — letting the service do PATCH-style updates
// without forcing every field through.
func (r *Repository) UpdateIncident(ctx context.Context, p UpdateIncidentParams) (*IncidentRecord, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{p.ID, p.ClinicID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.Severity != nil {
		add("severity", *p.Severity)
	}
	if p.Location != nil {
		add("location", *p.Location)
	}
	if p.BriefDescription != nil {
		add("brief_description", *p.BriefDescription)
	}
	if p.ImmediateActions != nil {
		add("immediate_actions", *p.ImmediateActions)
	}
	if p.WitnessesText != nil {
		add("witnesses_text", *p.WitnessesText)
	}
	if p.SubjectOutcome != nil {
		add("subject_outcome", *p.SubjectOutcome)
	}
	if p.NOKNotifiedAt != nil {
		add("nok_notified_at", *p.NOKNotifiedAt)
	}
	if p.GPNotifiedAt != nil {
		add("gp_notified_at", *p.GPNotifiedAt)
	}
	if p.PreventivePlanSummary != nil {
		add("preventive_plan_summary", *p.PreventivePlanSummary)
	}
	if p.CarePlanUpdatedAt != nil {
		add("care_plan_updated_at", *p.CarePlanUpdatedAt)
	}
	if p.ReviewedBy != nil {
		add("reviewed_by", *p.ReviewedBy)
	}
	if p.ReviewedAt != nil {
		add("reviewed_at", *p.ReviewedAt)
	}
	if p.Status != nil {
		add("status", *p.Status)
	}

	q := fmt.Sprintf(`
		UPDATE incident_events SET %s
		WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, joinSets(sets), incidentCols)
	row := r.db.QueryRow(ctx, q, args...)
	rec, err := scanIncident(row)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.UpdateIncident: %w", err)
	}
	return rec, nil
}

// EscalateIncident flips status + stamps escalation reason / by.
// Idempotent at the SQL level — repeat escalations overwrite.
func (r *Repository) EscalateIncident(ctx context.Context, p EscalateParams) (*IncidentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE incident_events
		SET status = 'escalated',
		    escalation_reason = $3,
		    escalated_at = $4,
		    escalated_by = $5,
		    updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, incidentCols),
		p.ID, p.ClinicID, p.Reason, p.OccurredAt, p.StaffID,
	)
	rec, err := scanIncident(row)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.EscalateIncident: %w", err)
	}
	return rec, nil
}

// MarkRegulatorNotified — the privacy officer flow once the regulator
// has been notified externally. Reference number captures the SIRS /
// CQC ticket id; audit reports cite this.
func (r *Repository) MarkRegulatorNotified(ctx context.Context, p NotifyRegulatorParams) (*IncidentRecord, error) {
	row := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE incident_events
		SET status = 'reported_to_regulator',
		    regulator_notified_at = $3,
		    regulator_reference_number = COALESCE($4, regulator_reference_number),
		    updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, incidentCols),
		p.ID, p.ClinicID, p.NotifiedAt, p.ReferenceNumber,
	)
	rec, err := scanIncident(row)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.MarkRegulatorNotified: %w", err)
	}
	return rec, nil
}

// UpdateReviewStatus stamps the review_status snapshot column on an
// incident row. Idempotent — invoked by the approvals service when an
// async review transitions states.
func (r *Repository) UpdateReviewStatus(ctx context.Context, id, clinicID uuid.UUID, status domain.EntityReviewStatus) error {
	const q = `UPDATE incident_events
	           SET review_status = $3,
	               updated_at = NOW()
	           WHERE id = $1 AND clinic_id = $2`
	if _, err := r.db.Exec(ctx, q, id, clinicID, string(status)); err != nil {
		return fmt.Errorf("incidents.repo.UpdateReviewStatus: %w", err)
	}
	return nil
}

// ── Witnesses ────────────────────────────────────────────────────────────────

func (r *Repository) AddWitness(ctx context.Context, incidentID, staffID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO incident_witnesses (incident_id, staff_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`,
		incidentID, staffID,
	)
	if err != nil {
		return fmt.Errorf("incidents.repo.AddWitness: %w", err)
	}
	return nil
}

func (r *Repository) RemoveWitness(ctx context.Context, incidentID, staffID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM incident_witnesses
		WHERE incident_id = $1 AND staff_id = $2`,
		incidentID, staffID,
	)
	if err != nil {
		return fmt.Errorf("incidents.repo.RemoveWitness: %w", err)
	}
	return nil
}

// ListWitnesses returns the staff ids associated with an incident.
// Resolving names is a service-layer concern via the staff service.
func (r *Repository) ListWitnesses(ctx context.Context, incidentID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx, `
		SELECT staff_id FROM incident_witnesses
		WHERE incident_id = $1
		ORDER BY staff_id`,
		incidentID,
	)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.ListWitnesses: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("incidents.repo.ListWitnesses: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("incidents.repo.ListWitnesses: rows: %w", err)
	}
	return out, nil
}

// ── Addendums ────────────────────────────────────────────────────────────────

func (r *Repository) CreateAddendum(ctx context.Context, p CreateAddendumParams) (*IncidentAddendumRecord, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO incident_addendums (id, incident_id, addendum_text, added_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, incident_id, addendum_text, added_by, added_at`,
		p.ID, p.IncidentID, p.Text, p.StaffID,
	)
	var a IncidentAddendumRecord
	if err := row.Scan(&a.ID, &a.IncidentID, &a.AddendumText, &a.AddedBy, &a.AddedAt); err != nil {
		return nil, fmt.Errorf("incidents.repo.CreateAddendum: %w", err)
	}
	return &a, nil
}

func (r *Repository) ListAddendums(ctx context.Context, incidentID uuid.UUID) ([]*IncidentAddendumRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, incident_id, addendum_text, added_by, added_at
		FROM incident_addendums
		WHERE incident_id = $1
		ORDER BY added_at`,
		incidentID,
	)
	if err != nil {
		return nil, fmt.Errorf("incidents.repo.ListAddendums: %w", err)
	}
	defer rows.Close()
	var out []*IncidentAddendumRecord
	for rows.Next() {
		var a IncidentAddendumRecord
		if err := rows.Scan(&a.ID, &a.IncidentID, &a.AddendumText, &a.AddedBy, &a.AddedAt); err != nil {
			return nil, fmt.Errorf("incidents.repo.ListAddendums: scan: %w", err)
		}
		out = append(out, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("incidents.repo.ListAddendums: rows: %w", err)
	}
	return out, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanIncident(row scannable) (*IncidentRecord, error) {
	var i IncidentRecord
	err := row.Scan(
		&i.ID, &i.ClinicID, &i.SubjectID, &i.NoteID, &i.NoteFieldID,
		&i.IncidentType,
		&i.SIRSPriority, &i.CQCNotifiable, &i.CQCNotificationType, &i.Severity,
		&i.OccurredAt, &i.Location, &i.BriefDescription, &i.ImmediateActions, &i.WitnessesText,
		&i.SubjectOutcome,
		&i.NOKNotifiedAt, &i.GPNotifiedAt, &i.RegulatorNotifiedAt, &i.RegulatorReferenceNumber,
		&i.NotificationDeadline, &i.EscalationReason, &i.EscalatedAt, &i.EscalatedBy,
		&i.ReportedBy, &i.ReviewedBy, &i.ReviewedAt,
		&i.PreventivePlanSummary, &i.CarePlanUpdatedAt, &i.Status,
		&i.WitnessID, &i.WitnessKind, &i.ExternalWitnessName, &i.ExternalWitnessRole, &i.WitnessAttestation,
		&i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanIncident: %w", err)
	}
	return &i, nil
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
