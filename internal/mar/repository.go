package mar

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

// ── Record types ────────────────────────────────────────────────────────

// PrescriptionRecord mirrors mar_prescriptions.
type PrescriptionRecord struct {
	ID                        uuid.UUID
	ClinicID                  uuid.UUID
	ResidentID                uuid.UUID
	CatalogEntryID            *string
	OverrideID                *uuid.UUID
	DrugName                  string
	Formulation               string
	Strength                  string
	Dose                      string
	Route                     string
	Frequency                 string
	ScheduleTimes             []string
	IsPRN                     bool
	PRNIndication             *string
	PRNMax24h                 *float64
	Indication                *string
	PrescriberID              *uuid.UUID
	PrescriberExternalName    *string
	PrescriberExternalAddress *string
	StartAt                   time.Time
	StopAt                    *time.Time
	ReviewAt                  *time.Time
	Instructions              *string
	AllergiesChecked          bool
	IsControlled              bool
	ScheduleClass             *string
	ArchivedAt                *time.Time
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// ScheduledDoseRecord mirrors mar_scheduled_doses.
type ScheduledDoseRecord struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	PrescriptionID uuid.UUID
	ScheduledAt    time.Time
	DoseQty        float64
	Route          string
	GeneratedAt    time.Time
}

// RoundRecord mirrors mar_rounds.
type RoundRecord struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	StartedBy   uuid.UUID
	StartedAt   time.Time
	CompletedAt *time.Time
	ShiftLabel  *string
	Location    *string
	Notes       *string
}

// AdminEventRecord mirrors mar_administration_events.
type AdminEventRecord struct {
	ID                          uuid.UUID
	ClinicID                    uuid.UUID
	ResidentID                  uuid.UUID
	PrescriptionID              uuid.UUID
	ScheduledDoseID             *uuid.UUID
	RoundID                     *uuid.UUID
	ActualAt                    time.Time
	ActualDoseQty               *float64
	Route                       *string
	OutcomeCode                 string
	OutcomeReason               *string
	AdministeredBy              uuid.UUID
	WitnessID                   *uuid.UUID
	Notes                       *string
	PRNIndicationTrigger        *string
	PRNEffectiveness            *string
	PRNEffectivenessReviewedAt  *time.Time
	DrugOpID                    *uuid.UUID
	CorrectsID                  *uuid.UUID
	ChainKey                    []byte
	EntrySeqInChain             *int64
	PrevRowHash                 []byte
	RowHash                     []byte
	CreatedAt                   time.Time
}

// ── Insert/update params ─────────────────────────────────────────────────

// CreatePrescriptionParams is the insert shape for mar_prescriptions.
type CreatePrescriptionParams struct {
	ID                        uuid.UUID
	ClinicID                  uuid.UUID
	ResidentID                uuid.UUID
	CatalogEntryID            *string
	OverrideID                *uuid.UUID
	DrugName                  string
	Formulation               string
	Strength                  string
	Dose                      string
	Route                     string
	Frequency                 string
	ScheduleTimes             []string
	IsPRN                     bool
	PRNIndication             *string
	PRNMax24h                 *float64
	Indication                *string
	PrescriberID              *uuid.UUID
	PrescriberExternalName    *string
	PrescriberExternalAddress *string
	StartAt                   time.Time
	StopAt                    *time.Time
	ReviewAt                  *time.Time
	Instructions              *string
	AllergiesChecked          bool
	IsControlled              bool
	ScheduleClass             *string
}

// UpdatePrescriptionParams is the partial-update shape.
type UpdatePrescriptionParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	Dose          *string
	Frequency     *string
	ScheduleTimes []string
	StopAt        *time.Time
	ReviewAt      *time.Time
	Instructions  *string
}

// CreateScheduledDoseParams — generated nightly from a prescription's
// schedule_times. Idempotent (UNIQUE on prescription_id+scheduled_at).
type CreateScheduledDoseParams struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	PrescriptionID uuid.UUID
	ScheduledAt    time.Time
	DoseQty        float64
	Route          string
}

// CreateRoundParams — start a new med-round session.
type CreateRoundParams struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	StartedBy  uuid.UUID
	StartedAt  time.Time
	ShiftLabel *string
	Location   *string
}

// CreateAdminEventParams is the workhorse insert. Service computes
// chain fields + (for CDs) calls drug_operations_log inside the same
// pgx.Tx via the DrugLedgerWriter port.
type CreateAdminEventParams struct {
	ID                         uuid.UUID
	ClinicID                   uuid.UUID
	ResidentID                 uuid.UUID
	PrescriptionID             uuid.UUID
	ScheduledDoseID            *uuid.UUID
	RoundID                    *uuid.UUID
	ActualAt                   time.Time
	ActualDoseQty              *float64
	Route                      *string
	OutcomeCode                string
	OutcomeReason              *string
	AdministeredBy             uuid.UUID
	WitnessID                  *uuid.UUID
	Notes                      *string
	PRNIndicationTrigger       *string
	PRNEffectiveness           *string
	PRNEffectivenessReviewedAt *time.Time
	DrugOpID                   *uuid.UUID
	CorrectsID                 *uuid.UUID
	ChainKey                   []byte
	EntrySeqInChain            *int64
	PrevRowHash                []byte
	RowHash                    []byte
}

