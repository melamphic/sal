package drugs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/drugs/catalog"
	"github.com/melamphic/sal/internal/drugs/validators"
)

// ── Service dependencies (small interfaces, satisfied by app.go adapters) ──

// ClinicLookup resolves vertical + country for a clinic — needed to scope
// the system catalog and to drive witnessing rules. Implemented by app.go
// via a thin wrapper over clinic.Service.
//
// GetClinicState is optional: AU clinics need it for state-aware
// validation (NSW/VIC/QLD/WA/...); other countries return empty
// string. Adapters that don't carry state may always return "" — the
// AU validator falls back to WA-strict, which is the safe default for
// shadow mode.
type ClinicLookup interface {
	GetVerticalAndCountry(ctx context.Context, clinicID uuid.UUID) (vertical, country string, err error)
	GetClinicState(ctx context.Context, clinicID uuid.UUID) (state string, err error)
}

// StaffPermLookup checks runtime perm flags for a staff member. The drugs
// service needs this for witness validation (witness must hold
// perm_witness_controlled_drugs). Implemented over staff.Service.
type StaffPermLookup interface {
	HasPermission(ctx context.Context, staffID, clinicID uuid.UUID, permName string) (bool, error)
}

// SubjectAccessLogger writes subject_access_log rows on every PII read or
// drug-history view. Implemented by patient.Repository (or any equivalent
// adapter). The "purpose" string is regulator-meaningful — pass something
// concrete like "drugs.history.subject_view" for traceability.
type SubjectAccessLogger interface {
	LogAccess(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, action, purpose string) error
}

// ── Public types — service input/output shapes ────────────────────────────

// CatalogResponse is the view a clinic gets when browsing the drug catalog
// for its (vertical, country). Combines the system master entries with the
// clinic's custom overrides.
//
//nolint:revive
type CatalogResponse struct {
	Vertical  string                    `json:"vertical"`
	Country   string                    `json:"country"`
	System    []CatalogEntryResponse    `json:"system"`
	Overrides []OverrideDrugResponse    `json:"overrides"`
}

// CatalogEntryResponse mirrors catalog.Entry on the wire.
//
//nolint:revive
type CatalogEntryResponse struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	ActiveIngredient  string            `json:"active_ingredient,omitempty"`
	Schedule          string            `json:"schedule"`
	RegulatoryClass   string            `json:"regulatory_class,omitempty"`
	CommonStrengths   []string          `json:"common_strengths,omitempty"`
	Form              string            `json:"form"`
	CommonRoutes      []string          `json:"common_routes,omitempty"`
	CommonDoses       map[string]string `json:"common_doses,omitempty"`
	BrandNames        []string          `json:"brand_names,omitempty"`
	DefaultUnit       string            `json:"default_unit"`
	Notes             string            `json:"notes,omitempty"`
	IsControlled      bool              `json:"is_controlled"`
	WitnessRequired   bool              `json:"witness_required"`
	WitnessesNeeded   int               `json:"witnesses_needed,omitempty"`
}

// OverrideDrugResponse — clinic-defined drug entry returned to clients.
//
//nolint:revive
type OverrideDrugResponse struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	ActiveIngredient *string `json:"active_ingredient,omitempty"`
	Schedule         *string `json:"schedule,omitempty"`
	Strength         *string `json:"strength,omitempty"`
	Form             *string `json:"form,omitempty"`
	BrandName        *string `json:"brand_name,omitempty"`
	Notes            *string `json:"notes,omitempty"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

// CreateOverrideDrugInput — service input for creating a clinic override.
type CreateOverrideDrugInput struct {
	ClinicID         uuid.UUID
	StaffID          uuid.UUID
	Name             string
	ActiveIngredient *string
	Schedule         *string
	Strength         *string
	Form             *string
	BrandName        *string
	Notes            *string
}

// UpdateOverrideDrugInput — metadata-only update.
type UpdateOverrideDrugInput struct {
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

// ShelfResponse — one shelf entry on the wire.
//
//nolint:revive
type ShelfResponse struct {
	ID             string  `json:"id"`
	CatalogID      *string `json:"catalog_id,omitempty"`
	OverrideDrugID *string `json:"override_drug_id,omitempty"`
	// DisplayName — human-readable drug name derived from the catalog
	// entry (system) or override row (clinic). Populated by the
	// service so the FE doesn't need a separate catalog round-trip
	// per shelf row. Falls back to nil only when the catalog/override
	// lookup misses (catalog drift, archived override, etc).
	DisplayName    *string `json:"display_name,omitempty"`
	Strength       *string `json:"strength,omitempty"`
	Form           *string `json:"form,omitempty"`
	BatchNumber    *string `json:"batch_number,omitempty"`
	ExpiryDate     *string `json:"expiry_date,omitempty"`
	Location       string  `json:"location"`
	Balance        float64 `json:"balance"`
	Unit           string  `json:"unit"`
	ParLevel       *float64 `json:"par_level,omitempty"`
	Notes          *string `json:"notes,omitempty"`
	// WitnessRequired tells the FE whether this drug requires a
	// witness on administer/dispense/discard/transfer (per the
	// catalog or override schedule). Populated by shelfWithName /
	// shelfWithNameCached when the shelf is hydrated for client use.
	// The cart UI uses this to gate the witness picker — controlled
	// lines need a witness, uncontrolled lines don't.
	WitnessRequired bool   `json:"witness_required"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ArchivedAt     *string `json:"archived_at,omitempty"`
}

