package forms

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// ── Record types ──────────────────────────────────────────────────────────────

// GroupRecord is the raw database representation of a form_groups row.
type GroupRecord struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Name        string
	Description *string
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// FormRecord is the raw database representation of a forms row.
type FormRecord struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	GroupID       *uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ArchivedAt    *time.Time
	RetireReason  *string
	RetiredBy     *uuid.UUID
	// SourceMarketplaceListingID/VersionID/AcquisitionID are non-nil only when
	// the form was imported from a marketplace listing. They power the
	// upgrade-flow UX: finding sibling forms imported from the same listing
	// (older or newer versions) and rendering the "v3 from marketplace" banner.
	SourceMarketplaceListingID     *uuid.UUID
	SourceMarketplaceVersionID     *uuid.UUID
	SourceMarketplaceAcquisitionID *uuid.UUID
	// Salvia-provided-content lineage — populated only when the materialiser
	// installs a template into a fresh clinic. Mutually exclusive with the
	// marketplace lineage above. When non-nil, the form participates in the
	// "Made by Salvia v1" UX (badge, upgrade banner, library panel).
	SalviaTemplateID       *string
	SalviaTemplateVersion  *int
	SalviaTemplateState    *string // "default" | "forked" | "deleted"
	FrameworkCurrencyDate  *time.Time
}

// FormVersionRecord is the raw database representation of a form_versions row.
type FormVersionRecord struct {
	ID                uuid.UUID
	FormID            uuid.UUID
	Status            domain.FormVersionStatus
	VersionMajor      *int
	VersionMinor      *int
	ChangeType        *domain.ChangeType
	ChangeSummary     *string
	// Changes is a JSONB array of typed ops ({op: "add_field", title: "...", ...})
	// computed client-side by diffing draft vs previous published. Stored opaque
	// so new op types can ship without a migration.
	Changes           json.RawMessage
	RollbackOf        *uuid.UUID
	PolicyCheckResult *string
	PolicyCheckBy     *uuid.UUID
	PolicyCheckAt     *time.Time
	PublishedAt       *time.Time
	PublishedBy       *uuid.UUID
	CreatedBy         uuid.UUID
	CreatedAt         time.Time
	// SystemHeaderConfig is the JSONB config for the patient-header card —
	// {enabled, fields[]} — pulled into every note rendering (review +
	// PDF). Stored opaque so adding new patient identifiers does not require
	// a migration; app code validates the field name list.
	SystemHeaderConfig json.RawMessage
	// GenerationMetadata is the AI-generation provenance JSONB written by
	// aigen-driven flows (provider, model, prompt_hash, staff_id, timestamps,
	// repair counts). NULL for human-authored versions. The Flutter editor
	// reads this to render an "AI drafted — review before publishing" pill.
	GenerationMetadata json.RawMessage
}

// FieldRecord is the raw database representation of a form_fields row.
type FieldRecord struct {
	ID             uuid.UUID
	FormVersionID  uuid.UUID
	Position       int
	Title          string
	Type           string
	Config         json.RawMessage
	AIPrompt       *string
	Required       bool
	Skippable      bool
	AllowInference bool     // false = reject AI inference; only direct quotes accepted
	MinConfidence  *float64 // ASR confidence floor; nil = no threshold enforced
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// StyleVersionRecord is the raw database representation of a clinic_form_style_versions row.
//
// Config is the rich doc-theme JSON blob produced by the designer (header shape,
// gradient stops, watermark, per-slot content, typography, etc.). The flat
// fields (LogoKey/PrimaryColor/FontFamily/HeaderExtra/FooterText) are kept as a
// legacy mirror of the top-level values for the old onboarding UI and simple
// renderers. PresetID tracks which vertical-specific preset the clinic is
// currently based on (e.g. "dental.clean_clinical").
type StyleVersionRecord struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	Version      int
	LogoKey      *string
	PrimaryColor *string
	FontFamily   *string
	HeaderExtra  *string
	FooterText   *string
	Config       json.RawMessage
	PresetID     *string
	IsActive     bool
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
	// PerDocOverrides keys partial DocTheme JSON blobs by doc-type
	// slug (signed_note | cd_register | …). The renderer merges
	// these over the base config when rendering the matching
	// doc-type. Always non-NULL at the column level (default '{}');
	// here represented as raw JSON for transport.
	PerDocOverrides json.RawMessage
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreateGroupParams holds values needed to insert a new form group.
type CreateGroupParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Name        string
	Description *string
	CreatedBy   uuid.UUID
}

// UpdateGroupParams holds values needed to update a form group.
type UpdateGroupParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Name        string
	Description *string
}

// CreateFormParams holds values needed to insert a new form row.
type CreateFormParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	GroupID       *uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
	CreatedBy     uuid.UUID
	// Marketplace lineage — populated only when the marketplace importer
	// creates the form. Nil for clinic-authored forms.
	SourceMarketplaceListingID     *uuid.UUID
	SourceMarketplaceVersionID     *uuid.UUID
	SourceMarketplaceAcquisitionID *uuid.UUID
	// Salvia-provided-content lineage — populated only when the materialiser
	// installs a Salvia v1 template into a clinic at signup. Mutually
	// exclusive with marketplace lineage.
	SalviaTemplateID       *string
	SalviaTemplateVersion  *int
	SalviaTemplateState    *string // "default" | "forked" | "deleted"
	FrameworkCurrencyDate  *time.Time
}

