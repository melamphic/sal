package policy

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

// PolicyFolderRecord is the raw database representation of a policy_folders row.
type PolicyFolderRecord struct {
	ID        uuid.UUID
	ClinicID  uuid.UUID
	Name      string
	CreatedBy uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PolicyRecord is the raw database representation of a policies row.
type PolicyRecord struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	FolderID     *uuid.UUID
	Name         string
	Description  *string
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   *time.Time
	RetireReason *string
}

// PolicyVersionRecord is the raw database representation of a policy_versions row.
type PolicyVersionRecord struct {
	ID            uuid.UUID
	PolicyID      uuid.UUID
	Status        string
	VersionMajor  *int
	VersionMinor  *int
	ChangeType    *string
	ChangeSummary *string
	Content       json.RawMessage
	RollbackOf    *uuid.UUID
	PublishedAt   *time.Time
	PublishedBy   *uuid.UUID
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
}

// PolicyClauseRecord is the raw database representation of a policy_clauses row.
type PolicyClauseRecord struct {
	ID              uuid.UUID
	PolicyVersionID uuid.UUID
	BlockID         string
	Title           string
	Body            string
	Parity          string
	CreatedAt       time.Time
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreateFolderParams holds values needed to insert a new policy folder.
type CreateFolderParams struct {
	ID        uuid.UUID
	ClinicID  uuid.UUID
	Name      string
	CreatedBy uuid.UUID
}

// UpdateFolderParams holds values needed to update a policy folder name.
type UpdateFolderParams struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	Name     string
}

// CreatePolicyParams holds values needed to insert a new policy row.
// SourceMarketplaceVersionID is non-nil only when the policy is materialised
// from a marketplace acquisition — enables soft edit warnings later.
type CreatePolicyParams struct {
	ID                         uuid.UUID
	ClinicID                   uuid.UUID
	FolderID                   *uuid.UUID
	Name                       string
	Description                *string
	CreatedBy                  uuid.UUID
	SourceMarketplaceVersionID *uuid.UUID
}

// UpdatePolicyMetaParams holds values needed to update policy metadata.
type UpdatePolicyMetaParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	FolderID    *uuid.UUID
	Name        string
	Description *string
}

// RetirePolicyParams holds values for retiring (archiving) a policy.
type RetirePolicyParams struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	RetireReason *string
	ArchivedAt   time.Time
}

// ListPoliciesParams holds filter and pagination for listing policies.
type ListPoliciesParams struct {
	Limit           int
	Offset          int
	FolderID        *uuid.UUID
	IncludeArchived bool
}

// CreateDraftVersionParams holds values needed to create a new draft version.
type CreateDraftVersionParams struct {
	ID         uuid.UUID
	PolicyID   uuid.UUID
	Content    json.RawMessage
	RollbackOf *uuid.UUID
	CreatedBy  uuid.UUID
}

// UpdateDraftContentParams holds values for updating the draft version.
type UpdateDraftContentParams struct {
	PolicyID uuid.UUID
	Content  json.RawMessage
}

// PublishDraftVersionParams holds values for publishing the draft version.
type PublishDraftVersionParams struct {
	PolicyID      uuid.UUID
	VersionMajor  int
	VersionMinor  int
	ChangeType    string
	ChangeSummary *string
	PublishedBy   uuid.UUID
	PublishedAt   time.Time
}

