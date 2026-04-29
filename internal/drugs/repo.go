package drugs

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// repo is the internal data-access interface for the drugs module. The
// concrete implementation is in repository.go; tests use fakeRepo
// (fake_repo_test.go).
//
// Method order mirrors the lifecycle: override drugs → shelf → operations
// → reconciliation. Every method takes clinic_id (or implicitly enforces it
// via the joined entity) — multi-tenancy is non-negotiable.
type repo interface {
	// ── Override drugs (clinic-defined, when system catalog doesn't cover) ─

	CreateOverrideDrug(ctx context.Context, p CreateOverrideDrugParams) (*OverrideDrugRecord, error)
	GetOverrideDrugByID(ctx context.Context, id, clinicID uuid.UUID) (*OverrideDrugRecord, error)
	ListOverrideDrugs(ctx context.Context, clinicID uuid.UUID) ([]*OverrideDrugRecord, error)
	UpdateOverrideDrug(ctx context.Context, p UpdateOverrideDrugParams) (*OverrideDrugRecord, error)
	ArchiveOverrideDrug(ctx context.Context, id, clinicID uuid.UUID) error

	// ── Shelf (clinic inventory) ────────────────────────────────────────────

	CreateShelfEntry(ctx context.Context, p CreateShelfEntryParams) (*ShelfRecord, error)
	GetShelfEntryByID(ctx context.Context, id, clinicID uuid.UUID) (*ShelfRecord, error)
	ListShelfEntries(ctx context.Context, clinicID uuid.UUID, p ListShelfParams) ([]*ShelfRecord, int, error)
	UpdateShelfMeta(ctx context.Context, p UpdateShelfMetaParams) (*ShelfRecord, error)
	ArchiveShelfEntry(ctx context.Context, id, clinicID uuid.UUID) error

	// ── Operations (append-only ledger) ─────────────────────────────────────

	// LogOperation MUST be transactional — it inserts the ops row AND updates
	// clinic_drug_shelf.balance atomically, with a FOR UPDATE lock on the
	// shelf row to detect concurrent balance changes.
	LogOperation(ctx context.Context, p CreateOperationParams) (*OperationRecord, error)
	GetOperationByID(ctx context.Context, id, clinicID uuid.UUID) (*OperationRecord, error)
	ListOperations(ctx context.Context, clinicID uuid.UUID, p ListOperationsParams) ([]*OperationRecord, int, error)
	// SumLedgerForShelfPeriod returns the net balance change over a period
	// (receive adds, administer/dispense/discard subtracts, transfer is 0).
	// Used by the reconciliation flow to compute expected ledger_count.
	SumLedgerForShelfPeriod(ctx context.Context, shelfID, clinicID uuid.UUID, periodStart, periodEnd time.Time) (float64, error)

	// ── Reconciliation (period-close + discrepancy resolution) ──────────────

	CreateReconciliation(ctx context.Context, p CreateReconciliationParams) (*ReconciliationRecord, error)
	GetReconciliationByID(ctx context.Context, id, clinicID uuid.UUID) (*ReconciliationRecord, error)
	UpdateReconciliationStatus(ctx context.Context, p UpdateReconciliationStatusParams) (*ReconciliationRecord, error)
	// LockOperationsToReconciliation sets reconciliation_id on every ops row
	// in (shelf, period), making them immutable. Returns rows affected.
	LockOperationsToReconciliation(ctx context.Context, p LockOperationsParams) (int64, error)
	ListReconciliations(ctx context.Context, clinicID uuid.UUID, p ListReconciliationsParams) ([]*ReconciliationRecord, int, error)
	HasOpenReconciliation(ctx context.Context, shelfID uuid.UUID, periodEnd time.Time) (bool, error)
}