// UpdateFormMetaParams holds values needed to update form metadata.
// Only top-level form fields; fields/version data live on the draft version.
type UpdateFormMetaParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	GroupID       *uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
}

// RetireFormParams holds values for retiring (archiving) a form.
type RetireFormParams struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	RetireReason *string
	ArchivedAt   time.Time
	RetiredBy    uuid.UUID
}

// ListFormsParams holds filter and pagination parameters for listing forms.
type ListFormsParams struct {
	Limit           int
	Offset          int
	GroupID         *uuid.UUID
	IncludeArchived bool
	Tag             *string
}

// CreateDraftVersionParams holds values needed to create a new draft version.
type CreateDraftVersionParams struct {
	ID         uuid.UUID
	FormID     uuid.UUID
	RollbackOf *uuid.UUID
	CreatedBy  uuid.UUID
}

// PublishDraftVersionParams holds values for freezing a draft into a published version.
type PublishDraftVersionParams struct {
	ID            uuid.UUID // draft version ID
	ClinicID      uuid.UUID // tenant guard — must match the form's clinic
	VersionMajor  int
	VersionMinor  int
	ChangeType    domain.ChangeType
	ChangeSummary *string
	// Changes is the JSONB-serialised array of typed ops. Empty array when nil.
	Changes     json.RawMessage
	PublishedBy uuid.UUID
	PublishedAt time.Time
}

// CreatePublishedVersionParams inserts a brand-new row already in the
// published state. Used by rollback — the rollback result is itself a new
// immutable version, so we skip the draft stage entirely.
type CreatePublishedVersionParams struct {
	ID            uuid.UUID
	FormID        uuid.UUID
	VersionMajor  int
	VersionMinor  int
	ChangeType    domain.ChangeType
	ChangeSummary *string
	Changes       json.RawMessage
	RollbackOf    *uuid.UUID
	PublishedBy   uuid.UUID
	PublishedAt   time.Time
}

// SavePolicyCheckParams holds the result of a policy compliance check.
// Result is a JSON-encoded array of entries (one per linked policy); the
// policy_check_result column stores it as JSONB.
type SavePolicyCheckParams struct {
	VersionID uuid.UUID
	ClinicID  uuid.UUID // tenant guard — must match the form's clinic
	Result    string
	CheckedBy uuid.UUID
	CheckedAt time.Time
}

// CreateFieldParams holds values needed to insert a single field.
type CreateFieldParams struct {
	ID             uuid.UUID
	FormVersionID  uuid.UUID
	Position       int
	Title          string
	Type           string
	Config         json.RawMessage
	AIPrompt       *string
	Required       bool
	Skippable      bool
	AllowInference bool
	MinConfidence  *float64
}

// CreateStyleVersionParams holds values for a new style version row.
type CreateStyleVersionParams struct {
	ID              uuid.UUID
	ClinicID        uuid.UUID
	Version         int
	LogoKey         *string
	PrimaryColor    *string
	FontFamily      *string
	HeaderExtra     *string
	FooterText      *string
	Config          json.RawMessage
	PresetID        *string
	PerDocOverrides json.RawMessage // optional; nil falls back to '{}' in DB
	CreatedBy       uuid.UUID
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the PostgreSQL implementation of the forms repo interface.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a forms Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── Groups ────────────────────────────────────────────────────────────────────

// CreateGroup inserts a new form group.
func (r *Repository) CreateGroup(ctx context.Context, p CreateGroupParams) (*GroupRecord, error) {
	const q = `
		INSERT INTO form_groups (id, clinic_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, clinic_id, name, description, created_by, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.Name, p.Description, p.CreatedBy)
	rec, err := scanGroup(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateGroup: %w", err)
	}
	return rec, nil
}

// GetGroupByID fetches a group by ID scoped to the clinic.
func (r *Repository) GetGroupByID(ctx context.Context, id, clinicID uuid.UUID) (*GroupRecord, error) {
	const q = `
		SELECT id, clinic_id, name, description, created_by, created_at, updated_at
		FROM form_groups
		WHERE id = $1 AND clinic_id = $2`

	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanGroup(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetGroupByID: %w", err)
	}
	return rec, nil
}

// ListGroups returns all groups for a clinic ordered by name.
func (r *Repository) ListGroups(ctx context.Context, clinicID uuid.UUID) ([]*GroupRecord, error) {
	const q = `
		SELECT id, clinic_id, name, description, created_by, created_at, updated_at
		FROM form_groups
		WHERE clinic_id = $1
		ORDER BY name`

	rows, err := r.db.Query(ctx, q, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListGroups: %w", err)
	}
	defer rows.Close()

	var groups []*GroupRecord
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("forms.repo.ListGroups: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListGroups: rows: %w", err)
	}
	return groups, nil
}

// UpdateGroup updates a group's name and description.
func (r *Repository) UpdateGroup(ctx context.Context, p UpdateGroupParams) (*GroupRecord, error) {
	const q = `
		UPDATE form_groups
		SET name = $3, description = $4
		WHERE id = $1 AND clinic_id = $2
		RETURNING id, clinic_id, name, description, created_by, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.Name, p.Description)
	rec, err := scanGroup(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.UpdateGroup: %w", err)
	}
	return rec, nil
}

// ── Forms ─────────────────────────────────────────────────────────────────────

// formCols is the canonical SELECT/RETURNING column list for the forms table.
// All form-row scans go through scanForm in this exact order; keeping it as a
// single constant prevents drift across the dozen+ queries that touch this row.
const formCols = `id, clinic_id, group_id, name, description, overall_prompt, tags,
		          created_by, created_at, updated_at, archived_at, retire_reason, retired_by,
		          source_marketplace_listing_id, source_marketplace_version_id, source_marketplace_acquisition_id`