// ClauseInput holds values for a single clause in a replace operation.
type ClauseInput struct {
	BlockID string
	Title   string
	Body    string
	Parity  string
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the PostgreSQL implementation of the policy data-access layer.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a policy Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── Folders ───────────────────────────────────────────────────────────────────

// CreateFolder inserts a new policy folder.
func (r *Repository) CreateFolder(ctx context.Context, p CreateFolderParams) (*PolicyFolderRecord, error) {
	const q = `
		INSERT INTO policy_folders (id, clinic_id, name, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, clinic_id, name, created_by, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.Name, p.CreatedBy)
	rec, err := scanFolder(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.CreateFolder: %w", err)
	}
	return rec, nil
}

// UpdateFolder updates a folder's name.
func (r *Repository) UpdateFolder(ctx context.Context, p UpdateFolderParams) (*PolicyFolderRecord, error) {
	const q = `
		UPDATE policy_folders SET name = $3
		WHERE id = $1 AND clinic_id = $2
		RETURNING id, clinic_id, name, created_by, created_at, updated_at`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.Name)
	rec, err := scanFolder(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.UpdateFolder: %w", err)
	}
	return rec, nil
}

// ListFolders returns all folders for a clinic ordered by name.
func (r *Repository) ListFolders(ctx context.Context, clinicID uuid.UUID) ([]*PolicyFolderRecord, error) {
	const q = `
		SELECT id, clinic_id, name, created_by, created_at, updated_at
		FROM policy_folders
		WHERE clinic_id = $1
		ORDER BY name`

	rows, err := r.db.Query(ctx, q, clinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.ListFolders: %w", err)
	}
	defer rows.Close()

	var list []*PolicyFolderRecord
	for rows.Next() {
		rec, err := scanFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("policy.repo.ListFolders: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy.repo.ListFolders: rows: %w", err)
	}
	return list, nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

// CreatePolicyWithDraftParams bundles the inputs needed to create a policy
// and its initial draft version atomically.
type CreatePolicyWithDraftParams struct {
	Policy       CreatePolicyParams
	DraftID      uuid.UUID
	DraftContent json.RawMessage
}

// CreatePolicyWithDraft inserts a new policy and its initial empty draft
// version inside a single transaction. Either both rows are written or
// neither — preventing the "zombie policy with no draft" state that arose
// when the two inserts ran independently.
func (r *Repository) CreatePolicyWithDraft(ctx context.Context, p CreatePolicyWithDraftParams) (*PolicyRecord, *PolicyVersionRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("policy.repo.CreatePolicyWithDraft: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const polQ = `
		INSERT INTO policies (id, clinic_id, folder_id, name, description, created_by, source_marketplace_version_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, clinic_id, folder_id, name, description,
		          created_by, created_at, updated_at, archived_at, retire_reason`

	pp := p.Policy
	polRow := tx.QueryRow(ctx, polQ, pp.ID, pp.ClinicID, pp.FolderID, pp.Name, pp.Description, pp.CreatedBy, pp.SourceMarketplaceVersionID)
	polRec, err := scanPolicy(polRow)
	if err != nil {
		return nil, nil, fmt.Errorf("policy.repo.CreatePolicyWithDraft: policy: %w", err)
	}

	draftContent := p.DraftContent
	if draftContent == nil {
		draftContent = json.RawMessage(`[]`)
	}
	verQ := fmt.Sprintf(`
		INSERT INTO policy_versions (id, policy_id, status, content, created_by)
		VALUES ($1, $2, 'draft', $3, $4)
		RETURNING %s`, versionCols)

	verRow := tx.QueryRow(ctx, verQ, p.DraftID, pp.ID, draftContent, pp.CreatedBy)
	verRec, err := scanVersion(verRow)
	if err != nil {
		return nil, nil, fmt.Errorf("policy.repo.CreatePolicyWithDraft: draft: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("policy.repo.CreatePolicyWithDraft: commit: %w", err)
	}
	return polRec, verRec, nil
}

// GetPoliciesByIDs returns all policies whose IDs are in the given list and
// belong to clinicID, in a single query. Used by the marketplace publisher to
// batch-snapshot bundled policies without an N+1 round-trip per policy.
// Policies that don't exist or belong to other tenants are silently dropped —
// the caller decides whether a partial result is an error.
func (r *Repository) GetPoliciesByIDs(ctx context.Context, ids []uuid.UUID, clinicID uuid.UUID) ([]*PolicyRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const q = `
		SELECT id, clinic_id, folder_id, name, description,
		       created_by, created_at, updated_at, archived_at, retire_reason
		FROM policies
		WHERE id = ANY($1) AND clinic_id = $2`

	rows, err := r.db.Query(ctx, q, ids, clinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetPoliciesByIDs: %w", err)
	}
	defer rows.Close()

	var list []*PolicyRecord
	for rows.Next() {
		rec, err := scanPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("policy.repo.GetPoliciesByIDs: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy.repo.GetPoliciesByIDs: rows: %w", err)
	}
	return list, nil
}

// GetPolicyByID fetches a policy by ID scoped to the clinic.
func (r *Repository) GetPolicyByID(ctx context.Context, id, clinicID uuid.UUID) (*PolicyRecord, error) {
	const q = `
		SELECT id, clinic_id, folder_id, name, description,
		       created_by, created_at, updated_at, archived_at, retire_reason
		FROM policies
		WHERE id = $1 AND clinic_id = $2`

	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanPolicy(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetPolicyByID: %w", err)
	}
	return rec, nil
}

// ListPolicies returns a paginated list of policies for a clinic.
func (r *Repository) ListPolicies(ctx context.Context, clinicID uuid.UUID, p ListPoliciesParams) ([]*PolicyRecord, int, error) {
	args := []any{clinicID}
	where := "clinic_id = $1"

	if !p.IncludeArchived {
		where += " AND archived_at IS NULL"
	}
	if p.FolderID != nil {
		args = append(args, *p.FolderID)
		where += fmt.Sprintf(" AND folder_id = $%d", len(args))
	}

	countQ := fmt.Sprintf("SELECT COUNT(*) FROM policies WHERE %s", where)
	var total int
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("policy.repo.ListPolicies: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	listQ := fmt.Sprintf(`
		SELECT id, clinic_id, folder_id, name, description,
		       created_by, created_at, updated_at, archived_at, retire_reason
		FROM policies
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("policy.repo.ListPolicies: %w", err)
	}
	defer rows.Close()

	var list []*PolicyRecord
	for rows.Next() {
		rec, err := scanPolicy(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("policy.repo.ListPolicies: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("policy.repo.ListPolicies: rows: %w", err)
	}
	return list, total, nil
}

// UpdatePolicyMeta updates top-level policy metadata.
func (r *Repository) UpdatePolicyMeta(ctx context.Context, p UpdatePolicyMetaParams) (*PolicyRecord, error) {
	const q = `
		UPDATE policies
		SET folder_id = $3, name = $4, description = $5
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, folder_id, name, description,
		          created_by, created_at, updated_at, archived_at, retire_reason`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.FolderID, p.Name, p.Description)
	rec, err := scanPolicy(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.UpdatePolicyMeta: %w", err)
	}
	return rec, nil
}

// RetirePolicy sets archived_at and retire_reason on the policy.
func (r *Repository) RetirePolicy(ctx context.Context, p RetirePolicyParams) (*PolicyRecord, error) {
	const q = `
		UPDATE policies
		SET archived_at = $3, retire_reason = $4
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, folder_id, name, description,
		          created_by, created_at, updated_at, archived_at, retire_reason`

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.ArchivedAt, p.RetireReason)
	rec, err := scanPolicy(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.RetirePolicy: %w", err)
	}
	return rec, nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

const versionCols = `
	id, policy_id, status, version_major, version_minor, change_type, change_summary,
	content, rollback_of, published_at, published_by, created_by, created_at`

// GetDraftVersion returns the single mutable draft version for a policy.
func (r *Repository) GetDraftVersion(ctx context.Context, policyID uuid.UUID) (*PolicyVersionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM policy_versions WHERE policy_id = $1 AND status = 'draft'`, versionCols)
	row := r.db.QueryRow(ctx, q, policyID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetDraftVersion: %w", err)
	}
	return rec, nil
}

// GetVersionByID fetches a specific version by its ID.
func (r *Repository) GetVersionByID(ctx context.Context, id uuid.UUID) (*PolicyVersionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM policy_versions WHERE id = $1`, versionCols)
	row := r.db.QueryRow(ctx, q, id)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetVersionByID: %w", err)
	}
	return rec, nil
}