// ShelfListResponse — paginated shelf listing.
//
//nolint:revive
type ShelfListResponse struct {
	Items  []*ShelfResponse `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// CreateShelfInput — admit a new (drug × strength × batch × location)
// onto the shelf with an opening balance.
type CreateShelfInput struct {
	ClinicID       uuid.UUID
	StaffID        uuid.UUID
	CatalogID      *string
	OverrideDrugID *uuid.UUID
	Strength       *string
	Form           *string
	BatchNumber    *string
	ExpiryDate     *time.Time
	Location       string
	OpeningBalance float64
	Unit           string
	ParLevel       *float64
	Notes          *string
}

// UpdateShelfMetaInput — non-balance updates.
type UpdateShelfMetaInput struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	Location string
	ParLevel *float64
	Notes    *string
}

// ListShelfInput — filters for the shelf listing.
type ListShelfInput struct {
	Limit           int
	Offset          int
	IncludeArchived bool
	Location        *string
	Search          *string
}

// LogOperationInput — the workhorse. Service computes balance_before /
// balance_after inside the txn from the pre-read shelf row.
type LogOperationInput struct {
	ClinicID         uuid.UUID
	StaffID          uuid.UUID // who's logging it (== AdministeredBy unless explicit)
	ShelfID          uuid.UUID
	SubjectID        *uuid.UUID
	NoteID           *uuid.UUID
	NoteFieldID      *uuid.UUID
	Operation        string
	Quantity         float64
	Unit             string
	Dose             *string
	Route            *string
	ReasonIndication *string
	AdministeredBy   uuid.UUID
	WitnessedBy      *uuid.UUID
	PrescribedBy     *uuid.UUID
	AddendsTo        *uuid.UUID
	// Status — 'confirmed' (default) for explicit user actions; pass
	// 'pending_confirm' from auto-materialisation flows that require a
	// downstream user tap (e.g. AI auto-creating drug ops without
	// review). The submit gate refuses to ship a note with any
	// pending_confirm rows linked to it.
	Status string

	// WitnessKind selects how the witness was/will-be captured. nil or
	// empty falls back to 'staff' (legacy default). Values:
	//   - "staff"    — concurrent internal witness (WitnessedBy set)
	//   - "pending"  — async sign-off; queue another qualified colleague
	//                  via the approvals service. WitnessedBy nil at
	//                  log time; populated on Approve.
	//   - "external" — concurrent paper-trail witness (non-Salvia user).
	//                  ExternalWitnessName/Role + WitnessAttestation set.
	//   - "self"     — emergency / solo. Attestation required; flagged
	//                  on regulator report. FORBIDDEN for 'discard' op
	//                  per UK S2 / AU S8 destruction rules.
	WitnessKind *string

	// Sync external-witness fields (non-Salvia user present at op).
	ExternalWitnessName *string
	ExternalWitnessRole *string
	// Free-text justification. Required (≥10 chars) for external mode,
	// (≥30 chars) for self mode.
	WitnessAttestation *string
	// Optional note left for the async approver — surfaces in their
	// queue card so they have context without opening the note.
	WitnessNote *string

	// Compliance — optional jurisdiction-specific fields populated by
	// callers that have them (UK Sch 6, US 1304, NZ Reg 40, AU state).
	// Phase 2a (shadow mode): validators inspect these and log
	// findings; the operation always proceeds. Phase 2c flips to
	// enforce mode for clinics on compliance_v2.
	Compliance validators.ComplianceInput
}

// OperationResponse — one ledger row on the wire.
//
//nolint:revive
type OperationResponse struct {
	ID                string  `json:"id"`
	ShelfID           string  `json:"shelf_id"`
	SubjectID         *string `json:"subject_id,omitempty"`
	NoteID            *string `json:"note_id,omitempty"`
	NoteFieldID       *string `json:"note_field_id,omitempty"`
	Operation         string  `json:"operation"`
	Quantity          float64 `json:"quantity"`
	Unit              string  `json:"unit"`
	Dose              *string `json:"dose,omitempty"`
	Route             *string `json:"route,omitempty"`
	ReasonIndication  *string `json:"reason_indication,omitempty"`
	AdministeredBy    string  `json:"administered_by"`
	WitnessedBy       *string `json:"witnessed_by,omitempty"`
	PrescribedBy      *string `json:"prescribed_by,omitempty"`
	BalanceBefore     float64 `json:"balance_before"`
	BalanceAfter      float64 `json:"balance_after"`
	ReconciliationID  *string `json:"reconciliation_id,omitempty"`
	AddendsTo         *string `json:"addends_to,omitempty"`
	// Status — 'pending_confirm' until a clinician confirms (only set
	// for system.drug_op widget creates). Always 'confirmed' for ops
	// created via the manual modal.
	Status      string  `json:"status"`
	ConfirmedBy *string `json:"confirmed_by,omitempty"`
	ConfirmedAt *string `json:"confirmed_at,omitempty"`

	// Witness shape — added with the async-approval flow.
	// WitnessKind is nil while a pending row awaits sign-off.
	// WitnessStatus is the snapshot of the latest approval row
	// (not_required | pending | approved | challenged).
	WitnessKind         *string `json:"witness_kind,omitempty"`
	ExternalWitnessName *string `json:"external_witness_name,omitempty"`
	ExternalWitnessRole *string `json:"external_witness_role,omitempty"`
	WitnessAttestation  *string `json:"witness_attestation,omitempty"`
	WitnessStatus       *string `json:"witness_status,omitempty"`

	CreatedAt string `json:"created_at"`
}

// OperationListResponse — paginated ledger.
//
//nolint:revive
type OperationListResponse struct {
	Items  []*OperationResponse `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

// ListOperationsInput — filters.
type ListOperationsInput struct {
	Limit            int
	Offset           int
	ShelfID          *uuid.UUID
	SubjectID        *uuid.UUID
	NoteID           *uuid.UUID
	Operation        *string
	Since            *time.Time
	Until            *time.Time
	OnlyPendingRecon bool
}

// StartReconciliationInput — initiate a (shelf, period) reconciliation.
// Service computes ledger_count from the operations log and persists the
// physical_count + ledger_count + a clean/discrepancy verdict in one shot.
// Two-staff signoff happens via a follow-up SecondarySign call.
type StartReconciliationInput struct {
	ClinicID               uuid.UUID
	StaffID                uuid.UUID
	ShelfID                uuid.UUID
	PeriodStart            time.Time
	PeriodEnd              time.Time
	PhysicalCount          float64
	DiscrepancyExplanation *string
}

// SecondarySignReconciliationInput — secondary staff member signs.
type SecondarySignReconciliationInput struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID // becomes reconciled_by_secondary
}

// ReportReconciliationInput — privacy officer marks discrepancy as reported
// to regulator (VCNZ / state poisons authority).
type ReportReconciliationInput struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID // becomes reported_by
}

// ReconciliationResponse — one reconciliation row on the wire.
//
//nolint:revive
type ReconciliationResponse struct {
	ID                       string   `json:"id"`
	ShelfID                  string   `json:"shelf_id"`
	PeriodStart              string   `json:"period_start"`
	PeriodEnd                string   `json:"period_end"`
	PhysicalCount            float64  `json:"physical_count"`
	LedgerCount              float64  `json:"ledger_count"`
	Discrepancy              float64  `json:"discrepancy"`
	ReconciledByPrimary      string   `json:"reconciled_by_primary"`
	ReconciledBySecondary    *string  `json:"reconciled_by_secondary,omitempty"`
	Status                   string   `json:"status"`
	DiscrepancyExplanation   *string  `json:"discrepancy_explanation,omitempty"`
	ReportedAt               *string  `json:"reported_at,omitempty"`
	ReportedBy               *string  `json:"reported_by,omitempty"`
	CreatedAt                string   `json:"created_at"`
}

// ReconciliationListResponse — paginated.
//
//nolint:revive
type ReconciliationListResponse struct {
	Items  []*ReconciliationResponse `json:"items"`
	Total  int                       `json:"total"`
	Limit  int                       `json:"limit"`
	Offset int                       `json:"offset"`
}

// ListReconciliationsInput — filters.
type ListReconciliationsInput struct {
	Limit   int
	Offset  int
	ShelfID *uuid.UUID
	Status  *string
	Since   *time.Time
	Until   *time.Time
}

// ── Service ───────────────────────────────────────────────────────────────────

// ApprovalSubmitter is the small surface drugs.Service consumes from
// the approvals package. Wired by app.go so drug ops with
// witness_kind=pending get an async second-pair-of-eyes row created
// alongside the ledger entry.
//
// Pre-set during Build(); a nil ApprovalSubmitter disables the async
// path entirely (LogOperation rejects pending mode with ErrValidation).
type ApprovalSubmitter interface {
	Submit(ctx context.Context, in ApprovalSubmitInput) error
}

// ApprovalSubmitInput captures the bits the approvals service needs.
// Mirrors approvals.SubmitInput but lives in the drugs package so
// drugs doesn't import approvals (cross-domain hygiene).
type ApprovalSubmitInput struct {
	ClinicID    uuid.UUID
	EntityID    uuid.UUID
	EntityOp    *string
	SubmittedBy uuid.UUID
	StaffRole   string
	Note        *string
	SubjectID   *uuid.UUID
	NoteID      *uuid.UUID
}

// Service is the drugs business-logic layer. Concurrency-safe; can be
// shared across handlers without locking.
type Service struct {
	repo         repo
	cat          *catalog.Loader
	clinics      ClinicLookup
	staffPerms   StaffPermLookup
	accessLogger SubjectAccessLogger
	approvals    ApprovalSubmitter
}

// NewService constructs the drugs Service.
//
// catalog is required (no fallback — calls fail without it). accessLogger
// is required for compliance traceability of subject reads. staffPerms is
// required for witness validation. clinics is required to scope the
// catalog.
func NewService(
	r repo,
	cat *catalog.Loader,
	clinics ClinicLookup,
	staffPerms StaffPermLookup,
	accessLogger SubjectAccessLogger,
) *Service {
	return &Service{
		repo:         r,
		cat:          cat,
		clinics:      clinics,
		staffPerms:   staffPerms,
		accessLogger: accessLogger,
	}
}