// ── Repository ──────────────────────────────────────────────────────────

// Repository is the pgx-backed data layer for the MAR module.
//
// Phase 3a ships table-row shapes + the simplest read paths so service
// scaffolding (Phase 3b) can build on top. Mutating methods return
// stubbed errors until 3b — they're declared on the interface so
// service code compiles.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a MAR repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const prescriptionCols = `
	id, clinic_id, resident_id, catalog_entry_id, override_id,
	drug_name, formulation, strength, dose, route, frequency, schedule_times,
	is_prn, prn_indication, prn_max_24h, indication,
	prescriber_id, prescriber_external_name, prescriber_external_address,
	start_at, stop_at, review_at, instructions, allergies_checked,
	is_controlled, schedule_class,
	archived_at, created_at, updated_at`

func scanPrescription(row pgx.Row) (*PrescriptionRecord, error) {
	var r PrescriptionRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.ResidentID, &r.CatalogEntryID, &r.OverrideID,
		&r.DrugName, &r.Formulation, &r.Strength, &r.Dose, &r.Route, &r.Frequency, &r.ScheduleTimes,
		&r.IsPRN, &r.PRNIndication, &r.PRNMax24h, &r.Indication,
		&r.PrescriberID, &r.PrescriberExternalName, &r.PrescriberExternalAddress,
		&r.StartAt, &r.StopAt, &r.ReviewAt, &r.Instructions, &r.AllergiesChecked,
		&r.IsControlled, &r.ScheduleClass,
		&r.ArchivedAt, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("mar.repo.scanPrescription: %w", err)
	}
	return &r, nil
}

// CreatePrescription inserts a row in mar_prescriptions.
func (r *Repository) CreatePrescription(ctx context.Context, p CreatePrescriptionParams) (*PrescriptionRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO mar_prescriptions (
			id, clinic_id, resident_id, catalog_entry_id, override_id,
			drug_name, formulation, strength, dose, route, frequency, schedule_times,
			is_prn, prn_indication, prn_max_24h, indication,
			prescriber_id, prescriber_external_name, prescriber_external_address,
			start_at, stop_at, review_at, instructions, allergies_checked,
			is_controlled, schedule_class
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19,
			$20, $21, $22, $23, $24,
			$25, $26
		)
		RETURNING %s`, prescriptionCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.ResidentID, p.CatalogEntryID, p.OverrideID,
		p.DrugName, p.Formulation, p.Strength, p.Dose, p.Route, p.Frequency, p.ScheduleTimes,
		p.IsPRN, p.PRNIndication, p.PRNMax24h, p.Indication,
		p.PrescriberID, p.PrescriberExternalName, p.PrescriberExternalAddress,
		p.StartAt, p.StopAt, p.ReviewAt, p.Instructions, p.AllergiesChecked,
		p.IsControlled, p.ScheduleClass,
	)
	rec, err := scanPrescription(row)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.CreatePrescription: %w", err)
	}
	return rec, nil
}

// GetPrescription returns one prescription by id (clinic-scoped).
func (r *Repository) GetPrescription(ctx context.Context, id, clinicID uuid.UUID) (*PrescriptionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM mar_prescriptions WHERE id = $1 AND clinic_id = $2`, prescriptionCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanPrescription(row)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.GetPrescription: %w", err)
	}
	return rec, nil
}

// ListPrescriptionsForResident returns active prescriptions for a
// resident; archived rows included only when includeArchived is true.
func (r *Repository) ListPrescriptionsForResident(ctx context.Context, clinicID, residentID uuid.UUID, includeArchived bool) ([]*PrescriptionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM mar_prescriptions
		WHERE clinic_id = $1 AND resident_id = $2 %s
		ORDER BY start_at DESC`,
		prescriptionCols,
		map[bool]string{true: "", false: "AND archived_at IS NULL"}[includeArchived])
	rows, err := r.db.Query(ctx, q, clinicID, residentID)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.ListPrescriptionsForResident: %w", err)
	}
	defer rows.Close()
	var out []*PrescriptionRecord
	for rows.Next() {
		rec, err := scanPrescription(rows)
		if err != nil {
			return nil, fmt.Errorf("mar.repo.ListPrescriptionsForResident: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mar.repo.ListPrescriptionsForResident: rows: %w", err)
	}
	return out, nil
}