// ListPublishedVersions returns all published versions for a policy, newest first.
func (r *Repository) ListPublishedVersions(ctx context.Context, policyID uuid.UUID) ([]*PolicyVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM policy_versions
		WHERE policy_id = $1 AND status = 'published'
		ORDER BY published_at DESC`, versionCols)

	rows, err := r.db.Query(ctx, q, policyID)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.ListPublishedVersions: %w", err)
	}
	defer rows.Close()

	var list []*PolicyVersionRecord
	for rows.Next() {
		rec, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("policy.repo.ListPublishedVersions: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy.repo.ListPublishedVersions: rows: %w", err)
	}
	return list, nil
}

// GetLatestPublishedVersions returns the latest published version (if any)
// for each of the given policy IDs in a single query. Used to enrich list
// responses without N+1 round-trips.
func (r *Repository) GetLatestPublishedVersions(ctx context.Context, policyIDs []uuid.UUID) (map[uuid.UUID]*PolicyVersionRecord, error) {
	out := make(map[uuid.UUID]*PolicyVersionRecord, len(policyIDs))
	if len(policyIDs) == 0 {
		return out, nil
	}
	q := fmt.Sprintf(`
		SELECT DISTINCT ON (policy_id) %s
		FROM policy_versions
		WHERE policy_id = ANY($1) AND status = 'published'
		ORDER BY policy_id, published_at DESC`, versionCols)

	rows, err := r.db.Query(ctx, q, policyIDs)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetLatestPublishedVersions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		rec, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("policy.repo.GetLatestPublishedVersions: %w", err)
		}
		out[rec.PolicyID] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy.repo.GetLatestPublishedVersions: rows: %w", err)
	}
	return out, nil
}

// GetLatestPublishedVersion returns the most recently published version.
func (r *Repository) GetLatestPublishedVersion(ctx context.Context, policyID uuid.UUID) (*PolicyVersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM policy_versions
		WHERE policy_id = $1 AND status = 'published'
		ORDER BY published_at DESC
		LIMIT 1`, versionCols)

	row := r.db.QueryRow(ctx, q, policyID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetLatestPublishedVersion: %w", err)
	}
	return rec, nil
}

