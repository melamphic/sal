// Package drugs implements the controlled-drug register, clinic shelf
// inventory, append-only operations log, and monthly reconciliation flow.
//
// Design summary (see internal/drugs/README.md for the full spec):
//
//   - The system master catalog ships as data files in catalog/. It is
//     vertical × country scoped (vet/dental/general/aged_care × NZ/AU/UK/US).
//     Catalog entries are read-only at runtime; updates require a deploy.
//
//   - Clinic-specific custom drugs (compounded, locally-registered, brand
//     variants) live in clinic_drug_catalog_overrides — mutable, tenant-scoped.
//
//   - clinic_drug_shelf is the per-clinic inventory ledger: one row per
//     (drug × strength × batch × location). Balances are denormalised on
//     the row and recomputed transactionally with each operation.
//
//   - drug_operations_log is the append-only event store. UPDATEs are
//     forbidden — corrections happen via the addends_to chain. Once a row's
//     reconciliation_id is set, the row is locked even from addendums; the
//     correction must escalate through the discrepancy workflow.
//
//   - drug_reconciliation rows close out a (shelf, period) with two-staff
//     signoff for controlled drugs, then bulk-update operations to point at
//     the reconciliation_id, locking them. Discrepancy rows have status
//     'discrepancy_logged' and an explanation; reporting to regulator is a
//     separate state transition.
package drugs

