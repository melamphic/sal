// Package mar implements the aged-care Medication Administration
// Record sub-module.
//
// Why a separate module from drugs:
//   - Most MAR rows are NON-controlled (paracetamol, vitamins,
//     laxatives) — they don't belong in drug_operations_log.
//   - The unit of work is "scheduled dose per resident per slot",
//     not "operation against a shelf row".
//   - The outcome is a 14-option enum, not a 6-option op type.
//
// When a MAR administration event involves a controlled drug, the MAR
// service calls drugs.Service.LogOperationTx (Phase 3b) so a parallel
// row lands in drug_operations_log atomically. The MAR event row
// carries drug_op_id back to that ledger row.
//
// Design: docs/drug-register-compliance-v2.md §4.6 + §5.6
package mar

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// repo is the internal data-access interface for the MAR module.
//
// Method order mirrors lifecycle: prescription → scheduled doses
// (generated nightly) → administration event → round (UI grouping).
// Tenancy: every method takes clinic_id (or implicitly enforces it
// through a parent FK).
type repo interface {
	// ── Prescriptions ─────────────────────────────────────────────────────

	CreatePrescription(ctx context.Context, p CreatePrescriptionParams) (*PrescriptionRecord, error)
	GetPrescription(ctx context.Context, id, clinicID uuid.UUID) (*PrescriptionRecord, error)
	ListPrescriptionsForResident(ctx context.Context, clinicID, residentID uuid.UUID, includeArchived bool) ([]*PrescriptionRecord, error)
	UpdatePrescription(ctx context.Context, p UpdatePrescriptionParams) (*PrescriptionRecord, error)
	ArchivePrescription(ctx context.Context, id, clinicID uuid.UUID) error

	// ── Scheduled doses ───────────────────────────────────────────────────

	CreateScheduledDose(ctx context.Context, p CreateScheduledDoseParams) (*ScheduledDoseRecord, error)
	ListDueScheduledDoses(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]*ScheduledDoseRecord, error)

	// ── Rounds ────────────────────────────────────────────────────────────

	CreateRound(ctx context.Context, p CreateRoundParams) (*RoundRecord, error)
	CompleteRound(ctx context.Context, id, clinicID uuid.UUID, completedAt time.Time) error
	ListRecentRounds(ctx context.Context, clinicID uuid.UUID, limit int) ([]*RoundRecord, error)

	// ── Administration events (the legal record) ──────────────────────────

	// CreateAdminEvent inserts one event row + (when prescription is
	// controlled) a parallel drug_operations_log row in the same
	// transaction. The cross-domain wiring lives in service.go via
	// the DrugLedgerWriter port.
	CreateAdminEvent(ctx context.Context, p CreateAdminEventParams) (*AdminEventRecord, error)
	GetAdminEvent(ctx context.Context, id, clinicID uuid.UUID) (*AdminEventRecord, error)
	ListAdminEventsForResident(ctx context.Context, clinicID, residentID uuid.UUID, from, to time.Time) ([]*AdminEventRecord, error)
}