// CreateDraftVersion inserts a new draft version for a policy.
func (r *Repository) CreateDraftVersion(ctx context.Context, p CreateDraftVersionParams) (*PolicyVersionRecord, error) {
	content := p.Content
	if content == nil {
		content = json.RawMessage(`[]`)
	}
	q := fmt.Sprintf(`
		INSERT INTO policy_versions (id, policy_id, status, content, rollback_of, created_by)
		VALUES ($1, $2, 'draft', $3, $4, $5)
		RETURNING %s`, versionCols)

	row := r.db.QueryRow(ctx, q, p.ID, p.PolicyID, content, p.RollbackOf, p.CreatedBy)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.CreateDraftVersion: %w", err)
	}
	return rec, nil
}

// UpdateDraftContent replaces the content JSONB on the current draft version.
func (r *Repository) UpdateDraftContent(ctx context.Context, p UpdateDraftContentParams) (*PolicyVersionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE policy_versions
		SET content = $2
		WHERE policy_id = $1 AND status = 'draft'
		RETURNING %s`, versionCols)

	row := r.db.QueryRow(ctx, q, p.PolicyID, p.Content)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.UpdateDraftContent: %w", err)
	}
	return rec, nil
}

// DeleteDraftVersion deletes the current draft version of a policy. Returns
// ErrNotFound if no draft exists.
func (r *Repository) DeleteDraftVersion(ctx context.Context, policyID uuid.UUID) error {
	const q = `DELETE FROM policy_versions WHERE policy_id = $1 AND status = 'draft'`
	tag, err := r.db.Exec(ctx, q, policyID)
	if err != nil {
		return fmt.Errorf("policy.repo.DeleteDraftVersion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("policy.repo.DeleteDraftVersion: %w", domain.ErrNotFound)
	}
	return nil
}

// PublishDraftVersion transitions the draft to published status.
func (r *Repository) PublishDraftVersion(ctx context.Context, p PublishDraftVersionParams) (*PolicyVersionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE policy_versions
		SET status        = 'published',
		    version_major = $2,
		    version_minor = $3,
		    change_type   = $4,
		    change_summary = $5,
		    published_by  = $6,
		    published_at  = $7
		WHERE policy_id = $1 AND status = 'draft'
		RETURNING %s`, versionCols)

	row := r.db.QueryRow(ctx, q,
		p.PolicyID, p.VersionMajor, p.VersionMinor,
		p.ChangeType, p.ChangeSummary,
		p.PublishedBy, p.PublishedAt,
	)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.PublishDraftVersion: %w", err)
	}
	return rec, nil
}

// ── Clauses ───────────────────────────────────────────────────────────────────