// UpdatePrescription performs a partial update. Phase 3b builds out
// the dynamic SQL; for now this rejects the call so service compiles.
func (r *Repository) UpdatePrescription(_ context.Context, _ UpdatePrescriptionParams) (*PrescriptionRecord, error) {
	return nil, fmt.Errorf("mar.repo.UpdatePrescription: not implemented in Phase 3a (TODO 3b): %w", domain.ErrConflict)
}

// ArchivePrescription soft-deletes a prescription.
func (r *Repository) ArchivePrescription(ctx context.Context, id, clinicID uuid.UUID) error {
	const q = `UPDATE mar_prescriptions
		SET archived_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL`
	if _, err := r.db.Exec(ctx, q, id, clinicID); err != nil {
		return fmt.Errorf("mar.repo.ArchivePrescription: %w", err)
	}
	return nil
}

// CreateScheduledDose inserts a row in mar_scheduled_doses; idempotent
// via the (prescription_id, scheduled_at) UNIQUE index.
func (r *Repository) CreateScheduledDose(ctx context.Context, p CreateScheduledDoseParams) (*ScheduledDoseRecord, error) {
	const q = `
		INSERT INTO mar_scheduled_doses (
			id, clinic_id, prescription_id, scheduled_at, dose_qty, route
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (prescription_id, scheduled_at) DO NOTHING
		RETURNING id, clinic_id, prescription_id, scheduled_at, dose_qty, route, generated_at`
	var rec ScheduledDoseRecord
	err := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.PrescriptionID, p.ScheduledAt, p.DoseQty, p.Route,
	).Scan(&rec.ID, &rec.ClinicID, &rec.PrescriptionID, &rec.ScheduledAt, &rec.DoseQty, &rec.Route, &rec.GeneratedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING returns no row — idempotent skip.
			return nil, nil
		}
		return nil, fmt.Errorf("mar.repo.CreateScheduledDose: %w", err)
	}
	return &rec, nil
}

// ListDueScheduledDoses returns rows scheduled in [from, to).
func (r *Repository) ListDueScheduledDoses(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]*ScheduledDoseRecord, error) {
	const q = `
		SELECT id, clinic_id, prescription_id, scheduled_at, dose_qty, route, generated_at
		  FROM mar_scheduled_doses
		 WHERE clinic_id = $1
		   AND scheduled_at >= $2
		   AND scheduled_at <  $3
		 ORDER BY scheduled_at ASC`
	rows, err := r.db.Query(ctx, q, clinicID, from, to)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.ListDueScheduledDoses: %w", err)
	}
	defer rows.Close()
	var out []*ScheduledDoseRecord
	for rows.Next() {
		var rec ScheduledDoseRecord
		if err := rows.Scan(&rec.ID, &rec.ClinicID, &rec.PrescriptionID, &rec.ScheduledAt, &rec.DoseQty, &rec.Route, &rec.GeneratedAt); err != nil {
			return nil, fmt.Errorf("mar.repo.ListDueScheduledDoses: %w", err)
		}
		out = append(out, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mar.repo.ListDueScheduledDoses: rows: %w", err)
	}
	return out, nil
}

// CreateRound starts a med-round session.
func (r *Repository) CreateRound(ctx context.Context, p CreateRoundParams) (*RoundRecord, error) {
	const q = `
		INSERT INTO mar_rounds (id, clinic_id, started_by, started_at, shift_label, location)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, clinic_id, started_by, started_at, completed_at, shift_label, location, notes`
	var rec RoundRecord
	err := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.StartedBy, p.StartedAt, p.ShiftLabel, p.Location,
	).Scan(&rec.ID, &rec.ClinicID, &rec.StartedBy, &rec.StartedAt, &rec.CompletedAt, &rec.ShiftLabel, &rec.Location, &rec.Notes)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.CreateRound: %w", err)
	}
	return &rec, nil
}

// CompleteRound stamps completed_at on a round.
func (r *Repository) CompleteRound(ctx context.Context, id, clinicID uuid.UUID, completedAt time.Time) error {
	const q = `UPDATE mar_rounds SET completed_at = $3
		WHERE id = $1 AND clinic_id = $2 AND completed_at IS NULL`
	if _, err := r.db.Exec(ctx, q, id, clinicID, completedAt); err != nil {
		return fmt.Errorf("mar.repo.CompleteRound: %w", err)
	}
	return nil
}