// CreateForm inserts a new form row.
func (r *Repository) CreateForm(ctx context.Context, p CreateFormParams) (*FormRecord, error) {
	const q = `
		INSERT INTO forms (id, clinic_id, group_id, name, description, overall_prompt, tags, created_by,
		                   source_marketplace_listing_id, source_marketplace_version_id, source_marketplace_acquisition_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING ` + formCols

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.GroupID, p.Name, p.Description,
		p.OverallPrompt, p.Tags, p.CreatedBy,
		p.SourceMarketplaceListingID, p.SourceMarketplaceVersionID, p.SourceMarketplaceAcquisitionID,
	)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateForm: %w", err)
	}
	return rec, nil
}

// CreateFormWithDraftParams bundles the inputs needed to create a form and
// its initial draft version atomically.
type CreateFormWithDraftParams struct {
	Form    CreateFormParams
	DraftID uuid.UUID
}

// CreateFormWithDraft inserts a new form and its initial empty draft version
// inside a single transaction. Either both rows are written or neither —
// preventing a partial failure from leaving a form without any draft (the
// "zombie form" state where neither edit, publish, nor policy-check can
// proceed).
func (r *Repository) CreateFormWithDraft(ctx context.Context, p CreateFormWithDraftParams) (*FormRecord, *FormVersionRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("forms.repo.CreateFormWithDraft: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const formQ = `
		INSERT INTO forms (id, clinic_id, group_id, name, description, overall_prompt, tags, created_by,
		                   source_marketplace_listing_id, source_marketplace_version_id, source_marketplace_acquisition_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING ` + formCols

	fp := p.Form
	formRow := tx.QueryRow(ctx, formQ,
		fp.ID, fp.ClinicID, fp.GroupID, fp.Name, fp.Description,
		fp.OverallPrompt, fp.Tags, fp.CreatedBy,
		fp.SourceMarketplaceListingID, fp.SourceMarketplaceVersionID, fp.SourceMarketplaceAcquisitionID,
	)
	formRec, err := scanForm(formRow)
	if err != nil {
		return nil, nil, fmt.Errorf("forms.repo.CreateFormWithDraft: form: %w", err)
	}

	verQ := fmt.Sprintf(`
		INSERT INTO form_versions (id, form_id, status, created_by)
		VALUES ($1, $2, 'draft', $3)
		RETURNING %s`, versionCols)

	verRow := tx.QueryRow(ctx, verQ, p.DraftID, fp.ID, fp.CreatedBy)
	verRec, err := scanVersion(verRow)
	if err != nil {
		return nil, nil, fmt.Errorf("forms.repo.CreateFormWithDraft: draft: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("forms.repo.CreateFormWithDraft: commit: %w", err)
	}
	return formRec, verRec, nil
}

// DeleteDraftVersion deletes the current draft version of a form. Returns
// domain.ErrNotFound if no draft exists. The form_fields rows owned by the
// draft are removed first (no FK cascade is configured) so this works as a
// single statement only when there's nothing referencing the version row;
// for never-published forms callers should prefer [DeleteFormCascade].
func (r *Repository) DeleteDraftVersion(ctx context.Context, formID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("forms.repo.DeleteDraftVersion: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM form_fields
		WHERE form_version_id IN (
			SELECT id FROM form_versions WHERE form_id = $1 AND status = 'draft'
		)`, formID); err != nil {
		return fmt.Errorf("forms.repo.DeleteDraftVersion: fields: %w", err)
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM form_versions WHERE form_id = $1 AND status = 'draft'`, formID)
	if err != nil {
		return fmt.Errorf("forms.repo.DeleteDraftVersion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("forms.repo.DeleteDraftVersion: %w", domain.ErrNotFound)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("forms.repo.DeleteDraftVersion: commit: %w", err)
	}
	return nil
}

// DeleteFormCascade hard-deletes a form along with all of its versions, the
// fields owned by those versions, and the form_policies links pointing at
// it. Marketplace `imported_form_id` references are NULLed out so an
// imported-then-discarded draft doesn't strand a marketplace install row.
//
// Used by Service.DiscardDraft when the form has never been published —
// "discard draft" then collapses the entire form record rather than
// leaving a zombie row with no usable version.
//
// Returns domain.ErrNotFound when no form row matches (id, clinic_id). All
// steps run in a single transaction.
func (r *Repository) DeleteFormCascade(ctx context.Context, formID, clinicID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Many-to-many link to policies — no FK cascade.
	if _, err := tx.Exec(ctx,
		`DELETE FROM form_policies WHERE form_id = $1`, formID); err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: form_policies: %w", err)
	}

	// 2. Form fields owned by ANY version of this form (no FK cascade).
	if _, err := tx.Exec(ctx, `
		DELETE FROM form_fields
		WHERE form_version_id IN (
			SELECT id FROM form_versions WHERE form_id = $1
		)`, formID); err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: form_fields: %w", err)
	}

	// 3. NULL out marketplace_acquisitions.imported_form_id so the
	//    acquisition tombstone survives — the linkage is gone but the
	//    marketplace history remains. Marketplace tables ship via
	//    migration 00032; if they're missing we expect the migration to
	//    have failed earlier and prefer to surface that here.
	if _, err := tx.Exec(ctx,
		`UPDATE marketplace_acquisitions SET imported_form_id = NULL WHERE imported_form_id = $1`,
		formID); err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: marketplace null-out: %w", err)
	}

	// 4. Form versions.
	if _, err := tx.Exec(ctx,
		`DELETE FROM form_versions WHERE form_id = $1`, formID); err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: form_versions: %w", err)
	}

	// 5. The form itself.
	tag, err := tx.Exec(ctx,
		`DELETE FROM forms WHERE id = $1 AND clinic_id = $2`, formID, clinicID)
	if err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: forms: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("forms.repo.DeleteFormCascade: %w", domain.ErrNotFound)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("forms.repo.DeleteFormCascade: commit: %w", err)
	}
	return nil
}

// GetFormByID fetches a form by ID scoped to the clinic.
func (r *Repository) GetFormByID(ctx context.Context, id, clinicID uuid.UUID) (*FormRecord, error) {
	q := `SELECT ` + formCols + `
		FROM forms
		WHERE id = $1 AND clinic_id = $2`

	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetFormByID: %w", err)
	}
	return rec, nil
}

// ListForms returns a paginated list of forms for a clinic.
func (r *Repository) ListForms(ctx context.Context, clinicID uuid.UUID, p ListFormsParams) ([]*FormRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"

	if !p.IncludeArchived {
		where += " AND archived_at IS NULL"
	}
	if p.GroupID != nil {
		args = append(args, *p.GroupID)
		where += fmt.Sprintf(" AND group_id = $%d", len(args))
	}
	if p.Tag != nil {
		args = append(args, *p.Tag)
		where += fmt.Sprintf(" AND $%d = ANY(tags)", len(args))
	}

	countQ := fmt.Sprintf("SELECT COUNT(*) FROM forms WHERE %s", where)
	var total int
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("forms.repo.ListForms: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	listQ := fmt.Sprintf(`
		SELECT `+formCols+`
		FROM forms
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("forms.repo.ListForms: %w", err)
	}
	defer rows.Close()

	var list []*FormRecord
	for rows.Next() {
		f, err := scanForm(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("forms.repo.ListForms: %w", err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("forms.repo.ListForms: rows: %w", err)
	}
	return list, total, nil
}

// UpdateFormMeta updates top-level form metadata on the forms row.
func (r *Repository) UpdateFormMeta(ctx context.Context, p UpdateFormMetaParams) (*FormRecord, error) {
	q := `UPDATE forms
		SET group_id = $3, name = $4, description = $5, overall_prompt = $6, tags = $7
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING ` + formCols

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.GroupID, p.Name, p.Description, p.OverallPrompt, p.Tags,
	)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.UpdateFormMeta: %w", err)
	}
	return rec, nil
}

// RetireForm sets archived_at, retire_reason, and retired_by on the form.
func (r *Repository) RetireForm(ctx context.Context, p RetireFormParams) (*FormRecord, error) {
	q := `UPDATE forms
		SET archived_at = $3, retire_reason = $4, retired_by = $5
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING ` + formCols

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.ArchivedAt, p.RetireReason, p.RetiredBy)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.RetireForm: %w", err)
	}
	return rec, nil
}

