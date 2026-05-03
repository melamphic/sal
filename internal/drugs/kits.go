package drugs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// ── Records (DB-shape) ───────────────────────────────────────────────────

// KitRecord is one drug_kits row — a clinic-defined bundle of drugs
// (e.g. spay pack, dental tray, end-of-life comfort pack). Items live
// in drug_kit_items keyed by kit_id; loaded together via GetKitWithItems.
type KitRecord struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Name        string
	Description *string
	UseContext  *string // 'spay' / 'dental_prophy' / 'discharge' / 'comfort_pack' / 'vaccine_panel'
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  *time.Time
}

// KitItemRecord is one drug_kit_items row. Exactly one of CatalogID or
// OverrideDrugID is non-nil (CHECK constraint at the DB level).
type KitItemRecord struct {
	ID               uuid.UUID
	KitID            uuid.UUID
	Position         int
	CatalogID        *string
	OverrideDrugID   *uuid.UUID
	DefaultQuantity  *float64
	Unit             string
	DefaultDose      *string
	DefaultRoute     *string
	DefaultOperation *string
	Notes            *string
	IsOptional       bool
	CreatedAt        time.Time
}

// KitWithItems aggregates a kit + its items for the API.
type KitWithItems struct {
	Kit   *KitRecord
	Items []*KitItemRecord
}

// ── Inputs ───────────────────────────────────────────────────────────────

// CreateKitInput is what the service receives when a clinic creates a
// new kit. Items can be empty at create-time (build out later via
// ReplaceKitItems).
type CreateKitInput struct { //nolint:revive
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	Name        string
	Description *string
	UseContext  *string
	Items       []KitItemInput
}

// UpdateKitInput updates the kit's metadata only — items are managed
// via ReplaceKitItems so the form can submit the whole list at once.
type UpdateKitInput struct { //nolint:revive
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Name        string
	Description *string
	UseContext  *string
}

// KitItemInput is one line of the kit. Either CatalogID or
// OverrideDrugID must be set; the service rejects payloads with both
// or neither.
type KitItemInput struct { //nolint:revive
	CatalogID        *string
	OverrideDrugID   *uuid.UUID
	DefaultQuantity  *float64
	Unit             string
	DefaultDose      *string
	DefaultRoute     *string
	DefaultOperation *string
	Notes            *string
	IsOptional       bool
}

// ── Responses (wire-shape) ───────────────────────────────────────────────

// KitResponse is the JSON-friendly kit shape.
//
//nolint:revive
type KitResponse struct {
	ID          string             `json:"id"`
	ClinicID    string             `json:"clinic_id"`
	Name        string             `json:"name"`
	Description *string            `json:"description,omitempty"`
	UseContext  *string            `json:"use_context,omitempty"`
	CreatedBy   string             `json:"created_by"`
	CreatedAt   string             `json:"created_at"`
	UpdatedAt   string             `json:"updated_at"`
	ArchivedAt  *string            `json:"archived_at,omitempty"`
	Items       []*KitItemResponse `json:"items"`
}

//nolint:revive
type KitItemResponse struct {
	ID               string   `json:"id"`
	Position         int      `json:"position"`
	CatalogID        *string  `json:"catalog_id,omitempty"`
	OverrideDrugID   *string  `json:"override_drug_id,omitempty"`
	DefaultQuantity  *float64 `json:"default_quantity,omitempty"`
	Unit             string   `json:"unit"`
	DefaultDose      *string  `json:"default_dose,omitempty"`
	DefaultRoute     *string  `json:"default_route,omitempty"`
	DefaultOperation *string  `json:"default_operation,omitempty"`
	Notes            *string  `json:"notes,omitempty"`
	IsOptional       bool     `json:"is_optional"`
}

//nolint:revive
type KitListResponse struct {
	Items []*KitResponse `json:"items"`
	Total int            `json:"total"`
}

// ── Repository methods ───────────────────────────────────────────────────