// ListRecentRounds returns the most recent N rounds for a clinic.
func (r *Repository) ListRecentRounds(ctx context.Context, clinicID uuid.UUID, limit int) ([]*RoundRecord, error) {
	const q = `
		SELECT id, clinic_id, started_by, started_at, completed_at, shift_label, location, notes
		  FROM mar_rounds
		 WHERE clinic_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2`
	rows, err := r.db.Query(ctx, q, clinicID, limit)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.ListRecentRounds: %w", err)
	}
	defer rows.Close()
	var out []*RoundRecord
	for rows.Next() {
		var rec RoundRecord
		if err := rows.Scan(&rec.ID, &rec.ClinicID, &rec.StartedBy, &rec.StartedAt, &rec.CompletedAt, &rec.ShiftLabel, &rec.Location, &rec.Notes); err != nil {
			return nil, fmt.Errorf("mar.repo.ListRecentRounds: %w", err)
		}
		out = append(out, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mar.repo.ListRecentRounds: rows: %w", err)
	}
	return out, nil
}

const adminEventCols = `
	id, clinic_id, resident_id, prescription_id,
	scheduled_dose_id, round_id,
	actual_at, actual_dose_qty, route,
	outcome_code, outcome_reason,
	administered_by, witness_id, notes,
	prn_indication_trigger, prn_effectiveness, prn_effectiveness_reviewed_at,
	drug_op_id, corrects_id,
	chain_key, entry_seq_in_chain, prev_row_hash, row_hash,
	created_at`

func scanAdminEvent(row pgx.Row) (*AdminEventRecord, error) {
	var r AdminEventRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.ResidentID, &r.PrescriptionID,
		&r.ScheduledDoseID, &r.RoundID,
		&r.ActualAt, &r.ActualDoseQty, &r.Route,
		&r.OutcomeCode, &r.OutcomeReason,
		&r.AdministeredBy, &r.WitnessID, &r.Notes,
		&r.PRNIndicationTrigger, &r.PRNEffectiveness, &r.PRNEffectivenessReviewedAt,
		&r.DrugOpID, &r.CorrectsID,
		&r.ChainKey, &r.EntrySeqInChain, &r.PrevRowHash, &r.RowHash,
		&r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("mar.repo.scanAdminEvent: %w", err)
	}
	return &r, nil
}

// CreateAdminEvent inserts the row. Service is responsible for the
// CD cross-link (drug_op_id) — Phase 3b wires that.
func (r *Repository) CreateAdminEvent(ctx context.Context, p CreateAdminEventParams) (*AdminEventRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO mar_administration_events (
			id, clinic_id, resident_id, prescription_id,
			scheduled_dose_id, round_id,
			actual_at, actual_dose_qty, route,
			outcome_code, outcome_reason,
			administered_by, witness_id, notes,
			prn_indication_trigger, prn_effectiveness, prn_effectiveness_reviewed_at,
			drug_op_id, corrects_id,
			chain_key, entry_seq_in_chain, prev_row_hash, row_hash
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7, $8, $9,
			$10, $11,
			$12, $13, $14,
			$15, $16, $17,
			$18, $19,
			$20, $21, $22, $23
		)
		RETURNING %s`, adminEventCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.ResidentID, p.PrescriptionID,
		p.ScheduledDoseID, p.RoundID,
		p.ActualAt, p.ActualDoseQty, p.Route,
		p.OutcomeCode, p.OutcomeReason,
		p.AdministeredBy, p.WitnessID, p.Notes,
		p.PRNIndicationTrigger, p.PRNEffectiveness, p.PRNEffectivenessReviewedAt,
		p.DrugOpID, p.CorrectsID,
		p.ChainKey, p.EntrySeqInChain, p.PrevRowHash, p.RowHash,
	)
	rec, err := scanAdminEvent(row)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.CreateAdminEvent: %w", err)
	}
	return rec, nil
}

// GetAdminEvent returns one event row.
func (r *Repository) GetAdminEvent(ctx context.Context, id, clinicID uuid.UUID) (*AdminEventRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM mar_administration_events WHERE id = $1 AND clinic_id = $2`, adminEventCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanAdminEvent(row)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.GetAdminEvent: %w", err)
	}
	return rec, nil
}

// ListAdminEventsForResident returns events in [from, to).
func (r *Repository) ListAdminEventsForResident(ctx context.Context, clinicID, residentID uuid.UUID, from, to time.Time) ([]*AdminEventRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s
		  FROM mar_administration_events
		 WHERE clinic_id = $1
		   AND resident_id = $2
		   AND actual_at >= $3
		   AND actual_at <  $4
		 ORDER BY actual_at DESC`, adminEventCols)
	rows, err := r.db.Query(ctx, q, clinicID, residentID, from, to)
	if err != nil {
		return nil, fmt.Errorf("mar.repo.ListAdminEventsForResident: %w", err)
	}
	defer rows.Close()
	var out []*AdminEventRecord
	for rows.Next() {
		rec, err := scanAdminEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("mar.repo.ListAdminEventsForResident: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mar.repo.ListAdminEventsForResident: rows: %w", err)
	}
	return out, nil
}