import (
	"bytes"
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

// OverrideDrugRecord is a clinic-specific custom drug entry. clinic_drug_shelf
// rows reference one of these via override_drug_id when the drug isn't in the
// system master catalog (compounded products, locally-registered brands).
type OverrideDrugRecord struct {
	ID               uuid.UUID
	ClinicID         uuid.UUID
	Name             string
	ActiveIngredient *string
	Schedule         *string
	Strength         *string
	Form             *string
	BrandName        *string
	Notes            *string
	CreatedBy        uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ArchivedAt       *time.Time
}

// ShelfRecord is one entry on a clinic's drug shelf — a unique
// (drug × strength × batch × location) combination.
type ShelfRecord struct {
	ID              uuid.UUID
	ClinicID        uuid.UUID
	CatalogID       *string    // string id from system catalog when not an override
	OverrideDrugID  *uuid.UUID // FK to override row when the drug is clinic-defined
	Strength        *string
	Form            *string
	BatchNumber     *string
	ExpiryDate      *time.Time
	Location        string
	Balance         float64
	Unit            string
	ParLevel        *float64
	Notes           *string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ArchivedAt      *time.Time
}

// OperationRecord is one row of the append-only operations ledger.
//
// Invariants enforced at the service layer (and partly in DB):
//   - subject_id required for administer/dispense
//   - witnessed_by required when the underlying drug is controlled
//   - balance_after = balance_before + qty (receive) or - qty (administer/dispense/discard)
//   - reconciliation_id NULL until the period is closed; once set, the row is
//     locked (no addendums)
//   - addends_to references the original operation when this row is a correction
type OperationRecord struct {
	ID                uuid.UUID
	ClinicID          uuid.UUID
	ShelfID           uuid.UUID
	SubjectID         *uuid.UUID
	NoteID            *uuid.UUID
	NoteFieldID       *uuid.UUID
	Operation         string // administer | dispense | discard | receive | transfer | adjust
	Quantity          float64
	Unit              string
	Dose              *string
	Route             *string
	ReasonIndication  *string
	AdministeredBy    uuid.UUID
	WitnessedBy       *uuid.UUID
	PrescribedBy      *uuid.UUID
	BalanceBefore     float64
	BalanceAfter      float64
	ReconciliationID  *uuid.UUID
	AddendsTo         *uuid.UUID
	// Status: 'pending_confirm' | 'confirmed'. Set to 'pending_confirm'
	// when the row is created via a system.drug_op widget on a form;
	// flipped to 'confirmed' when the clinician confirms via the note
	// review surface. Manual modal-driven creates default to 'confirmed'
	// (always an explicit user action).
	Status            string
	ConfirmedBy       *uuid.UUID
	ConfirmedAt       *time.Time
	CreatedAt         time.Time
}

// ReconciliationRecord closes a (shelf, period) with two-staff signoff.
// The discrepancy column is computed by Postgres (GENERATED ALWAYS).
type ReconciliationRecord struct {
	ID                       uuid.UUID
	ClinicID                 uuid.UUID
	ShelfID                  uuid.UUID
	PeriodStart              time.Time
	PeriodEnd                time.Time
	PhysicalCount            float64
	LedgerCount              float64
	Discrepancy              float64
	ReconciledByPrimary      uuid.UUID
	ReconciledBySecondary    *uuid.UUID
	Status                   string
	DiscrepancyExplanation   *string
	ReportedAt               *time.Time
	ReportedBy               *uuid.UUID
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// ── Param types — keep tightly scoped to the SQL each method runs ──────────

// CreateOverrideDrugParams holds the values needed to insert a custom drug.
type CreateOverrideDrugParams struct {
	ID               uuid.UUID
	ClinicID         uuid.UUID
	Name             string
	ActiveIngredient *string
	Schedule         *string
	Strength         *string
	Form             *string
	BrandName        *string
	Notes            *string
	CreatedBy        uuid.UUID
}

// UpdateOverrideDrugParams covers metadata-only updates. Archived rows are
// updated via ArchiveOverrideDrug, not this method.
type UpdateOverrideDrugParams struct {
	ID               uuid.UUID
	ClinicID         uuid.UUID
	Name             string
	ActiveIngredient *string
	Schedule         *string
	Strength         *string
	Form             *string
	BrandName        *string
	Notes            *string
}

// CreateShelfEntryParams is the insert shape. Exactly one of CatalogID /
// OverrideDrugID must be set; service layer enforces this before calling.
type CreateShelfEntryParams struct {
	ID             uuid.UUID
	ClinicID       uuid.UUID
	CatalogID      *string
	OverrideDrugID *uuid.UUID
	Strength       *string
	Form           *string
	BatchNumber    *string
	ExpiryDate     *time.Time
	Location       string
	Balance        float64
	Unit           string
	ParLevel       *float64
	Notes          *string
	CreatedBy      uuid.UUID
}

// UpdateShelfMetaParams — non-balance updates. Balance changes happen ONLY
// through CreateOperation in a transaction.
type UpdateShelfMetaParams struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	Location string
	ParLevel *float64
	Notes    *string
}

// CreateOperationParams is the insert shape for the ledger. The service layer
// computes balance_before / balance_after inside the same transaction that
// updates clinic_drug_shelf.balance — see Service.LogOperation.
type CreateOperationParams struct {
	ID                uuid.UUID
	ClinicID          uuid.UUID
	ShelfID           uuid.UUID
	SubjectID         *uuid.UUID
	NoteID            *uuid.UUID
	NoteFieldID       *uuid.UUID
	Operation         string
	Quantity          float64
	Unit              string
	Dose              *string
	Route             *string
	ReasonIndication  *string
	AdministeredBy    uuid.UUID
	WitnessedBy       *uuid.UUID
	PrescribedBy      *uuid.UUID
	BalanceBefore     float64
	BalanceAfter      float64
	AddendsTo         *uuid.UUID
	// Status — 'pending_confirm' for system.drug_op widget creates,
	// 'confirmed' for manual modal creates. Empty string is treated as
	// 'confirmed' for backwards compat with callers that haven't been
	// updated.
	Status            string

	// ── Compliance v2 fields (Phase 2b) ────────────────────────────────
	//
	// Page-identity snapshot — service populates from
	// catalogContext at insert time. Used by the repo to compute
	// chain_key + entry_seq_in_chain inside the same tx.
	DrugName     string
	DrugStrength string
	DrugForm     string

	// ChainKey — deterministic SHA256 over (clinic, drug_name,
	// strength, form). Empty when service didn't provide it (legacy
	// caller); repo skips chain compute in that case so v1 callers
	// keep working.
	ChainKey []byte

	// RetentionUntil — derived from clinic country at the service
	// layer; nil for legacy callers.
	RetentionUntil *time.Time
}

// ListShelfParams — filters for the shelf listing.
type ListShelfParams struct {
	Limit           int
	Offset          int
	IncludeArchived bool
	Location        *string
	Search          *string // matches override name OR catalog id substring
}

// ListOperationsParams — filters for the ledger listing.
type ListOperationsParams struct {
	Limit              int
	Offset             int
	ShelfID            *uuid.UUID
	SubjectID          *uuid.UUID
	NoteID             *uuid.UUID
	Operation          *string
	Since              *time.Time
	Until              *time.Time
	OnlyPendingRecon   bool // reconciliation_id IS NULL
	OnlyControlled     bool // joins shelf+catalog/override to filter to controlled drugs
}

// CreateReconciliationParams — start of a reconciliation. status begins
// 'clean'; the service layer flips it based on physical vs ledger diff.
type CreateReconciliationParams struct {
	ID                       uuid.UUID
	ClinicID                 uuid.UUID
	ShelfID                  uuid.UUID
	PeriodStart              time.Time
	PeriodEnd                time.Time
	PhysicalCount            float64
	LedgerCount              float64
	ReconciledByPrimary      uuid.UUID
	DiscrepancyExplanation   *string
}

// UpdateReconciliationStatusParams — used for the secondary-sign,
// log-discrepancy, and mark-reported state transitions.
type UpdateReconciliationStatusParams struct {
	ID                     uuid.UUID
	ClinicID               uuid.UUID
	Status                 string
	ReconciledBySecondary  *uuid.UUID
	DiscrepancyExplanation *string
	ReportedAt             *time.Time
	ReportedBy             *uuid.UUID
}

// LockOperationsParams binds drug_operations_log rows to a reconciliation,
// locking them against further addendums.
type LockOperationsParams struct {
	ReconciliationID uuid.UUID
	ShelfID          uuid.UUID
	PeriodStart      time.Time
	PeriodEnd        time.Time
}

// ListReconciliationsParams — filters for the reconciliation history.
type ListReconciliationsParams struct {
	Limit   int
	Offset  int
	ShelfID *uuid.UUID
	Status  *string
	Since   *time.Time
	Until   *time.Time
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the Postgres implementation of the drugs data-access layer.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a Repository from a pgx pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Compile-time check that Repository implements the repo interface.
var _ repo = (*Repository)(nil)

// ── scan helpers ──────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanOverrideDrug(row scannable) (*OverrideDrugRecord, error) {
	var r OverrideDrugRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.Name, &r.ActiveIngredient, &r.Schedule,
		&r.Strength, &r.Form, &r.BrandName, &r.Notes,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("drugs.repo.scanOverrideDrug: %w", err)
	}
	return &r, nil
}

const overrideDrugCols = `
	id, clinic_id, name, active_ingredient, schedule,
	strength, form, brand_name, notes,
	created_by, created_at, updated_at, archived_at`

func scanShelf(row scannable) (*ShelfRecord, error) {
	var r ShelfRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.CatalogID, &r.OverrideDrugID,
		&r.Strength, &r.Form, &r.BatchNumber, &r.ExpiryDate,
		&r.Location, &r.Balance, &r.Unit, &r.ParLevel, &r.Notes,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("drugs.repo.scanShelf: %w", err)
	}
	return &r, nil
}

const shelfCols = `
	id, clinic_id, catalog_id, override_drug_id,
	strength, form, batch_number, expiry_date,
	location, balance, unit, par_level, notes,
	created_by, created_at, updated_at, archived_at`