// ListByMarketplaceListing returns every form in this clinic that descended
// from the given marketplace listing — across all imported versions. Powers
// the upgrade UX: when a buyer imports a newer version, we surface their
// existing sibling form(s) so they can compare side-by-side and switch over.
//
// Includes archived forms so the UI can show "your previous version (archived)"
// after the buyer chooses to switch over.
func (r *Repository) ListByMarketplaceListing(ctx context.Context, clinicID, listingID uuid.UUID) ([]*FormRecord, error) {
	q := `SELECT ` + formCols + `
		FROM forms
		WHERE clinic_id = $1 AND source_marketplace_listing_id = $2
		ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, q, clinicID, listingID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListByMarketplaceListing: %w", err)
	}
	defer rows.Close()

	var list []*FormRecord
	for rows.Next() {
		f, err := scanForm(rows)
		if err != nil {
			return nil, fmt.Errorf("forms.repo.ListByMarketplaceListing: %w", err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListByMarketplaceListing: rows: %w", err)
	}
	return list, nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

const versionCols = `
	id, form_id, status, version_major, version_minor, change_type, change_summary,
	changes, rollback_of, policy_check_result::text, policy_check_by, policy_check_at,
	published_at, published_by, created_by, created_at, system_header_config,
	generation_metadata`

// prefixedVersionCols returns versionCols with each column qualified by alias —
// required when the same SELECT/RETURNING joins forms (which has overlapping
// names like id, created_at) so Postgres can disambiguate.
//
// IMPORTANT: this list MUST stay in sync with versionCols and scanVersion's
// Scan call. Adding a column to versionCols without updating both this
// builder and scanVersion produces a column-count mismatch in the
// publish / system-header / policy-check UPDATE…RETURNING paths, surfacing
// as an opaque "internal server error" 500 — the kind of regression that
// is hard to localise from the response alone.
func prefixedVersionCols(alias string) string {
	return fmt.Sprintf(`
	%[1]s.id, %[1]s.form_id, %[1]s.status, %[1]s.version_major, %[1]s.version_minor,
	%[1]s.change_type, %[1]s.change_summary, %[1]s.changes, %[1]s.rollback_of,
	%[1]s.policy_check_result::text, %[1]s.policy_check_by, %[1]s.policy_check_at,
	%[1]s.published_at, %[1]s.published_by, %[1]s.created_by, %[1]s.created_at,
	%[1]s.system_header_config, %[1]s.generation_metadata`, alias)
}

// GetDraftVersion returns the single mutable draft version for a form.
func (r *Repository) GetDraftVersion(ctx context.Context, formID uuid.UUID) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM form_versions WHERE form_id = $1 AND status = 'draft'`, versionCols)
	row := r.db.QueryRow(ctx, q, formID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetDraftVersion: %w", err)
	}
	return rec, nil
}