// ReplaceClauses atomically replaces all clauses for a policy version.
// All inserts are sent as a single batch to minimise round-trips.
func (r *Repository) ReplaceClauses(ctx context.Context, versionID uuid.UUID, clauses []ClauseInput) ([]*PolicyClauseRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.ReplaceClauses: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM policy_clauses WHERE policy_version_id = $1`, versionID); err != nil {
		return nil, fmt.Errorf("policy.repo.ReplaceClauses: delete: %w", err)
	}

	if len(clauses) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("policy.repo.ReplaceClauses: commit: %w", err)
		}
		return nil, nil
	}

	const insertQ = `
		INSERT INTO policy_clauses (id, policy_version_id, block_id, title, body, parity)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, policy_version_id, block_id, title, body, parity, created_at`

	batch := &pgx.Batch{}
	ids := make([]uuid.UUID, len(clauses))
	for i, c := range clauses {
		ids[i] = domain.NewID()
		batch.Queue(insertQ, ids[i], versionID, c.BlockID, c.Title, c.Body, c.Parity)
	}

	br := tx.SendBatch(ctx, batch)
	result := make([]*PolicyClauseRecord, 0, len(clauses))
	for range clauses {
		row := br.QueryRow()
		rec, err := scanClause(row)
		if err != nil {
			_ = br.Close()
			return nil, fmt.Errorf("policy.repo.ReplaceClauses: insert: %w", err)
		}
		result = append(result, rec)
	}
	if err := br.Close(); err != nil {
		return nil, fmt.Errorf("policy.repo.ReplaceClauses: batch close: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("policy.repo.ReplaceClauses: commit: %w", err)
	}
	return result, nil
}

// ClauseWithPolicyID extends PolicyClauseRecord with the owning policy ID.
type ClauseWithPolicyID struct {
	PolicyClauseRecord
	PolicyID uuid.UUID
}

// GetLatestClausesForPolicies returns all clauses from the most recently published
// version of each given policy in a single query. Policies with no published version
// are silently skipped.
func (r *Repository) GetLatestClausesForPolicies(ctx context.Context, policyIDs []uuid.UUID) ([]*ClauseWithPolicyID, error) {
	if len(policyIDs) == 0 {
		return nil, nil
	}

	const q = `
		WITH latest AS (
			SELECT DISTINCT ON (policy_id) id AS version_id, policy_id
			FROM policy_versions
			WHERE policy_id = ANY($1) AND status = 'published'
			ORDER BY policy_id, published_at DESC
		)
		SELECT l.policy_id, pc.id, pc.policy_version_id, pc.block_id, pc.title, pc.body, pc.parity, pc.created_at
		FROM latest l
		JOIN policy_clauses pc ON pc.policy_version_id = l.version_id
		ORDER BY l.policy_id, pc.created_at`

	rows, err := r.db.Query(ctx, q, policyIDs)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.GetLatestClausesForPolicies: %w", err)
	}
	defer rows.Close()

	var list []*ClauseWithPolicyID
	for rows.Next() {
		var c ClauseWithPolicyID
		if err := rows.Scan(&c.PolicyID, &c.ID, &c.PolicyVersionID, &c.BlockID, &c.Title, &c.Body, &c.Parity, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("policy.repo.GetLatestClausesForPolicies: scan: %w", err)
		}
		list = append(list, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy.repo.GetLatestClausesForPolicies: rows: %w", err)
	}
	return list, nil
}

// ListClauses returns all clauses for a policy version ordered by creation time.
func (r *Repository) ListClauses(ctx context.Context, versionID uuid.UUID) ([]*PolicyClauseRecord, error) {
	const q = `
		SELECT id, policy_version_id, block_id, title, body, parity, created_at
		FROM policy_clauses
		WHERE policy_version_id = $1
		ORDER BY created_at`

	rows, err := r.db.Query(ctx, q, versionID)
	if err != nil {
		return nil, fmt.Errorf("policy.repo.ListClauses: %w", err)
	}
	defer rows.Close()

	var list []*PolicyClauseRecord
	for rows.Next() {
		rec, err := scanClause(rows)
		if err != nil {
			return nil, fmt.Errorf("policy.repo.ListClauses: %w", err)
		}
		list = append(list, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy.repo.ListClauses: rows: %w", err)
	}
	return list, nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanFolder(row scannable) (*PolicyFolderRecord, error) {
	var r PolicyFolderRecord
	err := row.Scan(&r.ID, &r.ClinicID, &r.Name, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanFolder: %w", err)
	}
	return &r, nil
}

func scanPolicy(row scannable) (*PolicyRecord, error) {
	var r PolicyRecord
	err := row.Scan(
		&r.ID, &r.ClinicID, &r.FolderID, &r.Name, &r.Description,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.ArchivedAt, &r.RetireReason,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanPolicy: %w", err)
	}
	return &r, nil
}

func scanVersion(row scannable) (*PolicyVersionRecord, error) {
	var r PolicyVersionRecord
	err := row.Scan(
		&r.ID, &r.PolicyID, &r.Status, &r.VersionMajor, &r.VersionMinor,
		&r.ChangeType, &r.ChangeSummary, &r.Content, &r.RollbackOf,
		&r.PublishedAt, &r.PublishedBy, &r.CreatedBy, &r.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanVersion: %w", err)
	}
	return &r, nil
}

func scanClause(row scannable) (*PolicyClauseRecord, error) {
	var r PolicyClauseRecord
	err := row.Scan(&r.ID, &r.PolicyVersionID, &r.BlockID, &r.Title, &r.Body, &r.Parity, &r.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanClause: %w", err)
	}
	return &r, nil
}