func scanOperation(row scannable) (*OperationRecord, error) {
	var r OperationRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.ShelfID, &r.SubjectID, &r.NoteID, &r.NoteFieldID,
		&r.Operation, &r.Quantity, &r.Unit, &r.Dose, &r.Route, &r.ReasonIndication,
		&r.AdministeredBy, &r.WitnessedBy, &r.PrescribedBy,
		&r.BalanceBefore, &r.BalanceAfter,
		&r.ReconciliationID, &r.AddendsTo,
		&r.Status, &r.ConfirmedBy, &r.ConfirmedAt,
		&r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("drugs.repo.scanOperation: %w", err)
	}
	return &r, nil
}

const operationCols = `
	id, clinic_id, shelf_id, subject_id, note_id, note_field_id,
	operation, quantity, unit, dose, route, reason_indication,
	administered_by, witnessed_by, prescribed_by,
	balance_before, balance_after,
	reconciliation_id, addends_to,
	status, confirmed_by, confirmed_at,
	created_at`

func scanReconciliation(row scannable) (*ReconciliationRecord, error) {
	var r ReconciliationRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.ShelfID,
		&r.PeriodStart, &r.PeriodEnd,
		&r.PhysicalCount, &r.LedgerCount, &r.Discrepancy,
		&r.ReconciledByPrimary, &r.ReconciledBySecondary,
		&r.Status, &r.DiscrepancyExplanation, &r.ReportedAt, &r.ReportedBy,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("drugs.repo.scanReconciliation: %w", err)
	}
	return &r, nil
}

const reconciliationCols = `
	id, clinic_id, shelf_id,
	period_start, period_end,
	physical_count, ledger_count, discrepancy,
	reconciled_by_primary, reconciled_by_secondary,
	status, discrepancy_explanation, reported_at, reported_by,
	created_at, updated_at`

// ── OverrideDrug methods ─────────────────────────────────────────────────────