// SetApprovals wires the second-pair-of-eyes submitter. nil disables
// the async path so LogOperation will reject witness_kind=pending.
func (s *Service) SetApprovals(a ApprovalSubmitter) {
	s.approvals = a
}

// UpdateWitnessStatus flips the witness_status snapshot column on a
// drug_operations_log row. Invoked by the approvals service callback
// when a pending row transitions to approved / challenged. Idempotent.
func (s *Service) UpdateWitnessStatus(ctx context.Context, opID, clinicID uuid.UUID, status domain.EntityReviewStatus) error {
	if err := s.repo.UpdateWitnessStatus(ctx, opID, clinicID, status); err != nil {
		return fmt.Errorf("drugs.service.UpdateWitnessStatus: %w", err)
	}
	return nil
}

// ── Catalog read methods ─────────────────────────────────────────────────────

// ListCatalog returns the merged system catalog + clinic overrides for the
// clinic's vertical and country. Authentication / RBAC happens at the
// handler layer; this method only enforces tenancy via clinic.VerticalAndCountry.
func (s *Service) ListCatalog(ctx context.Context, clinicID uuid.UUID) (*CatalogResponse, error) {
	vertical, country, err := s.clinics.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListCatalog: clinic lookup: %w", err)
	}

	systemEntries := s.cat.Entries(vertical, country)
	systemOut := make([]CatalogEntryResponse, len(systemEntries))
	for i, e := range systemEntries {
		systemOut[i] = catalogEntryToResponse(e)
	}

	overrides, err := s.repo.ListOverrideDrugs(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListCatalog: overrides: %w", err)
	}
	overrideOut := make([]OverrideDrugResponse, len(overrides))
	for i, o := range overrides {
		overrideOut[i] = *overrideRecordToResponse(o)
	}

	return &CatalogResponse{
		Vertical:  vertical,
		Country:   country,
		System:    systemOut,
		Overrides: overrideOut,
	}, nil
}

// LookupCatalogEntry returns the system entry by ID for the clinic's
// vertical/country. Returns ErrNotFound when the ID isn't in the
// system catalog (does NOT search overrides — callers needing override
// detail should use GetOverrideDrug).
func (s *Service) LookupCatalogEntry(ctx context.Context, clinicID uuid.UUID, entryID string) (*CatalogEntryResponse, error) {
	vertical, country, err := s.clinics.GetVerticalAndCountry(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LookupCatalogEntry: clinic lookup: %w", err)
	}
	e := s.cat.Lookup(vertical, country, entryID)
	if e == nil {
		return nil, fmt.Errorf("drugs.service.LookupCatalogEntry: %w", domain.ErrNotFound)
	}
	resp := catalogEntryToResponse(*e)
	return &resp, nil
}

// ── Override drug methods ────────────────────────────────────────────────────

// CreateOverrideDrug inserts a clinic-defined custom drug.
func (s *Service) CreateOverrideDrug(ctx context.Context, in CreateOverrideDrugInput) (*OverrideDrugResponse, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("drugs.service.CreateOverrideDrug: name required: %w", domain.ErrValidation)
	}
	rec, err := s.repo.CreateOverrideDrug(ctx, CreateOverrideDrugParams{
		ID:               domain.NewID(),
		ClinicID:         in.ClinicID,
		Name:             in.Name,
		ActiveIngredient: in.ActiveIngredient,
		Schedule:         in.Schedule,
		Strength:         in.Strength,
		Form:             in.Form,
		BrandName:        in.BrandName,
		Notes:            in.Notes,
		CreatedBy:        in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.CreateOverrideDrug: %w", err)
	}
	return overrideRecordToResponse(rec), nil
}

// UpdateOverrideDrug updates metadata.
func (s *Service) UpdateOverrideDrug(ctx context.Context, in UpdateOverrideDrugInput) (*OverrideDrugResponse, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("drugs.service.UpdateOverrideDrug: name required: %w", domain.ErrValidation)
	}
	rec, err := s.repo.UpdateOverrideDrug(ctx, UpdateOverrideDrugParams(in))
	if err != nil {
		return nil, fmt.Errorf("drugs.service.UpdateOverrideDrug: %w", err)
	}
	return overrideRecordToResponse(rec), nil
}

// ArchiveOverrideDrug soft-deletes.
func (s *Service) ArchiveOverrideDrug(ctx context.Context, id, clinicID uuid.UUID) error {
	if err := s.repo.ArchiveOverrideDrug(ctx, id, clinicID); err != nil {
		return fmt.Errorf("drugs.service.ArchiveOverrideDrug: %w", err)
	}
	return nil
}

// ── Shelf methods ────────────────────────────────────────────────────────────