const kitCols = `id, clinic_id, name, description, use_context, created_by,
	created_at, updated_at, archived_at`

const kitItemCols = `id, kit_id, position, catalog_id, override_drug_id,
	default_quantity, unit, default_dose, default_route, default_operation,
	notes, is_optional, created_at`

// CreateKit inserts a new kit row. Items are inserted separately via
// ReplaceKitItems in the same tx.
func (r *Repository) CreateKit(ctx context.Context, p CreateKitInput) (*KitWithItems, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.CreateKit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id := domain.NewID()
	q := fmt.Sprintf(`
		INSERT INTO drug_kits (id, clinic_id, name, description, use_context, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING %s`, kitCols)
	row := tx.QueryRow(ctx, q, id, p.ClinicID, p.Name, p.Description, p.UseContext, p.StaffID)
	kit, err := scanKit(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.CreateKit: %w", err)
	}

	items, err := replaceKitItemsTx(ctx, tx, kit.ID, p.Items)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.CreateKit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("drugs.repo.CreateKit: commit: %w", err)
	}
	return &KitWithItems{Kit: kit, Items: items}, nil
}

// ListKits returns every non-archived kit for the clinic, with items.
// Done as two queries (kits + items WHERE kit_id IN (...)) and zipped
// in Go — small N (kits per clinic is dozens at most).
func (r *Repository) ListKits(ctx context.Context, clinicID uuid.UUID) ([]*KitWithItems, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM drug_kits
		WHERE clinic_id = $1 AND archived_at IS NULL
		ORDER BY name ASC`, kitCols)
	rows, err := r.db.Query(ctx, q, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ListKits: %w", err)
	}
	defer rows.Close()

	var kits []*KitRecord
	kitIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		k, err := scanKit(rows)
		if err != nil {
			return nil, fmt.Errorf("drugs.repo.ListKits: %w", err)
		}
		kits = append(kits, k)
		kitIDs = append(kitIDs, k.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("drugs.repo.ListKits: rows: %w", err)
	}

	itemsByKit := make(map[uuid.UUID][]*KitItemRecord)
	if len(kitIDs) > 0 {
		iq := fmt.Sprintf(`
			SELECT %s FROM drug_kit_items
			WHERE kit_id = ANY($1)
			ORDER BY kit_id, position`, kitItemCols)
		iRows, err := r.db.Query(ctx, iq, kitIDs)
		if err != nil {
			return nil, fmt.Errorf("drugs.repo.ListKits: items: %w", err)
		}
		defer iRows.Close()
		for iRows.Next() {
			it, err := scanKitItem(iRows)
			if err != nil {
				return nil, fmt.Errorf("drugs.repo.ListKits: scan item: %w", err)
			}
			itemsByKit[it.KitID] = append(itemsByKit[it.KitID], it)
		}
		if err := iRows.Err(); err != nil {
			return nil, fmt.Errorf("drugs.repo.ListKits: item rows: %w", err)
		}
	}

	out := make([]*KitWithItems, len(kits))
	for i, k := range kits {
		out[i] = &KitWithItems{Kit: k, Items: itemsByKit[k.ID]}
	}
	return out, nil
}

// GetKitByID returns a single kit + items, or domain.ErrNotFound.
func (r *Repository) GetKitByID(ctx context.Context, id, clinicID uuid.UUID) (*KitWithItems, error) {
	q := fmt.Sprintf(`SELECT %s FROM drug_kits WHERE id = $1 AND clinic_id = $2`, kitCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	kit, err := scanKit(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.GetKitByID: %w", err)
	}
	iq := fmt.Sprintf(`SELECT %s FROM drug_kit_items WHERE kit_id = $1 ORDER BY position`, kitItemCols)
	iRows, err := r.db.Query(ctx, iq, id)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.GetKitByID: items: %w", err)
	}
	defer iRows.Close()
	var items []*KitItemRecord
	for iRows.Next() {
		it, err := scanKitItem(iRows)
		if err != nil {
			return nil, fmt.Errorf("drugs.repo.GetKitByID: scan item: %w", err)
		}
		items = append(items, it)
	}
	if err := iRows.Err(); err != nil {
		return nil, fmt.Errorf("drugs.repo.GetKitByID: item rows: %w", err)
	}
	return &KitWithItems{Kit: kit, Items: items}, nil
}

// UpdateKit updates name/description/use_context only. Items are managed
// via ReplaceKitItems.
func (r *Repository) UpdateKit(ctx context.Context, p UpdateKitInput) (*KitRecord, error) {
	q := fmt.Sprintf(`
		UPDATE drug_kits
		SET name = $3, description = $4, use_context = $5, updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING %s`, kitCols)
	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.Name, p.Description, p.UseContext)
	rec, err := scanKit(row)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.UpdateKit: %w", err)
	}
	return rec, nil
}

// ArchiveKit soft-deletes by stamping archived_at. Idempotent.
func (r *Repository) ArchiveKit(ctx context.Context, id, clinicID uuid.UUID) error {
	const q = `UPDATE drug_kits SET archived_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL`
	tag, err := r.db.Exec(ctx, q, id, clinicID)
	if err != nil {
		return fmt.Errorf("drugs.repo.ArchiveKit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("drugs.repo.ArchiveKit: %w", domain.ErrNotFound)
	}
	return nil
}

// ReplaceKitItems wipes + re-inserts the kit's items in a tx. Simpler
// than diff-based item updates — kits are small (dozens of items at
// most) so the rewrite cost is trivial. Atomic per-kit.
func (r *Repository) ReplaceKitItems(ctx context.Context, kitID, clinicID uuid.UUID, items []KitItemInput) ([]*KitItemRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ReplaceKitItems: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Tenant guard — a 0-row UPDATE here means the kit isn't ours.
	guard, err := tx.Exec(ctx,
		`SELECT 1 FROM drug_kits WHERE id = $1 AND clinic_id = $2`,
		kitID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ReplaceKitItems: guard: %w", err)
	}
	if guard.RowsAffected() == 0 {
		// Exec doesn't return row counts for SELECT in pgx; fall back to a
		// QueryRow scan to confirm presence.
		var exists int
		err := tx.QueryRow(ctx,
			`SELECT 1 FROM drug_kits WHERE id = $1 AND clinic_id = $2`,
			kitID, clinicID).Scan(&exists)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("drugs.repo.ReplaceKitItems: %w", domain.ErrNotFound)
			}
			return nil, fmt.Errorf("drugs.repo.ReplaceKitItems: guard scan: %w", err)
		}
	}

	out, err := replaceKitItemsTx(ctx, tx, kitID, items)
	if err != nil {
		return nil, fmt.Errorf("drugs.repo.ReplaceKitItems: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("drugs.repo.ReplaceKitItems: commit: %w", err)
	}
	return out, nil
}

// replaceKitItemsTx — internal helper used by both CreateKit and
// ReplaceKitItems. Wipes existing items then bulk-inserts the new list.
func replaceKitItemsTx(ctx context.Context, tx pgx.Tx, kitID uuid.UUID, items []KitItemInput) ([]*KitItemRecord, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM drug_kit_items WHERE kit_id = $1`, kitID); err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}
	if len(items) == 0 {
		return nil, nil
	}
	q := fmt.Sprintf(`
		INSERT INTO drug_kit_items
		    (id, kit_id, position, catalog_id, override_drug_id,
		     default_quantity, unit, default_dose, default_route,
		     default_operation, notes, is_optional)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING %s`, kitItemCols)
	out := make([]*KitItemRecord, 0, len(items))
	for i, it := range items {
		row := tx.QueryRow(ctx, q,
			domain.NewID(), kitID, i,
			it.CatalogID, it.OverrideDrugID,
			it.DefaultQuantity, it.Unit, it.DefaultDose, it.DefaultRoute,
			it.DefaultOperation, it.Notes, it.IsOptional,
		)
		rec, err := scanKitItem(row)
		if err != nil {
			return nil, fmt.Errorf("insert item %d: %w", i, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func scanKit(row pgx.Row) (*KitRecord, error) {
	var k KitRecord
	err := row.Scan(
		&k.ID, &k.ClinicID, &k.Name, &k.Description, &k.UseContext,
		&k.CreatedBy, &k.CreatedAt, &k.UpdatedAt, &k.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanKit: %w", err)
	}
	return &k, nil
}

func scanKitItem(row pgx.Row) (*KitItemRecord, error) {
	var it KitItemRecord
	err := row.Scan(
		&it.ID, &it.KitID, &it.Position, &it.CatalogID, &it.OverrideDrugID,
		&it.DefaultQuantity, &it.Unit, &it.DefaultDose, &it.DefaultRoute,
		&it.DefaultOperation, &it.Notes, &it.IsOptional, &it.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanKitItem: %w", err)
	}
	return &it, nil
}

// ── KitsRepo (narrow interface for KitsService) ─────────────────────────

// KitsRepo is the slice of Repository the KitsService consumes. Defined
// here so unit tests can stub it without touching the broader drugs repo.
type KitsRepo interface {
	CreateKit(ctx context.Context, p CreateKitInput) (*KitWithItems, error)
	ListKits(ctx context.Context, clinicID uuid.UUID) ([]*KitWithItems, error)
	GetKitByID(ctx context.Context, id, clinicID uuid.UUID) (*KitWithItems, error)
	UpdateKit(ctx context.Context, p UpdateKitInput) (*KitRecord, error)
	ArchiveKit(ctx context.Context, id, clinicID uuid.UUID) error
	ReplaceKitItems(ctx context.Context, kitID, clinicID uuid.UUID, items []KitItemInput) ([]*KitItemRecord, error)
}

// Static check: Repository satisfies KitsRepo.
var _ KitsRepo = (*Repository)(nil)

// ── Service ──────────────────────────────────────────────────────────────

// KitsService is a separate service struct so the kits feature stays
// independent of the broader drugs.Service surface (which wires
// catalog / shelf / ops / reconciliation). KitsService only needs the
// kits repo + a clinic guard on writes.
type KitsService struct {
	repo KitsRepo
}

// NewKitsService constructs a KitsService.
func NewKitsService(r KitsRepo) *KitsService {
	return &KitsService{repo: r}
}

// CreateKit validates + persists a new kit. Items list can be empty;
// the user can fill it via UpdateItems later.
func (s *KitsService) CreateKit(ctx context.Context, in CreateKitInput) (*KitResponse, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("drugs.kits.CreateKit: name required: %w", domain.ErrValidation)
	}
	if err := validateItems(in.Items); err != nil {
		return nil, fmt.Errorf("drugs.kits.CreateKit: %w", err)
	}
	out, err := s.repo.CreateKit(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("drugs.kits.CreateKit: %w", err)
	}
	return toKitResponse(out), nil
}

// ListKits returns every non-archived kit for the clinic.
func (s *KitsService) ListKits(ctx context.Context, clinicID uuid.UUID) (*KitListResponse, error) {
	out, err := s.repo.ListKits(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.kits.ListKits: %w", err)
	}
	resp := &KitListResponse{Items: make([]*KitResponse, len(out)), Total: len(out)}
	for i, k := range out {
		resp.Items[i] = toKitResponse(k)
	}
	return resp, nil
}

// GetKit returns a single kit + items.
func (s *KitsService) GetKit(ctx context.Context, id, clinicID uuid.UUID) (*KitResponse, error) {
	out, err := s.repo.GetKitByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("drugs.kits.GetKit: %w", err)
	}
	return toKitResponse(out), nil
}

// UpdateKit updates the kit metadata. Items are managed separately.
func (s *KitsService) UpdateKit(ctx context.Context, in UpdateKitInput) (*KitResponse, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("drugs.kits.UpdateKit: name required: %w", domain.ErrValidation)
	}
	if _, err := s.repo.UpdateKit(ctx, in); err != nil {
		return nil, fmt.Errorf("drugs.kits.UpdateKit: %w", err)
	}
	return s.GetKit(ctx, in.ID, in.ClinicID)
}

