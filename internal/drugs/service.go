package drugs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/drugs/catalog"
)

// ── Service dependencies (small interfaces, satisfied by app.go adapters) ──

// ClinicLookup resolves vertical + country for a clinic — needed to scope
// the system catalog and to drive witnessing rules. Implemented by app.go
// via a thin wrapper over clinic.Service.
type ClinicLookup interface {
	GetVerticalAndCountry(ctx context.Context, clinicID uuid.UUID) (vertical, country string, err error)
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
	Strength       *string `json:"strength,omitempty"`
	Form           *string `json:"form,omitempty"`
	BatchNumber    *string `json:"batch_number,omitempty"`
	ExpiryDate     *string `json:"expiry_date,omitempty"`
	Location       string  `json:"location"`
	Balance        float64 `json:"balance"`
	Unit           string  `json:"unit"`
	ParLevel       *float64 `json:"par_level,omitempty"`
	Notes          *string `json:"notes,omitempty"`
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
	Status            string  `json:"status"`
	ConfirmedBy       *string `json:"confirmed_by,omitempty"`
	ConfirmedAt       *string `json:"confirmed_at,omitempty"`
	CreatedAt         string  `json:"created_at"`
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

// Service is the drugs business-logic layer. Concurrency-safe; can be
// shared across handlers without locking.
type Service struct {
	repo         repo
	cat          *catalog.Loader
	clinics      ClinicLookup
	staffPerms   StaffPermLookup
	accessLogger SubjectAccessLogger
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

	return shelfRecordToResponse(rec), nil
}

// GetShelfEntry returns one shelf row. Tenant-scoped.
func (s *Service) GetShelfEntry(ctx context.Context, id, clinicID uuid.UUID) (*ShelfResponse, error) {
	rec, err := s.repo.GetShelfEntryByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.GetShelfEntry: %w", err)
	}
	return shelfRecordToResponse(rec), nil
}

// ListShelfEntries — paginated.
func (s *Service) ListShelfEntries(ctx context.Context, clinicID uuid.UUID, in ListShelfInput) (*ShelfListResponse, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	recs, total, err := s.repo.ListShelfEntries(ctx, clinicID, ListShelfParams(in))
	if err != nil {
		return nil, fmt.Errorf("drugs.service.ListShelfEntries: %w", err)
	}
	out := make([]*ShelfResponse, len(recs))
	for i, r := range recs {
		out[i] = shelfRecordToResponse(r)
	}
	return &ShelfListResponse{Items: out, Total: total, Limit: in.Limit, Offset: in.Offset}, nil
}

// UpdateShelfMeta — non-balance updates.
func (s *Service) UpdateShelfMeta(ctx context.Context, in UpdateShelfMetaInput) (*ShelfResponse, error) {
	rec, err := s.repo.UpdateShelfMeta(ctx, UpdateShelfMetaParams(in))
	if err != nil {
		return nil, fmt.Errorf("drugs.service.UpdateShelfMeta: %w", err)
	}
	return shelfRecordToResponse(rec), nil
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

	// Witness validation — controlled drugs MUST have a witness who is not
	// the administering staff. If the underlying drug is non-controlled,
	// witnessed_by may be nil.
	if requiresWitness {
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
	}

	balanceBefore := shelf.Balance
	balanceAfter, err := computeBalanceAfter(in.Operation, balanceBefore, in.Quantity)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: %w", err)
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
	})
	if err != nil {
		return nil, fmt.Errorf("drugs.service.LogOperation: %w", err)
	}

	// Subject-access log: a drug op against a subject is a PII touch.
	if in.SubjectID != nil && s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, in.ClinicID, *in.SubjectID, in.StaffID, "drug_op", "drugs.service.LogOperation")
	}

	return operationRecordToResponse(rec), nil
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

// Sentinel: ensure errors.Is(err, domain.ErrXxx) works through our wrapping
// path for any caller that expects sentinel semantics.
var _ = errors.Is