// CreateShelfEntry adds (drug × strength × batch × location) to the shelf
// with an opening balance. The opening balance MUST also be logged as a
// 'receive' operation in the same transaction so the ledger reflects how
// the stock got there — this enforces ledger completeness.
func (s *Service) CreateShelfEntry(ctx context.Context, in CreateShelfInput) (*ShelfResponse, error) {
	if (in.CatalogID == nil) == (in.OverrideDrugID == nil) {
		return nil, fmt.Errorf("drugs.service.CreateShelfEntry: exactly one of catalog_id / override_drug_id required: %w", domain.ErrValidation)
	}
	if in.OpeningBalance < 0 {
		return nil, fmt.Errorf("drugs.service.CreateShelfEntry: opening balance must be >= 0: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Unit) == "" {
		return nil, fmt.Errorf("drugs.service.CreateShelfEntry: unit required: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Location) == "" {
		in.Location = "main"
	}

	rec, err := s.repo.CreateShelfEntry(ctx, CreateShelfEntryParams{
		ID:             domain.NewID(),
		ClinicID:       in.ClinicID,
		CatalogID:      in.CatalogID,
		OverrideDrugID: in.OverrideDrugID,
		Strength:       in.Strength,
		Form:           in.Form,
		BatchNumber:    in.BatchNumber,
		ExpiryDate:     in.ExpiryDate,
		Location:       in.Location,
		Balance:        in.OpeningBalance,
		Unit:           in.Unit,
		ParLevel:       in.ParLevel,
		Notes:          in.Notes,
		CreatedBy:      in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.CreateShelfEntry: %w", err)
	}

	// Log the opening balance as a 'receive' op so the ledger is complete.
	// We don't fail the whole operation if this auxiliary log fails — the
	// shelf row exists, the user can retry the receive op manually. We do
	// surface it as a service-side warning via the returned response (no
	// dedicated mechanism today; logged at slog.Error in the repo error path).
	if in.OpeningBalance > 0 {
		_, _ = s.repo.LogOperation(ctx, CreateOperationParams{
			ID:             domain.NewID(),
			ClinicID:       in.ClinicID,
			ShelfID:        rec.ID,
			Operation:      "receive",
			Quantity:       in.OpeningBalance,
			Unit:           in.Unit,
			AdministeredBy: in.StaffID, // 'received_by' semantically
			BalanceBefore:  0,
			BalanceAfter:   in.OpeningBalance,
		})
	}

	return s.shelfWithName(ctx, in.ClinicID, rec), nil
}

// GetShelfEntry returns one shelf row. Tenant-scoped.
func (s *Service) GetShelfEntry(ctx context.Context, id, clinicID uuid.UUID) (*ShelfResponse, error) {
	rec, err := s.repo.GetShelfEntryByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.GetShelfEntry: %w", err)
	}
	return s.shelfWithName(ctx, clinicID, rec), nil
}

// ListShelfEntries — paginated. Resolves a human display name per
// row by joining against the in-memory catalog (system entries) and
// the override table (clinic-specific). Without this the FE would
// render the raw catalog id like `vet.NZ.meloxicam.injectable.5mgml`
// — useful as a stable key, useless as a label.
func (s *Service) ListShelfEntries(ctx context.Context, clinicID uuid.UUID, in ListShelfInput) (*ShelfListResponse, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	recs, total, err := s.repo.ListShelfEntries(ctx, clinicID, ListShelfParams(in))
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListShelfEntries: %w", err)
	}
	// Pre-fetch override drug names in one query so we don't N+1.
	overrideNames, err := s.collectOverrideNames(ctx, clinicID, recs)
	if err != nil {
		// Fall back to nameless responses; FE knows how to render the
		// catalog id as a last resort.
		overrideNames = nil
	}
	out := make([]*ShelfResponse, len(recs))
	for i, r := range recs {
		out[i] = s.shelfWithNameCached(ctx, clinicID, r, overrideNames)
	}
	return &ShelfListResponse{Items: out, Total: total, Limit: in.Limit, Offset: in.Offset}, nil
}

// UpdateShelfMeta — non-balance updates.
func (s *Service) UpdateShelfMeta(ctx context.Context, in UpdateShelfMetaInput) (*ShelfResponse, error) {
	rec, err := s.repo.UpdateShelfMeta(ctx, UpdateShelfMetaParams(in))
	if err != nil {
		return nil, fmt.Errorf("drugs.service.UpdateShelfMeta: %w", err)
	}
	return s.shelfWithName(ctx, in.ClinicID, rec), nil
}

// shelfWithName resolves the catalog/override display name for a
// single shelf row. Cheap for catalog (in-memory map); for overrides
// it costs one DB lookup — fine for single-row paths (Get / Update /
// Create). For lists, use the cached helper instead.
func (s *Service) shelfWithName(ctx context.Context, clinicID uuid.UUID, r *ShelfRecord) *ShelfResponse {
	resp := shelfRecordToResponse(r)
	resp.DisplayName = s.lookupShelfName(ctx, clinicID, r, nil)
	if rw, err := s.shelfRequiresWitness(ctx, clinicID, r); err == nil {
		resp.WitnessRequired = rw
	}
	return resp
}

func (s *Service) shelfWithNameCached(ctx context.Context, clinicID uuid.UUID, r *ShelfRecord, overrideNames map[uuid.UUID]string) *ShelfResponse {
	resp := shelfRecordToResponse(r)
	resp.DisplayName = s.lookupShelfName(ctx, clinicID, r, overrideNames)
	if rw, err := s.shelfRequiresWitness(ctx, clinicID, r); err == nil {
		resp.WitnessRequired = rw
	}
	return resp
}

// lookupShelfName returns the human name for a shelf row by joining
// the catalog (in-memory) or the override table (cached map). nil
// when neither resolves — rare, only on catalog drift / archived
// override.
func (s *Service) lookupShelfName(ctx context.Context, clinicID uuid.UUID, r *ShelfRecord, overrideNames map[uuid.UUID]string) *string {
	if r.CatalogID != nil && s.cat != nil {
		// Catalog Lookup is in-memory; on miss it just returns nil.
		vertical, country := "", ""
		if v, c, err := s.clinics.GetVerticalAndCountry(ctx, clinicID); err == nil {
			vertical, country = v, c
		}
		if entry := s.cat.Lookup(vertical, country, *r.CatalogID); entry != nil {
			n := entry.Name
			return &n
		}
	}
	if r.OverrideDrugID != nil {
		if overrideNames != nil {
			if name, ok := overrideNames[*r.OverrideDrugID]; ok {
				return &name
			}
		}
		// Single-row path — fall back to one direct lookup.
		if rec, err := s.repo.GetOverrideDrugByID(ctx, *r.OverrideDrugID, clinicID); err == nil {
			n := rec.Name
			return &n
		}
	}
	return nil
}

// collectOverrideNames bulk-loads the override drug names referenced by
// any of the shelf rows, returning a map id→name. One query (via
// repo.ListOverrideDrugs) — small N (clinic overrides are dozens at
// most), so listing all and indexing in Go is cheaper than a per-row
// fetch.
func (s *Service) collectOverrideNames(ctx context.Context, clinicID uuid.UUID, recs []*ShelfRecord) (map[uuid.UUID]string, error) {
	any := false
	for _, r := range recs {
		if r.OverrideDrugID != nil {
			any = true
			break
		}
	}
	if !any {
		return nil, nil
	}
	all, err := s.repo.ListOverrideDrugs(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.collectOverrideNames: %w", err)
	}
	out := make(map[uuid.UUID]string, len(all))
	for _, o := range all {
		out[o.ID] = o.Name
	}
	return out, nil
}

// ArchiveShelfEntry — soft-delete.
func (s *Service) ArchiveShelfEntry(ctx context.Context, id, clinicID uuid.UUID) error {
	if err := s.repo.ArchiveShelfEntry(ctx, id, clinicID); err != nil {
		return fmt.Errorf("drugs.service.ArchiveShelfEntry: %w", err)
	}
	return nil
}

// ── Operations / ledger methods ──────────────────────────────────────────────

// LogOperation is the most consequential method in the module. It:
//
//   1. Loads the shelf row + balance (FOR UPDATE inside the txn).
//   2. Resolves the underlying drug (system catalog OR override) to get
//      controls metadata (witness required? register required?).
//   3. Validates witness presence + uniqueness + permission for controlled
//      drugs.
//   4. Computes balance_after based on operation kind:
//        receive  → balance + qty
//        admin/dispense/discard → balance - qty
//        transfer → no net change (TODO: split-row transfer in v2)
//        adjust   → directly to qty (caller passes target balance via Quantity?)
//   5. Calls repo.LogOperation which inserts ops row + updates shelf
//      balance atomically.
//   6. Logs subject access if subject_id is set (drugs administered/
//      dispensed are PII reads).
//
// Returns ErrConflict if a concurrent change moved the balance under us
// (the FOR UPDATE catches this).
func (s *Service) LogOperation(ctx context.Context, in LogOperationInput) (*OperationResponse, error) {
	if err := validateOperation(in); err != nil {
		return nil, err
	}

	shelf, err := s.repo.GetShelfEntryByID(ctx, in.ShelfID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: shelf: %w", err)
	}
	if shelf.ArchivedAt != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: shelf archived: %w", domain.ErrConflict)
	}

	requiresWitness, err := s.shelfRequiresWitness(ctx, in.ClinicID, shelf)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: %w", err)
	}

	// Compliance v2 shadow check — runs the per-country validator and
	// logs any findings. Operation is NOT blocked in shadow mode; we
	// gather telemetry to validate rule coverage against real traffic
	// before flipping to enforce. See docs/drug-register-compliance-v2.md
	// §3.8 + §5.1.
	s.runComplianceCheckShadow(ctx, in, shelf, requiresWitness)

	// Witness validation — four modes for controlled drugs:
	//   * staff (default)   — synchronous internal witness
	//   * pending           — async; queue another colleague via the
	//                         approvals service after this op lands
	//   * external          — synchronous paper-trail witness (non-Salvia)
	//   * self              — emergency / solo; attestation only, FLAGGED
	//                         on regulator export. Forbidden for 'discard'
	//                         (UK S2 / AU S8 / NZ MoH destruction rules).
	// Non-controlled drugs leave the witness fields empty regardless.
	witnessKind := ""
	if in.WitnessKind != nil {
		witnessKind = strings.TrimSpace(*in.WitnessKind)
	}
	if requiresWitness {
		switch witnessKind {
		case "", "staff":
			witnessKind = "staff"
			if in.WitnessedBy == nil {
				return nil, fmt.Errorf("drugs.service.LogOperation: witness required for controlled drug: %w", domain.ErrValidation)
			}
			if *in.WitnessedBy == in.AdministeredBy {
				return nil, fmt.Errorf("drugs.service.LogOperation: witness must differ from administering staff: %w", domain.ErrValidation)
			}
			ok, err := s.staffPerms.HasPermission(ctx, *in.WitnessedBy, in.ClinicID, "perm_witness_controlled_drugs")
			if err != nil {
				return nil, fmt.Errorf("drugs.service.LogOperation: witness perm lookup: %w", err)
			}
			if !ok {
				return nil, fmt.Errorf("drugs.service.LogOperation: witness lacks perm_witness_controlled_drugs: %w", domain.ErrForbidden)
			}
		case "pending":
			if s.approvals == nil {
				return nil, fmt.Errorf("drugs.service.LogOperation: async approvals not configured: %w", domain.ErrValidation)
			}
			// Pending mode logs the op now without a witness; the
			// approvals service creates a queue row after the ledger
			// insert succeeds (post-tx so a transient approvals
			// failure doesn't roll back the regulator-binding entry).
		case "external":
			if in.ExternalWitnessName == nil || strings.TrimSpace(*in.ExternalWitnessName) == "" {
				return nil, fmt.Errorf("drugs.service.LogOperation: external witness name required: %w", domain.ErrValidation)
			}
			if in.WitnessAttestation == nil || len(strings.TrimSpace(*in.WitnessAttestation)) < 10 {
				return nil, fmt.Errorf("drugs.service.LogOperation: external witness attestation too short: %w", domain.ErrValidation)
			}
		case "self":
			if in.Operation == "discard" {
				return nil, fmt.Errorf("drugs.service.LogOperation: self-witness not permitted for destruction; in-person witness required: %w", domain.ErrValidation)
			}
			if in.WitnessAttestation == nil || len(strings.TrimSpace(*in.WitnessAttestation)) < 30 {
				return nil, fmt.Errorf("drugs.service.LogOperation: self-witness attestation too short (min 30 chars): %w", domain.ErrValidation)
			}
		default:
			return nil, fmt.Errorf("drugs.service.LogOperation: unknown witness_kind %q: %w", witnessKind, domain.ErrValidation)
		}
	}

	balanceBefore := shelf.Balance
	balanceAfter, err := computeBalanceAfter(in.Operation, balanceBefore, in.Quantity)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: %w", err)
	}

	// Compliance v2 chain: page-identity + chain key for the new row.
	// Legacy callers (catalog drift, override drugs without metadata)
	// fall back to empty values; repo skips chain compute then.
	cc, err := s.loadCatalogContext(ctx, in.ClinicID, shelf)
	if err != nil {
		// Don't block the op on a catalog lookup failure — log and
		// proceed with empty page identity (repo will skip the chain).
		slog.Warn("drugs.service.LogOperation: catalog context load failed; chain disabled for this row",
			"clinic_id", in.ClinicID, "shelf_id", in.ShelfID, "error", err.Error())
		cc = &catalogContext{}
	}

	var chainK []byte
	if cc.DrugName != "" && cc.Strength != "" && cc.Form != "" {
		chainK = chainKey(in.ClinicID, cc.DrugName, cc.Strength, cc.Form)
	}

	// Compliance v2: derive retention floor from per-clinic policy.
	// Failure to fetch the policy is non-fatal — we'd rather log + leave
	// retention_until NULL (effectively keep-forever) than block the op.
	var retentionUntil *time.Time
	if pol, err := s.repo.GetRetentionPolicy(ctx, in.ClinicID); err == nil {
		ru := domainTimeNow().AddDate(pol.LedgerYears, 0, 0)
		retentionUntil = &ru
	} else if !errors.Is(err, domain.ErrNotFound) {
		slog.Warn("drugs.service.LogOperation: retention policy lookup failed; row keeps forever",
			"clinic_id", in.ClinicID, "error", err.Error())
	}

	// Map witness mode → snapshot status + persisted column shape.
	//   not_required: non-controlled drug — witness fields stay nil
	//   staff/external/self: synchronous; status='approved' on insert
	//   pending: async; status='pending', approval row created post-tx
	var witnessKindParam *string
	var witnessStatusParam *string
	{
		switch {
		case !requiresWitness:
			s := string(domain.EntityReviewNotRequired)
			witnessStatusParam = &s
		case witnessKind == "pending":
			s := string(domain.EntityReviewPending)
			witnessStatusParam = &s
			// witnessKindParam stays nil — populated when the
			// approver decides.
		default:
			k := witnessKind
			witnessKindParam = &k
			s := string(domain.EntityReviewApproved)
			witnessStatusParam = &s
		}
	}

	rec, err := s.repo.LogOperation(ctx, CreateOperationParams{
		ID:                domain.NewID(),
		ClinicID:          in.ClinicID,
		ShelfID:           in.ShelfID,
		SubjectID:         in.SubjectID,
		NoteID:            in.NoteID,
		NoteFieldID:       in.NoteFieldID,
		Operation:         in.Operation,
		Quantity:          in.Quantity,
		Unit:              in.Unit,
		Dose:              in.Dose,
		Route:             in.Route,
		ReasonIndication:  in.ReasonIndication,
		AdministeredBy:    in.AdministeredBy,
		WitnessedBy:       in.WitnessedBy,
		PrescribedBy:      in.PrescribedBy,
		BalanceBefore:     balanceBefore,
		BalanceAfter:      balanceAfter,
		AddendsTo:         in.AddendsTo,
		Status:            in.Status,

		// Compliance v2: page-identity + chain key + retention floor.
		DrugName:       cc.DrugName,
		DrugStrength:   cc.Strength,
		DrugForm:       cc.Form,
		ChainKey:       chainK,
		RetentionUntil: retentionUntil,

		// Witness shape (00074 + 00075).
		WitnessKind:         witnessKindParam,
		ExternalWitnessName: trimNonEmpty(in.ExternalWitnessName),
		ExternalWitnessRole: trimNonEmpty(in.ExternalWitnessRole),
		WitnessAttestation:  trimNonEmpty(in.WitnessAttestation),
		WitnessStatus:       witnessStatusParam,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: %w", err)
	}

	// Subject-access log: a drug op against a subject is a PII touch.
	if in.SubjectID != nil && s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, in.ClinicID, *in.SubjectID, in.StaffID, "drug_op", "drugs.service.LogOperation")
	}

	// Async-witness path: hand the row off to the approvals service so
	// a qualified colleague can sign it from the queue. Failures here
	// log + propagate so the caller knows the queue insert didn't take
	// (the ledger row has already landed; the witness_status column
	// is 'pending' so the row is visible as such on regulator
	// reports until the queue eventually catches up).
	if witnessKind == "pending" && s.approvals != nil {
		operation := in.Operation
		opPtr := &operation
		note := trimNonEmpty(in.WitnessNote)
		if err := s.approvals.Submit(ctx, ApprovalSubmitInput{
			ClinicID:    in.ClinicID,
			EntityID:    rec.ID,
			EntityOp:    opPtr,
			SubmittedBy: in.AdministeredBy,
			StaffRole:   "", // handler doesn't surface role on this path; ok for now
			Note:        note,
			SubjectID:   in.SubjectID,
			NoteID:      in.NoteID,
		}); err != nil {
			slog.Error("drugs.service.LogOperation: approval submit failed; ledger row landed but queue insert missed",
				"op_id", rec.ID, "error", err.Error())
			return nil, fmt.Errorf("drugs.service.LogOperation: approval submit: %w", err)
		}
	}

	return operationRecordToResponse(rec), nil
}