// GetFormPrompt returns the overall_prompt for the form that owns the given version.
// Used by the notes extraction worker to provide context to the AI alongside field-level prompts.
func (r *Repository) GetFormPrompt(ctx context.Context, versionID uuid.UUID) (*string, error) {
	var prompt *string
	err := r.db.QueryRow(ctx, `
		SELECT f.overall_prompt
		FROM form_versions fv
		JOIN forms f ON f.id = fv.form_id
		WHERE fv.id = $1`, versionID).Scan(&prompt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("forms.repo.GetFormPrompt: %w", err)
	}
	return prompt, nil
}

// GetVersionByID fetches a specific version by its ID.
func (r *Repository) GetVersionByID(ctx context.Context, id uuid.UUID) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM form_versions WHERE id = $1`, versionCols)
	row := r.db.QueryRow(ctx, q, id)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetVersionByID: %w", err)
	}
	return rec, nil
}

// ListPublishedVersions returns all published versions for a form, newest first.
func (r *Repository) ListPublishedVersions(ctx context.Context, formID uuid.UUID) ([]*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM form_versions
		WHERE form_id = $1 AND status = 'published'
		ORDER BY published_at DESC`, versionCols)

	rows, err := r.db.Query(ctx, q, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListPublishedVersions: %w", err)
	}
	defer rows.Close()

	var list []*FormVersionRecord
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("forms.repo.ListPublishedVersions: %w", err)
		}
		list = append(list, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListPublishedVersions: rows: %w", err)
	}
	return list, nil
}

// GetLatestPublishedVersion returns the most recently published version.
func (r *Repository) GetLatestPublishedVersion(ctx context.Context, formID uuid.UUID) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM form_versions
		WHERE form_id = $1 AND status = 'published'
		ORDER BY published_at DESC
		LIMIT 1`, versionCols)

	row := r.db.QueryRow(ctx, q, formID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetLatestPublishedVersion: %w", err)
	}
	return rec, nil
}


// CreateDraftVersion inserts a new draft version for a form.
func (r *Repository) CreateDraftVersion(ctx context.Context, p CreateDraftVersionParams) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO form_versions (id, form_id, status, rollback_of, created_by)
		VALUES ($1, $2, 'draft', $3, $4)
		RETURNING %s`, versionCols)

	row := r.db.QueryRow(ctx, q, p.ID, p.FormID, p.RollbackOf, p.CreatedBy)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateDraftVersion: %w", err)
	}
	return rec, nil
}