// ReplaceItems wipes + re-inserts the kit's items in one shot. The
// frontend builds the full list, then submits it — simpler than
// per-item PATCH for a list this small.
func (s *KitsService) ReplaceItems(ctx context.Context, kitID, clinicID uuid.UUID, items []KitItemInput) (*KitResponse, error) {
	if err := validateItems(items); err != nil {
		return nil, fmt.Errorf("drugs.kits.ReplaceItems: %w", err)
	}
	if _, err := s.repo.ReplaceKitItems(ctx, kitID, clinicID, items); err != nil {
		return nil, fmt.Errorf("drugs.kits.ReplaceItems: %w", err)
	}
	return s.GetKit(ctx, kitID, clinicID)
}

// ArchiveKit soft-deletes the kit. Existing dispenses linked to the
// kit's drugs aren't touched — they live on drug_operations_log with
// their own snapshot.
func (s *KitsService) ArchiveKit(ctx context.Context, id, clinicID uuid.UUID) error {
	if err := s.repo.ArchiveKit(ctx, id, clinicID); err != nil {
		return fmt.Errorf("drugs.kits.ArchiveKit: %w", err)
	}
	return nil
}

// validateItems enforces the per-line invariants the DB CHECK
// constraint also enforces, surfaced earlier so the user gets a clean
// 422 instead of a 500.
func validateItems(items []KitItemInput) error {
	for i, it := range items {
		hasCat := it.CatalogID != nil && *it.CatalogID != ""
		hasOverride := it.OverrideDrugID != nil
		switch {
		case !hasCat && !hasOverride:
			return fmt.Errorf("item %d: must reference catalog_id or override_drug_id: %w",
				i, domain.ErrValidation)
		case hasCat && hasOverride:
			return fmt.Errorf("item %d: only one of catalog_id / override_drug_id allowed: %w",
				i, domain.ErrValidation)
		}
		if strings.TrimSpace(it.Unit) == "" {
			return fmt.Errorf("item %d: unit required: %w", i, domain.ErrValidation)
		}
	}
	return nil
}