// trimNonEmpty returns nil for nil or whitespace-only strings; the
// trimmed value otherwise. Keeps optional witness fields out of the DB
// when callers send empty strings instead of nil.
func trimNonEmpty(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// runComplianceCheckShadow runs the per-country compliance validator
// and logs any findings. Operation is NOT blocked — Phase 2a ships in
// shadow mode so we can measure rule coverage against real traffic
// before flipping to enforce. Errors loading catalog/state never block
// either; they're logged for ops visibility.
func (s *Service) runComplianceCheckShadow(ctx context.Context, in LogOperationInput, shelf *ShelfRecord, requiresWitness bool) {
	cc, err := s.loadCatalogContext(ctx, in.ClinicID, shelf)
	if err != nil {
		slog.Warn("drugs.compliance.shadow: catalog context load failed",
			"clinic_id", in.ClinicID, "error", err.Error())
		return
	}
	_, country, err := s.clinics.GetVerticalAndCountry(ctx, in.ClinicID)
	if err != nil {
		slog.Warn("drugs.compliance.shadow: clinic country lookup failed",
			"clinic_id", in.ClinicID, "error", err.Error())
		return
	}
	state, err := s.clinics.GetClinicState(ctx, in.ClinicID)
	if err != nil {
		slog.Warn("drugs.compliance.shadow: clinic state lookup failed",
			"clinic_id", in.ClinicID, "error", err.Error())
		// continue — state is optional; AU falls back to WA-strict.
	}

	in.Compliance.Witnessed = in.WitnessedBy != nil

	octx := validators.OperationContext{
		Operation:     validators.Operation(in.Operation),
		Schedule:      cc.Schedule,
		IsControlled:  cc.IsControlled || requiresWitness,
		ClinicCountry: country,
		ClinicState:   state,
		Compliance:    in.Compliance,
	}

	v := validators.Dispatch(country)
	issues := v.Validate(octx)
	if len(issues) == 0 {
		return
	}
	for _, issue := range issues {
		slog.Info("drugs.compliance.shadow",
			"clinic_id", in.ClinicID,
			"shelf_id", in.ShelfID,
			"operation", in.Operation,
			"country", country,
			"state", state,
			"schedule", cc.Schedule,
			"field", issue.Field,
			"code", issue.Code,
			"severity", severityLabel(issue.Severity),
			"message", issue.Message,
		)
	}
}

func severityLabel(s validators.Severity) string {
	if s == validators.Error {
		return "error"
	}
	return "warning"
}

// catalogContext bundles the regulator-relevant catalog facts for one
// shelf row: page-identity (drug name + strength + form) plus the
// schedule + control-flag the validators need.
//
// Returned even for override drugs (catalog metadata may be empty for
// clinic-defined drugs — the override path returns ScheduleEmpty +
// IsControlled=false today; controls metadata for overrides is a
// known v1 limitation called out in shelfRequiresWitness).
type catalogContext struct {
	DrugName     string
	Strength     string
	Form         string
	Schedule     string
	IsControlled bool
}

// loadCatalogContext joins the shelf row to the system catalog (or
// override) and returns the page-identity + control facts. Used by
// LogOperation to build the validator OperationContext + (Phase 2b)
// the chain key.
func (s *Service) loadCatalogContext(ctx context.Context, clinicID uuid.UUID, shelf *ShelfRecord) (*catalogContext, error) {
	cc := &catalogContext{}
	if shelf.Strength != nil {
		cc.Strength = *shelf.Strength
	}
	if shelf.Form != nil {
		cc.Form = *shelf.Form
	}

	if shelf.CatalogID != nil {
		vertical, country, err := s.clinics.GetVerticalAndCountry(ctx, clinicID)
		if err != nil {
			return nil, fmt.Errorf("clinic lookup: %w", err)
		}
		entry := s.cat.Lookup(vertical, country, *shelf.CatalogID)
		if entry != nil {
			cc.DrugName = entry.Name
			cc.Schedule = entry.Schedule
			cc.IsControlled = entry.RequiresWitness() || entry.Controls.RegisterRequired
			if cc.Form == "" {
				cc.Form = entry.Form
			}
			return cc, nil
		}
	}

	if shelf.OverrideDrugID != nil {
		ov, err := s.repo.GetOverrideDrugByID(ctx, *shelf.OverrideDrugID, clinicID)
		if err != nil {
			return nil, fmt.Errorf("override lookup: %w", err)
		}
		cc.DrugName = ov.Name
		if ov.Schedule != nil {
			cc.Schedule = *ov.Schedule
		}
		// Override drugs don't carry Controls metadata in v1; treat as
		// non-controlled. (See shelfRequiresWitness for the same v1
		// limitation.)
	}

	return cc, nil
}

// shelfRequiresWitness consults the catalog (or override) for the
// underlying drug and returns whether logging an op against this shelf
// row needs a witness. Override drugs without explicit Controls metadata
// are treated as non-controlled — clinics adding overrides for CDs MUST
// use the Schedule field plus a custom rules table (future work).
func (s *Service) shelfRequiresWitness(ctx context.Context, clinicID uuid.UUID, shelf *ShelfRecord) (bool, error) {
	if shelf.CatalogID != nil {
		vertical, country, err := s.clinics.GetVerticalAndCountry(ctx, clinicID)
		if err != nil {
			return false, fmt.Errorf("clinic lookup: %w", err)
		}
		entry := s.cat.Lookup(vertical, country, *shelf.CatalogID)
		if entry == nil {
			// Catalog drift: catalog_id present but no matching entry. Treat
			// conservatively as controlled.
			return true, nil
		}
		return entry.RequiresWitness(), nil
	}
	// Override drug — we don't have controls metadata on the override row
	// today. v1 treats overrides as non-controlled; clinics needing
	// witnessing on overrides should request a system catalog addition.
	return false, nil
}

// validateOperation checks the input shape before any DB call.
func validateOperation(in LogOperationInput) error {
	if in.Quantity <= 0 {
		return fmt.Errorf("drugs.service.validateOperation: quantity must be > 0: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Unit) == "" {
		return fmt.Errorf("drugs.service.validateOperation: unit required: %w", domain.ErrValidation)
	}
	switch in.Operation {
	case "administer", "dispense":
		if in.SubjectID == nil {
			return fmt.Errorf("drugs.service.validateOperation: subject_id required for %s: %w", in.Operation, domain.ErrValidation)
		}
	case "discard", "receive", "transfer", "adjust":
		// no subject required
	default:
		return fmt.Errorf("drugs.service.validateOperation: unknown operation %q: %w", in.Operation, domain.ErrValidation)
	}
	return nil
}

func computeBalanceAfter(op string, balanceBefore, qty float64) (float64, error) {
	switch op {
	case "receive":
		return balanceBefore + qty, nil
	case "administer", "dispense", "discard":
		next := balanceBefore - qty
		if next < 0 {
			return 0, fmt.Errorf("drugs.service.computeBalanceAfter: would result in negative balance (%v - %v): %w", balanceBefore, qty, domain.ErrValidation)
		}
		return next, nil
	case "transfer":
		// v1: transfer is logged but doesn't change THIS shelf's balance.
		// v2 will support split-row transfer (decrement source, increment
		// destination in one transaction).
		return balanceBefore, nil
	case "adjust":
		// Adjust treats Quantity as the new absolute balance. Reason field
		// must explain why (service caller enforces this; we just persist).
		return qty, nil
	default:
		return 0, fmt.Errorf("drugs.service.computeBalanceAfter: unknown op %q", op)
	}
}

// GetOperation — single-row read.
func (s *Service) GetOperation(ctx context.Context, id, clinicID uuid.UUID) (*OperationResponse, error) {
	rec, err := s.repo.GetOperationByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.GetOperation: %w", err)
	}
	return operationRecordToResponse(rec), nil
}

// ConfirmOperation flips a pending_confirm row created via a
// system.drug_op widget to confirmed. Idempotent: re-calling on an
// already-confirmed row returns it unchanged. Witness must be the same
// staff_id rules apply at LogOperation time — this method does NOT
// re-validate witness because the row already exists; it ONLY records
// the clinician's explicit acknowledgement that the AI-prefilled values
// are correct.
//
// Note submission is gated by ListPendingConfirmForNote — any
// unconfirmed system.drug_op row blocks submission.
func (s *Service) ConfirmOperation(ctx context.Context, id, clinicID, staffID uuid.UUID) (*OperationResponse, error) {
	rec, err := s.repo.ConfirmOperation(ctx, id, clinicID, staffID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ConfirmOperation: %w", err)
	}
	return operationRecordToResponse(rec), nil
}

// HasPendingConfirmForNote reports whether any drug op linked to the
// given note still needs confirmation. The note-submit pipeline calls
// this before allowing submission.
func (s *Service) HasPendingConfirmForNote(ctx context.Context, noteID, clinicID uuid.UUID) (bool, error) {
	pending, err := s.repo.ListPendingConfirmForNote(ctx, noteID, clinicID)
	if err != nil {
		return false, fmt.Errorf("drugs.service.HasPendingConfirmForNote: %w", err)
	}
	return len(pending) > 0, nil
}

// ListOperations — paginated ledger.
func (s *Service) ListOperations(ctx context.Context, clinicID uuid.UUID, in ListOperationsInput) (*OperationListResponse, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	recs, total, err := s.repo.ListOperations(ctx, clinicID, ListOperationsParams{
		Limit:            in.Limit,
		Offset:           in.Offset,
		ShelfID:          in.ShelfID,
		SubjectID:        in.SubjectID,
		NoteID:           in.NoteID,
		Operation:        in.Operation,
		Since:            in.Since,
		Until:            in.Until,
		OnlyPendingRecon: in.OnlyPendingRecon,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListOperations: %w", err)
	}
	out := make([]*OperationResponse, len(recs))
	for i, r := range recs {
		out[i] = operationRecordToResponse(r)
	}
	return &OperationListResponse{Items: out, Total: total, Limit: in.Limit, Offset: in.Offset}, nil
}

// ListSubjectMedications — convenience wrapper that filters by subject_id
// and additionally logs the read to subject_access_log.
func (s *Service) ListSubjectMedications(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, in ListOperationsInput) (*OperationListResponse, error) {
	in.SubjectID = &subjectID
	resp, err := s.ListOperations(ctx, clinicID, in)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListSubjectMedications: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, subjectID, staffID, "drug_history_view", "drugs.service.ListSubjectMedications")
	}
	return resp, nil
}