// CreatePublishedVersionWithFields inserts a brand-new version already in
// the published state AND its fields, all inside a single transaction. Used
// by rollback — a partial failure (version row inserted but field insert
// fails) would otherwise leave a published version with zero fields in the
// history, unusable but also undeletable since published versions are
// immutable.
//
// A collision on the partial unique index
// form_versions_published_semver_uniq (form_id, version_major, version_minor)
// WHERE status='published' maps to domain.ErrConflict so the caller can
// recompute the next version and retry.
func (r *Repository) CreatePublishedVersionWithFields(ctx context.Context, p CreatePublishedVersionParams, fields []CreateFieldParams) (*FormVersionRecord, []*FieldRecord, error) {
	changes := p.Changes
	if len(changes) == 0 {
		changes = json.RawMessage("[]")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("forms.repo.CreatePublishedVersionWithFields: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	versionQ := fmt.Sprintf(`
		INSERT INTO form_versions (
			id, form_id, status, version_major, version_minor,
			change_type, change_summary, changes, rollback_of,
			published_by, published_at, created_by
		) VALUES (
			$1, $2, 'published', $3, $4,
			$5, $6, $7, $8,
			$9, $10, $9
		)
		RETURNING %s`, versionCols)

	row := tx.QueryRow(ctx, versionQ,
		p.ID, p.FormID, p.VersionMajor, p.VersionMinor,
		string(p.ChangeType), p.ChangeSummary, changes, p.RollbackOf,
		p.PublishedBy, p.PublishedAt,
	)
	rec, err := scanVersion(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, nil, fmt.Errorf("forms.repo.CreatePublishedVersionWithFields: %w", domain.ErrConflict)
		}
		return nil, nil, fmt.Errorf("forms.repo.CreatePublishedVersionWithFields: %w", err)
	}

	fieldQ := fmt.Sprintf(`
		INSERT INTO form_fields (id, form_version_id, position, title, type, config, ai_prompt, required, skippable, allow_inference, min_confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING %s`, fieldCols)

	inserted := make([]*FieldRecord, 0, len(fields))
	for _, fp := range fields {
		frow := tx.QueryRow(ctx, fieldQ,
			fp.ID, fp.FormVersionID, fp.Position, fp.Title, fp.Type,
			fp.Config, fp.AIPrompt, fp.Required, fp.Skippable,
			fp.AllowInference, fp.MinConfidence,
		)
		f, err := scanField(frow)
		if err != nil {
			return nil, nil, fmt.Errorf("forms.repo.CreatePublishedVersionWithFields: field: %w", err)
		}
		inserted = append(inserted, f)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("forms.repo.CreatePublishedVersionWithFields: commit: %w", err)
	}
	return rec, inserted, nil
}

// PublishDraftVersion transitions a draft to published status. A unique
// violation on the partial index
// form_versions_published_semver_uniq (form_id, version_major, version_minor)
// WHERE status='published' maps to domain.ErrConflict so the service can
// recompute the next version and retry once.
func (r *Repository) PublishDraftVersion(ctx context.Context, p PublishDraftVersionParams) (*FormVersionRecord, error) {
	changes := p.Changes
	if len(changes) == 0 {
		changes = json.RawMessage("[]")
	}
	q := fmt.Sprintf(`
		UPDATE form_versions fv
		SET status        = 'published',
		    version_major = $2,
		    version_minor = $3,
		    change_type   = $4,
		    change_summary = $5,
		    changes       = $6,
		    published_by  = $7,
		    published_at  = $8
		FROM forms f
		WHERE fv.id = $1 AND fv.status = 'draft'
		  AND fv.form_id = f.id AND f.clinic_id = $9
		RETURNING %s`, prefixedVersionCols("fv"))

	row := r.db.QueryRow(ctx, q,
		p.ID, p.VersionMajor, p.VersionMinor,
		string(p.ChangeType), p.ChangeSummary, changes,
		p.PublishedBy, p.PublishedAt, p.ClinicID,
	)
	rec, err := scanVersion(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("forms.repo.PublishDraftVersion: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("forms.repo.PublishDraftVersion: %w", err)
	}
	return rec, nil
}

// UpdateDraftSystemHeader replaces the system_header_config JSONB on the
// given draft version. Published versions are immutable, so a non-draft row
// returns domain.ErrNotFound (the WHERE clause filters it out). clinicID is
// required for tenant isolation — the JOIN ensures the draft belongs to the
// caller's clinic, even if a service-layer bug passed the wrong version ID.
func (r *Repository) UpdateDraftSystemHeader(ctx context.Context, versionID, clinicID uuid.UUID, config []byte) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE form_versions fv
		SET system_header_config = $2::jsonb
		FROM forms f
		WHERE fv.id = $1 AND fv.status = 'draft'
		  AND fv.form_id = f.id AND f.clinic_id = $3
		RETURNING %s`, prefixedVersionCols("fv"))

	row := r.db.QueryRow(ctx, q, versionID, config, clinicID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.UpdateDraftSystemHeader: %w", err)
	}
	return rec, nil
}

// SavePolicyCheckResult records the AI policy-check result on the draft version.
// p.Result is a JSON-encoded array of PolicyCheckResultEntry; the column is JSONB.
func (r *Repository) SavePolicyCheckResult(ctx context.Context, p SavePolicyCheckParams) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE form_versions fv
		SET policy_check_result = $2::jsonb,
		    policy_check_by     = $3,
		    policy_check_at     = $4
		FROM forms f
		WHERE fv.id = $1 AND fv.status = 'draft'
		  AND fv.form_id = f.id AND f.clinic_id = $5
		RETURNING %s`, prefixedVersionCols("fv"))

	row := r.db.QueryRow(ctx, q, p.VersionID, p.Result, p.CheckedBy, p.CheckedAt, p.ClinicID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.SavePolicyCheckResult: %w", err)
	}
	return rec, nil
}

// ── Fields ────────────────────────────────────────────────────────────────────

const fieldCols = `id, form_version_id, position, title, type, config, ai_prompt, required, skippable, allow_inference, min_confidence, created_at, updated_at`

// GetFieldsByVersionID returns all fields for a version ordered by position.
func (r *Repository) GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]*FieldRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM form_fields
		WHERE form_version_id = $1
		ORDER BY position`, fieldCols)

	rows, err := r.db.Query(ctx, q, versionID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetFieldsByVersionID: %w", err)
	}
	defer rows.Close()

	var list []*FieldRecord
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, fmt.Errorf("forms.repo.GetFieldsByVersionID: %w", err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.GetFieldsByVersionID: rows: %w", err)
	}
	return list, nil
}

