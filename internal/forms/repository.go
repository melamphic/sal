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
	RollbackOf        *uuid.UUID
	PolicyCheckResult *string
	PolicyCheckBy     *uuid.UUID
	PolicyCheckAt     *time.Time
	PublishedAt       *time.Time
	PublishedBy       *uuid.UUID
	CreatedBy         uuid.UUID
	CreatedAt         time.Time
}

// FieldRecord is the raw database representation of a form_fields row.
type FieldRecord struct {
	ID            uuid.UUID
	FormVersionID uuid.UUID
	Position      int
	Title         string
	Type          string
	Config        json.RawMessage
	AIPrompt      *string
	Required      bool
	Skippable     bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// StyleVersionRecord is the raw database representation of a clinic_form_style_versions row.
type StyleVersionRecord struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	Version      int
	LogoKey      *string
	PrimaryColor *string
	FontFamily   *string
	HeaderExtra  *string
	FooterText   *string
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
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
	VersionMajor  int
	VersionMinor  int
	ChangeType    domain.ChangeType
	ChangeSummary *string
	PublishedBy   uuid.UUID
	PublishedAt   time.Time
}

// SavePolicyCheckParams holds the result of a policy compliance check.
type SavePolicyCheckParams struct {
	VersionID uuid.UUID
	Result    string
	CheckedBy uuid.UUID
	CheckedAt time.Time
}

// CreateFieldParams holds values needed to insert a single field.
type CreateFieldParams struct {
	ID            uuid.UUID
	FormVersionID uuid.UUID
	Position      int
	Title         string
	Type          string
	Config        json.RawMessage
	AIPrompt      *string
	Required      bool
	Skippable     bool
}

// CreateStyleVersionParams holds values for a new style version row.
type CreateStyleVersionParams struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	Version      int
	LogoKey      *string
	PrimaryColor *string
	FontFamily   *string
	HeaderExtra  *string
	FooterText   *string
	CreatedBy    uuid.UUID
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

// CreateForm inserts a new form row.
func (r *Repository) CreateForm(ctx context.Context, p CreateFormParams) (*FormRecord, error) {
	const q = `
		INSERT INTO forms (id, clinic_id, group_id, name, description, overall_prompt, tags, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, clinic_id, group_id, name, description, overall_prompt, tags,
		          created_by, created_at, updated_at, archived_at, retire_reason`

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.GroupID, p.Name, p.Description,
		p.OverallPrompt, p.Tags, p.CreatedBy,
	)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateForm: %w", err)
	}
	return rec, nil
}

// GetFormByID fetches a form by ID scoped to the clinic.
func (r *Repository) GetFormByID(ctx context.Context, id, clinicID uuid.UUID) (*FormRecord, error) {
	const q = `
		SELECT id, clinic_id, group_id, name, description, overall_prompt, tags,
		       created_by, created_at, updated_at, archived_at, retire_reason
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
		SELECT id, clinic_id, group_id, name, description, overall_prompt, tags,
		       created_by, created_at, updated_at, archived_at, retire_reason
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
	const q = `
		UPDATE forms
		SET group_id = $3, name = $4, description = $5, overall_prompt = $6, tags = $7
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, group_id, name, description, overall_prompt, tags,
		          created_by, created_at, updated_at, archived_at, retire_reason`

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.GroupID, p.Name, p.Description, p.OverallPrompt, p.Tags,
	)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.UpdateFormMeta: %w", err)
	}
	return rec, nil
}

// RetireForm sets archived_at and retire_reason on the form.
func (r *Repository) RetireForm(ctx context.Context, p RetireFormParams) (*FormRecord, error) {
	const q = `
		UPDATE forms
		SET archived_at = $3, retire_reason = $4
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, group_id, name, description, overall_prompt, tags,
		          created_by, created_at, updated_at, archived_at, retire_reason`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.ArchivedAt, p.RetireReason)
	rec, err := scanForm(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.RetireForm: %w", err)
	}
	return rec, nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

const versionCols = `
	id, form_id, status, version_major, version_minor, change_type, change_summary,
	rollback_of, policy_check_result, policy_check_by, policy_check_at,
	published_at, published_by, created_by, created_at`

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

// PublishDraftVersion transitions a draft to published status.
func (r *Repository) PublishDraftVersion(ctx context.Context, p PublishDraftVersionParams) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE form_versions
		SET status        = 'published',
		    version_major = $2,
		    version_minor = $3,
		    change_type   = $4,
		    change_summary = $5,
		    published_by  = $6,
		    published_at  = $7
		WHERE id = $1 AND status = 'draft'
		RETURNING %s`, versionCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.VersionMajor, p.VersionMinor,
		string(p.ChangeType), p.ChangeSummary,
		p.PublishedBy, p.PublishedAt,
	)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.PublishDraftVersion: %w", err)
	}
	return rec, nil
}