// ── Reconciliation methods ───────────────────────────────────────────────────

// StartReconciliation creates the reconciliation row, computes the
// expected ledger_count from the operations log, and persists the diff.
// Status starts 'clean' if physical == ledger, else 'discrepancy_logged'.
//
// The (shelf, period_end) UNIQUE index in DB makes a duplicate start a
// conflict — this is the right behaviour: clinics can't double-close the
// same period. Re-running requires a fresh period_end (or escalation).
func (s *Service) StartReconciliation(ctx context.Context, in StartReconciliationInput) (*ReconciliationResponse, error) {
	if in.PeriodEnd.Before(in.PeriodStart) || in.PeriodEnd.Equal(in.PeriodStart) {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: period_end must be after period_start: %w", domain.ErrValidation)
	}
	if in.PhysicalCount < 0 {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: physical_count must be >= 0: %w", domain.ErrValidation)
	}
	// Refuse if this period_end is already reconciled.
	exists, err := s.repo.HasOpenReconciliation(ctx, in.ShelfID, in.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: period already reconciled: %w", domain.ErrConflict)
	}

	// Compute ledger count: previous balance (from shelf at start) plus
	// net of operations in the period. Simpler: read current balance now;
	// subtract any operations *after* period_end (none if period_end ≈ now).
	// In practice clinics reconcile recently-closed periods, so the
	// period-bounded sum is what we want.
	shelf, err := s.repo.GetShelfEntryByID(ctx, in.ShelfID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: shelf: %w", err)
	}
	// ledger_count = current shelf balance — by the time we lock this
	// period, all operations within it are reflected. Discrepancy emerges
	// when physical_count diverges.
	ledgerCount := shelf.Balance

	rec, err := s.repo.CreateReconciliation(ctx, CreateReconciliationParams{
		ID:                     domain.NewID(),
		ClinicID:               in.ClinicID,
		ShelfID:                in.ShelfID,
		PeriodStart:            in.PeriodStart,
		PeriodEnd:              in.PeriodEnd,
		PhysicalCount:          in.PhysicalCount,
		LedgerCount:            ledgerCount,
		ReconciledByPrimary:    in.StaffID,
		DiscrepancyExplanation: in.DiscrepancyExplanation,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: %w", err)
	}

	// Lock the operations rows in the period to this reconciliation.
	if _, err := s.repo.LockOperationsToReconciliation(ctx, LockOperationsParams{
		ReconciliationID: rec.ID,
		ShelfID:          in.ShelfID,
		PeriodStart:      in.PeriodStart,
		PeriodEnd:        in.PeriodEnd,
	}); err != nil {
		return nil, fmt.Errorf("drugs.service.StartReconciliation: lock ops: %w", err)
	}

	return reconciliationRecordToResponse(rec), nil
}

// SecondarySignReconciliation captures the second-staff signoff. The
// secondary must differ from the primary (enforced here). For controlled
// drugs (which trigger the witnessing rule) this completes the regulator
// requirement; the row stays at its existing status (clean / discrepancy).
func (s *Service) SecondarySignReconciliation(ctx context.Context, in SecondarySignReconciliationInput) (*ReconciliationResponse, error) {
	rec, err := s.repo.GetReconciliationByID(ctx, in.ID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.SecondarySign: get: %w", err)
	}
	if in.StaffID == rec.ReconciledByPrimary {
		return nil, fmt.Errorf("drugs.service.SecondarySign: secondary must differ from primary: %w", domain.ErrValidation)
	}
	if rec.ReconciledBySecondary != nil {
		return nil, fmt.Errorf("drugs.service.SecondarySign: already signed: %w", domain.ErrConflict)
	}

	updated, err := s.repo.UpdateReconciliationStatus(ctx, UpdateReconciliationStatusParams{
		ID:                    in.ID,
		ClinicID:              in.ClinicID,
		Status:                rec.Status, // unchanged
		ReconciledBySecondary: &in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.SecondarySign: update: %w", err)
	}
	return reconciliationRecordToResponse(updated), nil
}

// ReportReconciliationToRegulator marks a discrepancy as escalated to
// regulator. Privacy-officer-only flow (handler enforces perm).
func (s *Service) ReportReconciliationToRegulator(ctx context.Context, in ReportReconciliationInput) (*ReconciliationResponse, error) {
	rec, err := s.repo.GetReconciliationByID(ctx, in.ID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.Report: get: %w", err)
	}
	if rec.Status == "clean" {
		return nil, fmt.Errorf("drugs.service.Report: no discrepancy to report: %w", domain.ErrValidation)
	}
	if rec.Status == "reported_to_regulator" {
		return nil, fmt.Errorf("drugs.service.Report: already reported: %w", domain.ErrConflict)
	}
	now := domainTimeNow()
	updated, err := s.repo.UpdateReconciliationStatus(ctx, UpdateReconciliationStatusParams{
		ID:         in.ID,
		ClinicID:   in.ClinicID,
		Status:     "reported_to_regulator",
		ReportedAt: &now,
		ReportedBy: &in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.Report: update: %w", err)
	}
	return reconciliationRecordToResponse(updated), nil
}

// ListReconciliations — paginated.
func (s *Service) ListReconciliations(ctx context.Context, clinicID uuid.UUID, in ListReconciliationsInput) (*ReconciliationListResponse, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	recs, total, err := s.repo.ListReconciliations(ctx, clinicID, ListReconciliationsParams(in))
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListReconciliations: %w", err)
	}
	out := make([]*ReconciliationResponse, len(recs))
	for i, r := range recs {
		out[i] = reconciliationRecordToResponse(r)
	}
	return &ReconciliationListResponse{Items: out, Total: total, Limit: in.Limit, Offset: in.Offset}, nil
}

// GetReconciliation — single row.
func (s *Service) GetReconciliation(ctx context.Context, id, clinicID uuid.UUID) (*ReconciliationResponse, error) {
	rec, err := s.repo.GetReconciliationByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.GetReconciliation: %w", err)
	}
	return reconciliationRecordToResponse(rec), nil
}

// ── response converters ──────────────────────────────────────────────────────

func catalogEntryToResponse(e catalog.Entry) CatalogEntryResponse {
	return CatalogEntryResponse{
		ID:                e.ID,
		Name:              e.Name,
		ActiveIngredient:  e.ActiveIngredient,
		Schedule:          e.Schedule,
		RegulatoryClass:   e.RegulatoryClass,
		CommonStrengths:   e.CommonStrengths,
		Form:              e.Form,
		CommonRoutes:      e.CommonRoutes,
		CommonDoses:       e.CommonDoses,
		BrandNames:        e.BrandNames,
		DefaultUnit:       e.DefaultUnit,
		Notes:             e.Notes,
		IsControlled:      e.IsControlled(),
		WitnessRequired:   e.RequiresWitness(),
		WitnessesNeeded:   e.Controls.WitnessesRequiredCount,
	}
}

func overrideRecordToResponse(r *OverrideDrugRecord) *OverrideDrugResponse {
	return &OverrideDrugResponse{
		ID:               r.ID.String(),
		Name:             r.Name,
		ActiveIngredient: r.ActiveIngredient,
		Schedule:         r.Schedule,
		Strength:         r.Strength,
		Form:             r.Form,
		BrandName:        r.BrandName,
		Notes:            r.Notes,
		CreatedAt:        r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        r.UpdatedAt.Format(time.RFC3339),
	}
}

func shelfRecordToResponse(r *ShelfRecord) *ShelfResponse {
	resp := &ShelfResponse{
		ID:           r.ID.String(),
		CatalogID:    r.CatalogID,
		Strength:     r.Strength,
		Form:         r.Form,
		BatchNumber:  r.BatchNumber,
		Location:     r.Location,
		Balance:      r.Balance,
		Unit:         r.Unit,
		ParLevel:     r.ParLevel,
		Notes:        r.Notes,
		CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    r.UpdatedAt.Format(time.RFC3339),
	}
	if r.OverrideDrugID != nil {
		s := r.OverrideDrugID.String()
		resp.OverrideDrugID = &s
	}
	if r.ExpiryDate != nil {
		s := r.ExpiryDate.Format("2006-01-02")
		resp.ExpiryDate = &s
	}
	if r.ArchivedAt != nil {
		s := r.ArchivedAt.Format(time.RFC3339)
		resp.ArchivedAt = &s
	}
	return resp
}

func operationRecordToResponse(r *OperationRecord) *OperationResponse {
	resp := &OperationResponse{
		ID:                r.ID.String(),
		ShelfID:           r.ShelfID.String(),
		Operation:         r.Operation,
		Quantity:          r.Quantity,
		Unit:              r.Unit,
		Dose:              r.Dose,
		Route:             r.Route,
		ReasonIndication:  r.ReasonIndication,
		AdministeredBy:    r.AdministeredBy.String(),
		BalanceBefore:     r.BalanceBefore,
		BalanceAfter:      r.BalanceAfter,
		Status:            r.Status,
		CreatedAt:         r.CreatedAt.Format(time.RFC3339),
	}
	if r.SubjectID != nil {
		s := r.SubjectID.String()
		resp.SubjectID = &s
	}
	if r.NoteID != nil {
		s := r.NoteID.String()
		resp.NoteID = &s
	}
	if r.NoteFieldID != nil {
		s := r.NoteFieldID.String()
		resp.NoteFieldID = &s
	}
	if r.WitnessedBy != nil {
		s := r.WitnessedBy.String()
		resp.WitnessedBy = &s
	}
	if r.PrescribedBy != nil {
		s := r.PrescribedBy.String()
		resp.PrescribedBy = &s
	}
	if r.ReconciliationID != nil {
		s := r.ReconciliationID.String()
		resp.ReconciliationID = &s
	}
	if r.AddendsTo != nil {
		s := r.AddendsTo.String()
		resp.AddendsTo = &s
	}
	if r.ConfirmedBy != nil {
		s := r.ConfirmedBy.String()
		resp.ConfirmedBy = &s
	}
	if r.ConfirmedAt != nil {
		s := r.ConfirmedAt.Format(time.RFC3339)
		resp.ConfirmedAt = &s
	}
	resp.WitnessKind = r.WitnessKind
	resp.ExternalWitnessName = r.ExternalWitnessName
	resp.ExternalWitnessRole = r.ExternalWitnessRole
	resp.WitnessAttestation = r.WitnessAttestation
	resp.WitnessStatus = r.WitnessStatus
	return resp
}

func reconciliationRecordToResponse(r *ReconciliationRecord) *ReconciliationResponse {
	resp := &ReconciliationResponse{
		ID:                       r.ID.String(),
		ShelfID:                  r.ShelfID.String(),
		PeriodStart:              r.PeriodStart.Format(time.RFC3339),
		PeriodEnd:                r.PeriodEnd.Format(time.RFC3339),
		PhysicalCount:            r.PhysicalCount,
		LedgerCount:              r.LedgerCount,
		Discrepancy:              r.Discrepancy,
		ReconciledByPrimary:      r.ReconciledByPrimary.String(),
		Status:                   r.Status,
		DiscrepancyExplanation:   r.DiscrepancyExplanation,
		CreatedAt:                r.CreatedAt.Format(time.RFC3339),
	}
	if r.ReconciledBySecondary != nil {
		s := r.ReconciledBySecondary.String()
		resp.ReconciledBySecondary = &s
	}
	if r.ReportedAt != nil {
		s := r.ReportedAt.Format(time.RFC3339)
		resp.ReportedAt = &s
	}
	if r.ReportedBy != nil {
		s := r.ReportedBy.String()
		resp.ReportedBy = &s
	}
	return resp
}

// domainTimeNow wraps domain.TimeNow for ergonomic use here. Indirection
// also makes it trivial to mock in tests via SetTimeNow.
func domainTimeNow() time.Time {
	return domain.TimeNow()
}

// ── Compliance v2: chain verification ─────────────────────────────────────

// VerifyChainInput identifies a chain to verify by its page-identity
// tuple. Service derives the chain_key from these — callers don't need
// to know the SHA256 detail.
type VerifyChainInput struct {
	ClinicID     uuid.UUID
	DrugName     string
	DrugStrength string
	DrugForm     string
}

// VerifyChainResponse mirrors repo.ChainStatus on the wire.
//
//nolint:revive
type VerifyChainResponse struct {
	Intact            bool   `json:"intact"`
	Length            int64  `json:"length"`
	FirstBrokenSeq    *int64 `json:"first_broken_seq,omitempty"`
	FirstBrokenReason string `json:"first_broken_reason,omitempty"`
}

// VerifyChain walks the (clinic, drug, strength, form) chain and
// reports whether every row's row_hash recomputes correctly. Used by
// the regulator-export endpoint + on-demand inspection by clinic
// admins.
func (s *Service) VerifyChain(ctx context.Context, in VerifyChainInput) (*VerifyChainResponse, error) {
	if in.DrugName == "" || in.DrugStrength == "" || in.DrugForm == "" {
		return nil, fmt.Errorf("drugs.service.VerifyChain: drug_name + strength + form required: %w", domain.ErrValidation)
	}
	chainK := chainKey(in.ClinicID, in.DrugName, in.DrugStrength, in.DrugForm)

	status, err := s.repo.VerifyChain(ctx, in.ClinicID, chainK)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.VerifyChain: %w", err)
	}
	return &VerifyChainResponse{
		Intact:            status.Intact,
		Length:            status.Length,
		FirstBrokenSeq:    status.FirstBrokenSeq,
		FirstBrokenReason: status.FirstBrokenReason,
	}, nil
}

// Sentinel: ensure errors.Is(err, domain.ErrXxx) works through our wrapping
// path for any caller that expects sentinel semantics.
var _ = errors.Is