// ReplaceFields atomically replaces all fields for a draft version.
// It deletes existing fields then bulk-inserts the new set.
func (r *Repository) ReplaceFields(ctx context.Context, versionID uuid.UUID, fields []CreateFieldParams) ([]*FieldRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ReplaceFields: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM form_fields WHERE form_version_id = $1`, versionID); err != nil {
		return nil, fmt.Errorf("forms.repo.ReplaceFields: delete: %w", err)
	}

	q := fmt.Sprintf(`
		INSERT INTO form_fields (id, form_version_id, position, title, type, config, ai_prompt, required, skippable, allow_inference, min_confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING %s`, fieldCols)

	result := make([]*FieldRecord, 0, len(fields))
	for _, fp := range fields {
		row := tx.QueryRow(ctx, q,
			fp.ID, fp.FormVersionID, fp.Position, fp.Title, fp.Type,
			fp.Config, fp.AIPrompt, fp.Required, fp.Skippable,
			fp.AllowInference, fp.MinConfidence,
		)
		f, err := scanField(row)
		if err != nil {
			return nil, fmt.Errorf("forms.repo.ReplaceFields: insert: %w", err)
		}
		result = append(result, f)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("forms.repo.ReplaceFields: commit: %w", err)
	}
	return result, nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

// LinkPolicy inserts a form_policies row. If an inactive (unlinked) row
// already exists for this pair, it is reactivated in place rather than
// inserting a duplicate, preserving the audit trail.
func (r *Repository) LinkPolicy(ctx context.Context, formID, policyID, linkedBy uuid.UUID) error {
	const q = `
		INSERT INTO form_policies (form_id, policy_id, linked_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (form_id, policy_id) WHERE unlinked_at IS NULL DO NOTHING`

	if _, err := r.db.Exec(ctx, q, formID, policyID, linkedBy); err != nil {
		return fmt.Errorf("forms.repo.LinkPolicy: %w", err)
	}
	return nil
}

// ListFormIDsByPolicyID returns all form IDs that actively link the given policy.
// Soft-unlinked rows are excluded.
func (r *Repository) ListFormIDsByPolicyID(ctx context.Context, policyID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT form_id FROM form_policies WHERE policy_id = $1 AND unlinked_at IS NULL`

	rows, err := r.db.Query(ctx, q, policyID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListFormIDsByPolicyID: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("forms.repo.ListFormIDsByPolicyID: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListFormIDsByPolicyID: rows: %w", err)
	}
	return ids, nil
}

// UnlinkPolicy soft-unlinks a form_policies row, preserving audit history.
func (r *Repository) UnlinkPolicy(ctx context.Context, formID, policyID uuid.UUID) error {
	const q = `
		UPDATE form_policies
		SET unlinked_at = NOW()
		WHERE form_id = $1 AND policy_id = $2 AND unlinked_at IS NULL`
	if _, err := r.db.Exec(ctx, q, formID, policyID); err != nil {
		return fmt.Errorf("forms.repo.UnlinkPolicy: %w", err)
	}
	return nil
}

// UnlinkPolicyFromAllFormsParams carries the context recorded on each
// soft-unlinked row when a policy is retired, so the form's trail can surface
// which policy unlinked and why even after the policy itself is renamed later.
type UnlinkPolicyFromAllFormsParams struct {
	PolicyID           uuid.UUID
	PolicyNameSnapshot string
	Reason             *string
}

// UnlinkPolicyFromAllForms soft-unlinks every active row for the given policy,
// stamping the policy name (as of retire) and an optional reason on each row.
func (r *Repository) UnlinkPolicyFromAllForms(ctx context.Context, p UnlinkPolicyFromAllFormsParams) error {
	const q = `
		UPDATE form_policies
		SET unlinked_at          = NOW(),
		    unlinked_reason      = $2,
		    policy_name_snapshot = $3
		WHERE policy_id = $1 AND unlinked_at IS NULL`
	if _, err := r.db.Exec(ctx, q, p.PolicyID, p.Reason, p.PolicyNameSnapshot); err != nil {
		return fmt.Errorf("forms.repo.UnlinkPolicyFromAllForms: %w", err)
	}
	return nil
}

// ListLinkedPolicies returns all active policy IDs linked to a form.
func (r *Repository) ListLinkedPolicies(ctx context.Context, formID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT policy_id FROM form_policies
		WHERE form_id = $1 AND unlinked_at IS NULL
		ORDER BY linked_at`

	rows, err := r.db.Query(ctx, q, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListLinkedPolicies: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("forms.repo.ListLinkedPolicies: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListLinkedPolicies: rows: %w", err)
	}
	return ids, nil
}

// PolicyUnlinkEventRecord is a soft-unlinked form_policies row, used to
// inject synthetic "Policy X unlinked" entries into a form's version trail.
type PolicyUnlinkEventRecord struct {
	FormID             uuid.UUID
	PolicyID           uuid.UUID
	PolicyNameSnapshot *string
	UnlinkedAt         time.Time
	UnlinkedReason     *string
}

// ListPolicyUnlinkEvents returns every soft-unlinked policy for a form,
// newest first. Used by ListVersions to inject synthetic trail entries.
func (r *Repository) ListPolicyUnlinkEvents(ctx context.Context, formID uuid.UUID) ([]*PolicyUnlinkEventRecord, error) {
	const q = `
		SELECT form_id, policy_id, policy_name_snapshot, unlinked_at, unlinked_reason
		FROM form_policies
		WHERE form_id = $1 AND unlinked_at IS NOT NULL
		ORDER BY unlinked_at DESC`

	rows, err := r.db.Query(ctx, q, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListPolicyUnlinkEvents: %w", err)
	}
	defer rows.Close()

	var list []*PolicyUnlinkEventRecord
	for rows.Next() {
		var e PolicyUnlinkEventRecord
		if err := rows.Scan(&e.FormID, &e.PolicyID, &e.PolicyNameSnapshot, &e.UnlinkedAt, &e.UnlinkedReason); err != nil {
			return nil, fmt.Errorf("forms.repo.ListPolicyUnlinkEvents: scan: %w", err)
		}
		list = append(list, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListPolicyUnlinkEvents: rows: %w", err)
	}
	return list, nil
}

// ── Style ─────────────────────────────────────────────────────────────────────

const styleCols = `id, clinic_id, version, logo_key, primary_color, font_family, header_extra, footer_text, config, preset_id, is_active, created_by, created_at, per_doc_overrides`

// GetCurrentStyle returns the active style record for the clinic.
// Falls back to the highest-version row when no row is marked active (legacy rows).
func (r *Repository) GetCurrentStyle(ctx context.Context, clinicID uuid.UUID) (*StyleVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM clinic_form_style_versions
		WHERE clinic_id = $1
		ORDER BY is_active DESC, version DESC
		LIMIT 1`, styleCols)

	row := r.db.QueryRow(ctx, q, clinicID)
	rec, err := scanStyle(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetCurrentStyle: %w", err)
	}
	return rec, nil
}

// ListStyleVersions returns every style version for a clinic, newest first.
func (r *Repository) ListStyleVersions(ctx context.Context, clinicID uuid.UUID) ([]*StyleVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM clinic_form_style_versions
		WHERE clinic_id = $1
		ORDER BY version DESC`, styleCols)

	rows, err := r.db.Query(ctx, q, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.ListStyleVersions: %w", err)
	}
	defer rows.Close()

	var out []*StyleVersionRecord
	for rows.Next() {
		rec, scanErr := scanStyle(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("forms.repo.ListStyleVersions: %w", scanErr)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forms.repo.ListStyleVersions: rows: %w", err)
	}
	return out, nil
}

// CreateStyleVersion inserts a new style version row and marks it active,
// demoting any previously-active row for the clinic inside the same tx.
func (r *Repository) CreateStyleVersion(ctx context.Context, p CreateStyleVersionParams) (*StyleVersionRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateStyleVersion: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE clinic_form_style_versions SET is_active = FALSE
		WHERE clinic_id = $1 AND is_active`, p.ClinicID); err != nil {
		return nil, fmt.Errorf("forms.repo.CreateStyleVersion: deactivate: %w", err)
	}

	q := fmt.Sprintf(`
		INSERT INTO clinic_form_style_versions
		    (id, clinic_id, version, logo_key, primary_color, font_family, header_extra, footer_text, config, preset_id, is_active, created_by, per_doc_overrides)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, TRUE, $11, COALESCE($12, '{}'::jsonb))
		RETURNING %s`, styleCols)

	row := tx.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.Version, p.LogoKey, p.PrimaryColor,
		p.FontFamily, p.HeaderExtra, p.FooterText, p.Config, p.PresetID,
		p.CreatedBy, p.PerDocOverrides,
	)
	rec, err := scanStyle(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateStyleVersion: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("forms.repo.CreateStyleVersion: commit: %w", err)
	}
	return rec, nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanGroup(row scannable) (*GroupRecord, error) {
	var g GroupRecord
	err := row.Scan(
		&g.ID, &g.ClinicID, &g.Name, &g.Description,
		&g.CreatedBy, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanGroup: %w", err)
	}
	return &g, nil
}