func toKitResponse(k *KitWithItems) *KitResponse {
	resp := &KitResponse{
		ID:          k.Kit.ID.String(),
		ClinicID:    k.Kit.ClinicID.String(),
		Name:        k.Kit.Name,
		Description: k.Kit.Description,
		UseContext:  k.Kit.UseContext,
		CreatedBy:   k.Kit.CreatedBy.String(),
		CreatedAt:   k.Kit.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   k.Kit.UpdatedAt.Format(time.RFC3339),
		Items:       make([]*KitItemResponse, len(k.Items)),
	}
	if k.Kit.ArchivedAt != nil {
		s := k.Kit.ArchivedAt.Format(time.RFC3339)
		resp.ArchivedAt = &s
	}
	for i, it := range k.Items {
		resp.Items[i] = toKitItemResponse(it)
	}
	return resp
}

func toKitItemResponse(it *KitItemRecord) *KitItemResponse {
	r := &KitItemResponse{
		ID:               it.ID.String(),
		Position:         it.Position,
		CatalogID:        it.CatalogID,
		DefaultQuantity:  it.DefaultQuantity,
		Unit:             it.Unit,
		DefaultDose:      it.DefaultDose,
		DefaultRoute:     it.DefaultRoute,
		DefaultOperation: it.DefaultOperation,
		Notes:            it.Notes,
		IsOptional:       it.IsOptional,
	}
	if it.OverrideDrugID != nil {
		s := it.OverrideDrugID.String()
		r.OverrideDrugID = &s
	}
	return r
}

// _ keeps the pgxpool import alive even if we end up not using
// pool-typed args directly here in the future.
var _ = (*pgxpool.Pool)(nil)