// CreateOverrideDrug inserts a clinic-defined drug entry that doesn't
// appear in the system master catalog.
func (r *Repository) CreateOverrideDrug(ctx context.Context, p CreateOverrideDrugParams) (*OverrideDrugRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO clinic_drug_catalog_overrides (
			id, clinic_id, name, active_ingredient, schedule,
			strength, form, brand_name, notes, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING %s`, overrideDrugCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.Name, p.ActiveIngredient, p.Schedule,
		p.Strength, p.Form, p.BrandName, p.Notes, p.CreatedBy,
	)
	rec, err := scanOverrideDrug(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.CreateOverrideDrug: %w", err)
	}
	return rec, nil
}

// GetOverrideDrugByID returns a single override entry. Tenant-scoped.
func (r *Repository) GetOverrideDrugByID(ctx context.Context, id, clinicID uuid.UUID) (*OverrideDrugRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM clinic_drug_catalog_overrides
		WHERE id = $1 AND clinic_id = $2`, overrideDrugCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanOverrideDrug(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.GetOverrideDrugByID: %w", err)
	}
	return rec, nil
}

// ListOverrideDrugs returns all non-archived overrides for a clinic.
func (r *Repository) ListOverrideDrugs(ctx context.Context, clinicID uuid.UUID) ([]*OverrideDrugRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM clinic_drug_catalog_overrides
		WHERE clinic_id = $1 AND archived_at IS NULL
		ORDER BY name ASC`, overrideDrugCols)
	rows, err := r.db.Query(ctx, q, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ListOverrideDrugs: %w", err)
	}
	defer rows.Close()
	var out []*OverrideDrugRecord
	for rows.Next() {
		rec, err := scanOverrideDrug(rows)
		if err != nil {
			return nil, fmt.Errorf("drugs.repo.ListOverrideDrugs: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drugs.repo.ListOverrideDrugs: rows: %w", err)
	}
	return out, nil
}

// UpdateOverrideDrug updates metadata on an existing override.
func (r *Repository) UpdateOverrideDrug(ctx context.Context, p UpdateOverrideDrugParams) (*OverrideDrugRecord, error) {
	q := fmt.Sprintf(`
		UPDATE clinic_drug_catalog_overrides
		   SET name = $3, active_ingredient = $4, schedule = $5,
		       strength = $6, form = $7, brand_name = $8, notes = $9,
		       updated_at = NOW()
		 WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING %s`, overrideDrugCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.Name, p.ActiveIngredient, p.Schedule,
		p.Strength, p.Form, p.BrandName, p.Notes,
	)
	rec, err := scanOverrideDrug(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.UpdateOverrideDrug: %w", err)
	}
	return rec, nil
}

// ArchiveOverrideDrug soft-deletes an override. Existing shelf rows that
// reference this override remain valid (the FK is preserved); only new
// shelf rows are blocked.
func (r *Repository) ArchiveOverrideDrug(ctx context.Context, id, clinicID uuid.UUID) error {
	const q = `
		UPDATE clinic_drug_catalog_overrides
		   SET archived_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL`
	tag, err := r.db.Exec(ctx, q, id, clinicID)
	if err != nil {
		return fmt.Errorf("drugs.repo.ArchiveOverrideDrug: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("drugs.repo.ArchiveOverrideDrug: %w", domain.ErrNotFound)
	}
	return nil
}

// ── Shelf methods ────────────────────────────────────────────────────────────

// CreateShelfEntry inserts a new shelf entry. The unique partial index
// (clinic_id, drug source, strength, batch, location) makes duplicate
// active rows a unique-violation; service layer maps that to ErrConflict.
func (r *Repository) CreateShelfEntry(ctx context.Context, p CreateShelfEntryParams) (*ShelfRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO clinic_drug_shelf (
			id, clinic_id, catalog_id, override_drug_id,
			strength, form, batch_number, expiry_date,
			location, balance, unit, par_level, notes, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING %s`, shelfCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.CatalogID, p.OverrideDrugID,
		p.Strength, p.Form, p.BatchNumber, p.ExpiryDate,
		p.Location, p.Balance, p.Unit, p.ParLevel, p.Notes, p.CreatedBy,
	)
	rec, err := scanShelf(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("drugs.repo.CreateShelfEntry: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("drugs.repo.CreateShelfEntry: %w", err)
	}
	return rec, nil
}

// GetShelfEntryByID returns one shelf entry. Tenant-scoped.
func (r *Repository) GetShelfEntryByID(ctx context.Context, id, clinicID uuid.UUID) (*ShelfRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM clinic_drug_shelf
		WHERE id = $1 AND clinic_id = $2`, shelfCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanShelf(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.GetShelfEntryByID: %w", err)
	}
	return rec, nil
}

// ListShelfEntries returns paginated shelf rows with optional filters.
// search matches override.name (when joined) or catalog_id substring.
func (r *Repository) ListShelfEntries(ctx context.Context, clinicID uuid.UUID, p ListShelfParams) ([]*ShelfRecord, int, error) {
	args := []any{clinicID}
	where := "s.clinic_id = $1"
	if !p.IncludeArchived {
		where += " AND s.archived_at IS NULL"
	}
	if p.Location != nil && *p.Location != "" {
		args = append(args, *p.Location)
		where += fmt.Sprintf(" AND s.location = $%d", len(args))
	}
	if p.Search != nil && *p.Search != "" {
		args = append(args, "%"+*p.Search+"%")
		// catalog_id is an opaque string; override drugs have a name we can match.
		where += fmt.Sprintf(
			" AND (s.catalog_id ILIKE $%d OR EXISTS ("+
				"SELECT 1 FROM clinic_drug_catalog_overrides o "+
				"WHERE o.id = s.override_drug_id AND o.name ILIKE $%d))",
			len(args), len(args),
		)
	}

	countQ := fmt.Sprintf(`SELECT COUNT(*) FROM clinic_drug_shelf s WHERE %s`, where)
	var total int
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListShelfEntries: count: %w", err)
	}

	listQ := fmt.Sprintf(`SELECT %s FROM clinic_drug_shelf s WHERE %s
		ORDER BY s.created_at DESC LIMIT $%d OFFSET $%d`,
		shelfCols, where, len(args)+1, len(args)+2)
	args = append(args, p.Limit, p.Offset)
	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListShelfEntries: list: %w", err)
	}
	defer rows.Close()
	var out []*ShelfRecord
	for rows.Next() {
		rec, err := scanShelf(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("drugs.repo.ListShelfEntries: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListShelfEntries: rows: %w", err)
	}
	return out, total, nil
}

// UpdateShelfMeta updates non-balance metadata. For balance changes use
// CreateOperation in a transaction (Repository.LogOperation).
func (r *Repository) UpdateShelfMeta(ctx context.Context, p UpdateShelfMetaParams) (*ShelfRecord, error) {
	q := fmt.Sprintf(`
		UPDATE clinic_drug_shelf
		   SET location = $3, par_level = $4, notes = $5, updated_at = NOW()
		 WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING %s`, shelfCols)
	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.Location, p.ParLevel, p.Notes)
	rec, err := scanShelf(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.UpdateShelfMeta: %w", err)
	}
	return rec, nil
}

// ArchiveShelfEntry soft-deletes a shelf entry. Existing operations log rows
// referring to it remain valid (FK preserved).
func (r *Repository) ArchiveShelfEntry(ctx context.Context, id, clinicID uuid.UUID) error {
	const q = `
		UPDATE clinic_drug_shelf
		   SET archived_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL`
	tag, err := r.db.Exec(ctx, q, id, clinicID)
	if err != nil {
		return fmt.Errorf("drugs.repo.ArchiveShelfEntry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("drugs.repo.ArchiveShelfEntry: %w", domain.ErrNotFound)
	}
	return nil
}

// LogOperation atomically:
//   - inserts the new operations log row
//   - updates clinic_drug_shelf.balance to balance_after
// inside a single transaction.
//
// The caller (service layer) computed balance_before / balance_after from a
// fresh read of the shelf row; this method does NOT recompute. It does
// verify with FOR UPDATE that the shelf balance is still balance_before to
// detect concurrent updates — in that case it returns ErrConflict.
func (r *Repository) LogOperation(ctx context.Context, p CreateOperationParams) (*OperationRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.LogOperation: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the shelf row + verify balance hasn't changed under us.
	const lockQ = `SELECT balance FROM clinic_drug_shelf
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL FOR UPDATE`
	var currentBalance float64
	if err := tx.QueryRow(ctx, lockQ, p.ShelfID, p.ClinicID).Scan(&currentBalance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("drugs.repo.LogOperation: shelf: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("drugs.repo.LogOperation: lock shelf: %w", err)
	}
	if currentBalance != p.BalanceBefore {
		return nil, fmt.Errorf("drugs.repo.LogOperation: balance changed under us, expected %v got %v: %w",
			p.BalanceBefore, currentBalance, domain.ErrConflict)
	}

	// Default status to 'confirmed' for backwards-compat with the manual
	// modal-driven path; system.drug_op widgets pass 'pending_confirm'.
	status := p.Status
	if status == "" {
		status = "confirmed"
	}

	// Compliance v2: compute chain fields when the service supplied a
	// chain key. Legacy callers (v1) leave ChainKey empty and the row
	// inserts without chain fields — keeps existing flows working
	// while we backfill legacy rows out-of-band.
	var (
		entrySeqInChain *int64
		prevRowHash     []byte
		rowHash         []byte
	)
	if len(p.ChainKey) > 0 {
		// Advisory lock per chain — serialises concurrent inserts on
		// the same drug-strength-form page within the txn lifetime.
		lockID := chainAdvisoryLockID(p.ChainKey)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
			return nil, fmt.Errorf("drugs.repo.LogOperation: chain advisory lock: %w", err)
		}

		const headQ = `
			SELECT row_hash, entry_seq_in_chain
			  FROM drug_operations_log
			 WHERE clinic_id = $1 AND chain_key = $2
			   AND entry_seq_in_chain IS NOT NULL
			 ORDER BY entry_seq_in_chain DESC
			 LIMIT 1`
		var prevSeq int64
		err := tx.QueryRow(ctx, headQ, p.ClinicID, p.ChainKey).Scan(&prevRowHash, &prevSeq)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// First row in this chain.
			prevRowHash = ZeroHash()
			prevSeq = 0
		case err != nil:
			return nil, fmt.Errorf("drugs.repo.LogOperation: read chain head: %w", err)
		}

		nextSeq := prevSeq + 1
		entrySeqInChain = &nextSeq

		canonical := canonicalRowBytes(
			p.ID, p.ClinicID, p.ChainKey, nextSeq,
			p.Operation, p.Quantity, p.Unit,
			p.DrugName, p.DrugStrength, p.DrugForm,
			p.BalanceAfter, prevRowHash,
		)
		rowHash = computeRowHash(canonical, prevRowHash)
	}

	// Insert the operation row. Chain fields + page-identity snapshots
	// + retention_until ride along with v1 fields; all are NULLABLE so
	// legacy callers (ChainKey empty) get NULLs.
	insertQ := fmt.Sprintf(`
		INSERT INTO drug_operations_log (
			id, clinic_id, shelf_id, subject_id, note_id, note_field_id,
			operation, quantity, unit, dose, route, reason_indication,
			administered_by, witnessed_by, prescribed_by,
			balance_before, balance_after, addends_to,
			status,
			drug_name, drug_strength, drug_form,
			chain_key, entry_seq_in_chain, prev_row_hash, row_hash,
			retention_until
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15,
			$16, $17, $18,
			$19,
			$20, $21, $22,
			$23, $24, $25, $26,
			$27
		)
		RETURNING %s`, operationCols)
	row := tx.QueryRow(ctx, insertQ,
		p.ID, p.ClinicID, p.ShelfID, p.SubjectID, p.NoteID, p.NoteFieldID,
		p.Operation, p.Quantity, p.Unit, p.Dose, p.Route, p.ReasonIndication,
		p.AdministeredBy, p.WitnessedBy, p.PrescribedBy,
		p.BalanceBefore, p.BalanceAfter, p.AddendsTo,
		status,
		nullIfEmpty(p.DrugName), nullIfEmpty(p.DrugStrength), nullIfEmpty(p.DrugForm),
		nullableBytes(p.ChainKey), entrySeqInChain, nullableBytes(prevRowHash), nullableBytes(rowHash),
		p.RetentionUntil,
	)
	rec, err := scanOperation(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.LogOperation: insert op: %w", err)
	}

	// Update the shelf balance.
	const updateQ = `UPDATE clinic_drug_shelf
		SET balance = $3, updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2`
	if _, err := tx.Exec(ctx, updateQ, p.ShelfID, p.ClinicID, p.BalanceAfter); err != nil {
		return nil, fmt.Errorf("drugs.repo.LogOperation: update balance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("drugs.repo.LogOperation: commit: %w", err)
	}
	return rec, nil
}

// GetOperationByID returns one operation row. Tenant-scoped.
func (r *Repository) GetOperationByID(ctx context.Context, id, clinicID uuid.UUID) (*OperationRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM drug_operations_log
		WHERE id = $1 AND clinic_id = $2`, operationCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanOperation(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.GetOperationByID: %w", err)
	}
	return rec, nil
}

// ConfirmOperation flips a pending_confirm row to confirmed, stamping
// confirmed_by + confirmed_at. Idempotent on already-confirmed rows
// (returns the existing row unchanged) so the note-submit pipeline can
// re-call without trouble.
func (r *Repository) ConfirmOperation(ctx context.Context, id, clinicID, staffID uuid.UUID) (*OperationRecord, error) {
	q := fmt.Sprintf(`
		UPDATE drug_operations_log
		   SET status = 'confirmed',
		       confirmed_by = $3,
		       confirmed_at = NOW()
		 WHERE id = $1 AND clinic_id = $2 AND status = 'pending_confirm'
		 RETURNING %s`, operationCols)
	row := r.db.QueryRow(ctx, q, id, clinicID, staffID)
	rec, err := scanOperation(row)
	if err != nil {
		// No row updated → either it didn't exist or it was already
		// confirmed. Fall back to a plain Get to disambiguate; treat
		// already-confirmed as success (idempotent).
		if errors.Is(err, domain.ErrNotFound) {
			existing, getErr := r.GetOperationByID(ctx, id, clinicID)
			if getErr != nil {
				return nil, fmt.Errorf("drugs.repo.ConfirmOperation: %w", getErr)
			}
			if existing.Status == "confirmed" {
				return existing, nil
			}
			return nil, fmt.Errorf("drugs.repo.ConfirmOperation: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("drugs.repo.ConfirmOperation: %w", err)
	}
	return rec, nil
}

// ListPendingConfirmForNote returns drug ops linked to a note that still
// need clinician confirmation. Used by the note-submit gate.
func (r *Repository) ListPendingConfirmForNote(ctx context.Context, noteID, clinicID uuid.UUID) ([]*OperationRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM drug_operations_log
		WHERE note_id = $1 AND clinic_id = $2 AND status = 'pending_confirm'
		ORDER BY created_at`, operationCols)
	rows, err := r.db.Query(ctx, q, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ListPendingConfirmForNote: %w", err)
	}
	defer rows.Close()
	var out []*OperationRecord
	for rows.Next() {
		rec, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("drugs.repo.ListPendingConfirmForNote: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drugs.repo.ListPendingConfirmForNote: %w", err)
	}
	return out, nil
}

// ListOperations returns the operations ledger with the requested filters.
// Always clinic-scoped. Results ordered by created_at DESC (newest first).
func (r *Repository) ListOperations(ctx context.Context, clinicID uuid.UUID, p ListOperationsParams) ([]*OperationRecord, int, error) {
	args := []any{clinicID}
	where := "o.clinic_id = $1"
	if p.ShelfID != nil {
		args = append(args, *p.ShelfID)
		where += fmt.Sprintf(" AND o.shelf_id = $%d", len(args))
	}
	if p.SubjectID != nil {
		args = append(args, *p.SubjectID)
		where += fmt.Sprintf(" AND o.subject_id = $%d", len(args))
	}
	if p.NoteID != nil {
		args = append(args, *p.NoteID)
		where += fmt.Sprintf(" AND o.note_id = $%d", len(args))
	}
	if p.Operation != nil && *p.Operation != "" {
		args = append(args, *p.Operation)
		where += fmt.Sprintf(" AND o.operation = $%d", len(args))
	}
	if p.Since != nil {
		args = append(args, *p.Since)
		where += fmt.Sprintf(" AND o.created_at >= $%d", len(args))
	}
	if p.Until != nil {
		args = append(args, *p.Until)
		where += fmt.Sprintf(" AND o.created_at <= $%d", len(args))
	}
	if p.OnlyPendingRecon {
		where += " AND o.reconciliation_id IS NULL"
	}
	// Caller can ask for a specific note's ledger (note review surface
	// uses this with NoteID set, regardless of submit state). All other
	// list queries — patient ledger, reconciliation pending list, the
	// drug history page — exclude rows tied to a draft note so
	// unfinished work doesn't pollute the regulator-facing ledger.
	if p.NoteID == nil {
		where += " AND (o.note_id IS NULL OR o.note_id IN (SELECT id FROM notes WHERE status = 'submitted'))"
	}

	countQ := fmt.Sprintf(`SELECT COUNT(*) FROM drug_operations_log o WHERE %s`, where)
	var total int
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListOperations: count: %w", err)
	}

	listQ := fmt.Sprintf(`SELECT %s FROM drug_operations_log o WHERE %s
		ORDER BY o.created_at DESC LIMIT $%d OFFSET $%d`,
		operationCols, where, len(args)+1, len(args)+2)
	args = append(args, p.Limit, p.Offset)
	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListOperations: list: %w", err)
	}
	defer rows.Close()
	var out []*OperationRecord
	for rows.Next() {
		rec, err := scanOperation(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("drugs.repo.ListOperations: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListOperations: rows: %w", err)
	}
	return out, total, nil
}

// SumLedgerForShelfPeriod returns the net change in balance over a period
// for a shelf entry (used by reconciliation to compute the expected
// ledger_count). Receive adds, everything else subtracts.
func (r *Repository) SumLedgerForShelfPeriod(ctx context.Context, shelfID, clinicID uuid.UUID, periodStart, periodEnd time.Time) (float64, error) {
	const q = `
		SELECT COALESCE(SUM(
			CASE WHEN operation = 'receive'
			     THEN quantity
			     WHEN operation = 'transfer'
			     THEN 0  -- net-zero across locations; future-proofing
			     ELSE -quantity
			END
		), 0)::FLOAT8
		FROM drug_operations_log
		WHERE shelf_id = $1
		  AND clinic_id = $2
		  AND created_at >= $3
		  AND created_at <= $4
		  AND reconciliation_id IS NULL
		  AND (note_id IS NULL OR note_id IN (SELECT id FROM notes WHERE status = 'submitted'))`
	var sum float64
	if err := r.db.QueryRow(ctx, q, shelfID, clinicID, periodStart, periodEnd).Scan(&sum); err != nil {
		return 0, fmt.Errorf("drugs.repo.SumLedgerForShelfPeriod: %w", err)
	}
	return sum, nil
}

// ── Reconciliation methods ───────────────────────────────────────────────────

// CreateReconciliation inserts a new reconciliation row in 'clean' status.
// The service layer flips status based on the discrepancy magnitude after
// computing it from the params.
func (r *Repository) CreateReconciliation(ctx context.Context, p CreateReconciliationParams) (*ReconciliationRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO drug_reconciliation (
			id, clinic_id, shelf_id, period_start, period_end,
			physical_count, ledger_count,
			reconciled_by_primary, status, discrepancy_explanation
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
		          CASE WHEN $6 = $7 THEN 'clean' ELSE 'discrepancy_logged' END,
		          $9)
		RETURNING %s`, reconciliationCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.ShelfID, p.PeriodStart, p.PeriodEnd,
		p.PhysicalCount, p.LedgerCount,
		p.ReconciledByPrimary, p.DiscrepancyExplanation,
	)
	rec, err := scanReconciliation(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("drugs.repo.CreateReconciliation: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("drugs.repo.CreateReconciliation: %w", err)
	}
	return rec, nil
}

// GetReconciliationByID returns one reconciliation row.
func (r *Repository) GetReconciliationByID(ctx context.Context, id, clinicID uuid.UUID) (*ReconciliationRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM drug_reconciliation
		WHERE id = $1 AND clinic_id = $2`, reconciliationCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanReconciliation(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.GetReconciliationByID: %w", err)
	}
	return rec, nil
}

// UpdateReconciliationStatus handles state transitions (secondary signoff,
// discrepancy explanation, mark-reported). Service layer enforces which
// transitions are valid; this method just persists.
func (r *Repository) UpdateReconciliationStatus(ctx context.Context, p UpdateReconciliationStatusParams) (*ReconciliationRecord, error) {
	q := fmt.Sprintf(`
		UPDATE drug_reconciliation
		   SET status = $3,
		       reconciled_by_secondary = COALESCE($4, reconciled_by_secondary),
		       discrepancy_explanation = COALESCE($5, discrepancy_explanation),
		       reported_at = COALESCE($6, reported_at),
		       reported_by = COALESCE($7, reported_by),
		       updated_at = NOW()
		 WHERE id = $1 AND clinic_id = $2
		RETURNING %s`, reconciliationCols)
	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.Status,
		p.ReconciledBySecondary, p.DiscrepancyExplanation,
		p.ReportedAt, p.ReportedBy,
	)
	rec, err := scanReconciliation(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.UpdateReconciliationStatus: %w", err)
	}
	return rec, nil
}

// LockOperationsToReconciliation sets reconciliation_id on every operations
// row in (shelf, period). Once set, those rows are immutable — addendums
// targeting them must escalate via the reconciliation discrepancy workflow.
func (r *Repository) LockOperationsToReconciliation(ctx context.Context, p LockOperationsParams) (int64, error) {
	const q = `
		UPDATE drug_operations_log
		   SET reconciliation_id = $1
		 WHERE shelf_id = $2
		   AND created_at >= $3
		   AND created_at <= $4
		   AND reconciliation_id IS NULL`
	tag, err := r.db.Exec(ctx, q, p.ReconciliationID, p.ShelfID, p.PeriodStart, p.PeriodEnd)
	if err != nil {
		return 0, fmt.Errorf("drugs.repo.LockOperationsToReconciliation: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListReconciliations returns paginated reconciliation history.
func (r *Repository) ListReconciliations(ctx context.Context, clinicID uuid.UUID, p ListReconciliationsParams) ([]*ReconciliationRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"
	if p.ShelfID != nil {
		args = append(args, *p.ShelfID)
		where += fmt.Sprintf(" AND shelf_id = $%d", len(args))
	}
	if p.Status != nil && *p.Status != "" {
		args = append(args, *p.Status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if p.Since != nil {
		args = append(args, *p.Since)
		where += fmt.Sprintf(" AND period_end >= $%d", len(args))
	}
	if p.Until != nil {
		args = append(args, *p.Until)
		where += fmt.Sprintf(" AND period_end <= $%d", len(args))
	}

	countQ := fmt.Sprintf(`SELECT COUNT(*) FROM drug_reconciliation WHERE %s`, where)
	var total int
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListReconciliations: count: %w", err)
	}

	listQ := fmt.Sprintf(`SELECT %s FROM drug_reconciliation WHERE %s
		ORDER BY period_end DESC LIMIT $%d OFFSET $%d`,
		reconciliationCols, where, len(args)+1, len(args)+2)
	args = append(args, p.Limit, p.Offset)
	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListReconciliations: list: %w", err)
	}
	defer rows.Close()
	var out []*ReconciliationRecord
	for rows.Next() {
		rec, err := scanReconciliation(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("drugs.repo.ListReconciliations: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("drugs.repo.ListReconciliations: rows: %w", err)
	}
	return out, total, nil
}

// RetentionPolicy carries the per-clinic retention years from
// clinic_drug_retention_policy.
type RetentionPolicy struct {
	ClinicID     uuid.UUID
	LedgerYears  int
	ReconYears   int
	MARYears     int
}

// BackfillChainRowParams stamps chain fields onto a legacy row.
type BackfillChainRowParams struct {
	ID              uuid.UUID
	ClinicID        uuid.UUID
	DrugName        string
	DrugStrength    string
	DrugForm        string
	ChainKey        []byte
	EntrySeqInChain int64
	PrevRowHash     []byte
	RowHash         []byte
	RetentionUntil  *time.Time
}

// GetRetentionPolicy reads the per-clinic retention policy. Returns
// ErrNotFound if the clinic was created before migration 00068 and no
// row was seeded — caller should fall back to country defaults.
func (r *Repository) GetRetentionPolicy(ctx context.Context, clinicID uuid.UUID) (*RetentionPolicy, error) {
	const q = `
		SELECT clinic_id, ledger_years, recon_years, mar_years
		  FROM clinic_drug_retention_policy
		 WHERE clinic_id = $1`
	var p RetentionPolicy
	err := r.db.QueryRow(ctx, q, clinicID).Scan(
		&p.ClinicID, &p.LedgerYears, &p.ReconYears, &p.MARYears,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("drugs.repo.GetRetentionPolicy: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("drugs.repo.GetRetentionPolicy: %w", err)
	}
	return &p, nil
}

// SoftDeleteOpsPastRetention sets archived_at on every active ledger
// row whose retention_until has passed. Append-only is preserved
// (rows still exist + still verify in the chain). Physical purge
// happens via a separate admin endpoint with grace window — see
// design doc §5.5.
func (r *Repository) SoftDeleteOpsPastRetention(ctx context.Context, asOf time.Time) (int64, error) {
	const q = `
		UPDATE drug_operations_log
		   SET archived_at = NOW()
		 WHERE retention_until IS NOT NULL
		   AND retention_until < $1::date
		   AND archived_at IS NULL`
	tag, err := r.db.Exec(ctx, q, asOf)
	if err != nil {
		return 0, fmt.Errorf("drugs.repo.SoftDeleteOpsPastRetention: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListLegacyOpsForBackfill returns rows in (clinic, created_at) order
// that lack chain_key — i.e. inserted before Phase 2 wired up the
// chain. Bounded by limit + a since cursor (use the previous page's
// last created_at as the next page's `since`).
func (r *Repository) ListLegacyOpsForBackfill(ctx context.Context, clinicID uuid.UUID, since time.Time, limit int) ([]*OperationRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s
		  FROM drug_operations_log
		 WHERE clinic_id = $1
		   AND chain_key IS NULL
		   AND created_at >= $2
		 ORDER BY created_at ASC
		 LIMIT $3`, operationCols)
	rows, err := r.db.Query(ctx, q, clinicID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ListLegacyOpsForBackfill: %w", err)
	}
	defer rows.Close()
	var out []*OperationRecord
	for rows.Next() {
		rec, err := scanOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("drugs.repo.ListLegacyOpsForBackfill: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drugs.repo.ListLegacyOpsForBackfill: rows: %w", err)
	}
	return out, nil
}

// BackfillChainRow stamps chain fields onto one legacy row. Idempotent:
// skips when chain_key is already set.
func (r *Repository) BackfillChainRow(ctx context.Context, p BackfillChainRowParams) error {
	const q = `
		UPDATE drug_operations_log
		   SET drug_name           = $3,
		       drug_strength       = $4,
		       drug_form           = $5,
		       chain_key           = $6,
		       entry_seq_in_chain  = $7,
		       prev_row_hash       = $8,
		       row_hash            = $9,
		       retention_until     = COALESCE(retention_until, $10)
		 WHERE id = $1
		   AND clinic_id = $2
		   AND chain_key IS NULL`
	if _, err := r.db.Exec(ctx, q,
		p.ID, p.ClinicID,
		nullIfEmpty(p.DrugName), nullIfEmpty(p.DrugStrength), nullIfEmpty(p.DrugForm),
		nullableBytes(p.ChainKey), p.EntrySeqInChain,
		nullableBytes(p.PrevRowHash), nullableBytes(p.RowHash),
		p.RetentionUntil,
	); err != nil {
		return fmt.Errorf("drugs.repo.BackfillChainRow: %w", err)
	}
	return nil
}

// ChainStatus is the result of repo.VerifyChain.
type ChainStatus struct {
	Intact            bool
	Length            int64
	FirstBrokenSeq    *int64
	FirstBrokenReason string
}

// VerifyChain reads every row in (clinic, chain_key) ordered by
// entry_seq_in_chain and recomputes the canonical hash for each. The
// first row whose stored row_hash differs from the recomputed hash is
// reported as broken; the walk stops there.
//
// A chain with zero rows is considered Intact (length 0, no row to
// fail). Sequence gaps (e.g. 1, 2, 4) are reported as broken at the
// first missing seq.
func (r *Repository) VerifyChain(ctx context.Context, clinicID uuid.UUID, chainK []byte) (*ChainStatus, error) {
	const q = `
		SELECT id, entry_seq_in_chain, operation, quantity, unit,
		       drug_name, drug_strength, drug_form, balance_after,
		       prev_row_hash, row_hash
		  FROM drug_operations_log
		 WHERE clinic_id = $1
		   AND chain_key = $2
		   AND entry_seq_in_chain IS NOT NULL
		 ORDER BY entry_seq_in_chain ASC`
	rows, err := r.db.Query(ctx, q, clinicID, chainK)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.VerifyChain: query: %w", err)
	}
	defer rows.Close()

	status := &ChainStatus{Intact: true}
	prevHash := ZeroHash()
	expectedSeq := int64(1)

	for rows.Next() {
		var (
			id                              uuid.UUID
			seq                             int64
			operation, unit                 string
			drugName, strength, form        string
			quantity, balanceAfter          float64
			storedPrevHash, storedRowHash   []byte
		)
		if err := rows.Scan(&id, &seq, &operation, &quantity, &unit,
			&drugName, &strength, &form, &balanceAfter,
			&storedPrevHash, &storedRowHash); err != nil {
			return nil, fmt.Errorf("drugs.repo.VerifyChain: scan: %w", err)
		}
		status.Length++

		if seq != expectedSeq {
			status.Intact = false
			missingSeq := expectedSeq
			status.FirstBrokenSeq = &missingSeq
			status.FirstBrokenReason = fmt.Sprintf("sequence gap at %d (next row was %d)", expectedSeq, seq)
			return status, nil
		}

		// Stored prev_row_hash must match the running prevHash we're
		// carrying forward — catches reorderings.
		if !bytes.Equal(storedPrevHash, prevHash) {
			status.Intact = false
			status.FirstBrokenSeq = &seq
			status.FirstBrokenReason = "stored prev_row_hash does not match the previous row's row_hash"
			return status, nil
		}

		canonical := canonicalRowBytes(
			id, clinicID, chainK, seq,
			operation, quantity, unit,
			drugName, strength, form,
			balanceAfter, prevHash,
		)
		recomputed := computeRowHash(canonical, prevHash)
		if !bytes.Equal(recomputed, storedRowHash) {
			status.Intact = false
			status.FirstBrokenSeq = &seq
			status.FirstBrokenReason = "stored row_hash does not match recomputed hash"
			return status, nil
		}

		prevHash = storedRowHash
		expectedSeq++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drugs.repo.VerifyChain: rows: %w", err)
	}
	return status, nil
}

// HasOpenReconciliation reports whether there's already an unreported
// reconciliation row for the (shelf, period_end) pair. Used by the service
// layer to prevent duplicate concurrent reconciliations.
func (r *Repository) HasOpenReconciliation(ctx context.Context, shelfID uuid.UUID, periodEnd time.Time) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1 FROM drug_reconciliation
			WHERE shelf_id = $1 AND period_end = $2
		)`
	var exists bool
	if err := r.db.QueryRow(ctx, q, shelfID, periodEnd).Scan(&exists); err != nil {
		return false, fmt.Errorf("drugs.repo.HasOpenReconciliation: %w", err)
	}
	return exists, nil
}