func scanForm(row scannable) (*FormRecord, error) {
	var f FormRecord
	err := row.Scan(
		&f.ID, &f.ClinicID, &f.GroupID, &f.Name, &f.Description,
		&f.OverallPrompt, &f.Tags, &f.CreatedBy,
		&f.CreatedAt, &f.UpdatedAt, &f.ArchivedAt, &f.RetireReason, &f.RetiredBy,
		&f.SourceMarketplaceListingID, &f.SourceMarketplaceVersionID, &f.SourceMarketplaceAcquisitionID,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanForm: %w", err)
	}
	return &f, nil
}

func scanVersion(row scannable) (*FormVersionRecord, error) {
	var v FormVersionRecord
	var changeType *string
	err := row.Scan(
		&v.ID, &v.FormID, &v.Status, &v.VersionMajor, &v.VersionMinor,
		&changeType, &v.ChangeSummary, &v.Changes, &v.RollbackOf,
		&v.PolicyCheckResult, &v.PolicyCheckBy, &v.PolicyCheckAt,
		&v.PublishedAt, &v.PublishedBy, &v.CreatedBy, &v.CreatedAt,
		&v.SystemHeaderConfig,
		&v.GenerationMetadata,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanVersion: %w", err)
	}
	if changeType != nil {
		ct := domain.ChangeType(*changeType)
		v.ChangeType = &ct
	}
	return &v, nil
}

func scanField(row scannable) (*FieldRecord, error) {
	var f FieldRecord
	err := row.Scan(
		&f.ID, &f.FormVersionID, &f.Position, &f.Title, &f.Type,
		&f.Config, &f.AIPrompt, &f.Required, &f.Skippable,
		&f.AllowInference, &f.MinConfidence,
		&f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanField: %w", err)
	}
	return &f, nil
}

// SaveGenerationMetadata persists the AI-generation provenance JSONB on a
// form_version row. The version is matched via its parent form's clinic_id
// to enforce tenant isolation.
func (r *Repository) SaveGenerationMetadata(ctx context.Context, versionID, clinicID uuid.UUID, metadata []byte) error {
	const q = `
		UPDATE form_versions fv
		   SET generation_metadata = $1::JSONB
		  FROM forms f
		 WHERE fv.id = $2
		   AND fv.form_id = f.id
		   AND f.clinic_id = $3
	`
	tag, err := r.db.Exec(ctx, q, metadata, versionID, clinicID)
	if err != nil {
		return fmt.Errorf("forms.repo.SaveGenerationMetadata: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("forms.repo.SaveGenerationMetadata: %w", domain.ErrNotFound)
	}
	return nil
}

func scanStyle(row scannable) (*StyleVersionRecord, error) {
	var s StyleVersionRecord
	err := row.Scan(
		&s.ID, &s.ClinicID, &s.Version, &s.LogoKey, &s.PrimaryColor,
		&s.FontFamily, &s.HeaderExtra, &s.FooterText,
		&s.Config, &s.PresetID, &s.IsActive,
		&s.CreatedBy, &s.CreatedAt, &s.PerDocOverrides,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanStyle: %w", err)
	}
	return &s, nil
}