// SavePolicyCheckResult records the AI policy-check result on the draft version.
func (r *Repository) SavePolicyCheckResult(ctx context.Context, p SavePolicyCheckParams) (*FormVersionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE form_versions
		SET policy_check_result = $2,
		    policy_check_by     = $3,
		    policy_check_at     = $4
		WHERE id = $1 AND status = 'draft'
		RETURNING %s`, versionCols)

	row := r.db.QueryRow(ctx, q, p.VersionID, p.Result, p.CheckedBy, p.CheckedAt)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.SavePolicyCheckResult: %w", err)
	}
	return rec, nil
}

// ── Fields ────────────────────────────────────────────────────────────────────

const fieldCols = `id, form_version_id, position, title, type, config, ai_prompt, required, skippable, created_at, updated_at`

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
		INSERT INTO form_fields (id, form_version_id, position, title, type, config, ai_prompt, required, skippable)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING %s`, fieldCols)

	result := make([]*FieldRecord, 0, len(fields))
	for _, fp := range fields {
		row := tx.QueryRow(ctx, q,
			fp.ID, fp.FormVersionID, fp.Position, fp.Title, fp.Type,
			fp.Config, fp.AIPrompt, fp.Required, fp.Skippable,
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

// LinkPolicy inserts a form_policies row.
func (r *Repository) LinkPolicy(ctx context.Context, formID, policyID, linkedBy uuid.UUID) error {
	const q = `
		INSERT INTO form_policies (form_id, policy_id, linked_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (form_id, policy_id) DO NOTHING`

	if _, err := r.db.Exec(ctx, q, formID, policyID, linkedBy); err != nil {
		return fmt.Errorf("forms.repo.LinkPolicy: %w", err)
	}
	return nil
}

// ListFormIDsByPolicyID returns all form IDs that have the given policy linked.
func (r *Repository) ListFormIDsByPolicyID(ctx context.Context, policyID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT form_id FROM form_policies WHERE policy_id = $1`

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

// UnlinkPolicy removes a form_policies row.
func (r *Repository) UnlinkPolicy(ctx context.Context, formID, policyID uuid.UUID) error {
	const q = `DELETE FROM form_policies WHERE form_id = $1 AND policy_id = $2`
	if _, err := r.db.Exec(ctx, q, formID, policyID); err != nil {
		return fmt.Errorf("forms.repo.UnlinkPolicy: %w", err)
	}
	return nil
}

// ListLinkedPolicies returns all policy IDs linked to a form.
func (r *Repository) ListLinkedPolicies(ctx context.Context, formID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT policy_id FROM form_policies WHERE form_id = $1 ORDER BY linked_at`

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

// ── Style ─────────────────────────────────────────────────────────────────────

const styleCols = `id, clinic_id, version, logo_key, primary_color, font_family, header_extra, footer_text, created_by, created_at`

// GetCurrentStyle returns the highest-version style record for the clinic.
func (r *Repository) GetCurrentStyle(ctx context.Context, clinicID uuid.UUID) (*StyleVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM clinic_form_style_versions
		WHERE clinic_id = $1
		ORDER BY version DESC
		LIMIT 1`, styleCols)

	row := r.db.QueryRow(ctx, q, clinicID)
	rec, err := scanStyle(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.GetCurrentStyle: %w", err)
	}
	return rec, nil
}

// CreateStyleVersion inserts a new style version row.
func (r *Repository) CreateStyleVersion(ctx context.Context, p CreateStyleVersionParams) (*StyleVersionRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO clinic_form_style_versions
		    (id, clinic_id, version, logo_key, primary_color, font_family, header_extra, footer_text, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING %s`, styleCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ClinicID, p.Version, p.LogoKey, p.PrimaryColor,
		p.FontFamily, p.HeaderExtra, p.FooterText, p.CreatedBy,
	)
	rec, err := scanStyle(row)
	if err != nil {
		return nil, fmt.Errorf("forms.repo.CreateStyleVersion: %w", err)
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
		&f.CreatedAt, &f.UpdatedAt, &f.ArchivedAt, &f.RetireReason,
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
		&changeType, &v.ChangeSummary, &v.RollbackOf,
		&v.PolicyCheckResult, &v.PolicyCheckBy, &v.PolicyCheckAt,
		&v.PublishedAt, &v.PublishedBy, &v.CreatedBy, &v.CreatedAt,
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

func scanStyle(row scannable) (*StyleVersionRecord, error) {
	var s StyleVersionRecord
	err := row.Scan(
		&s.ID, &s.ClinicID, &s.Version, &s.LogoKey, &s.PrimaryColor,
		&s.FontFamily, &s.HeaderExtra, &s.FooterText,
		&s.CreatedBy, &s.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanStyle: %w", err)
	}
	return &s, nil
}
