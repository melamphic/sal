package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// FormLinker is the cross-module interface used to detach a policy from all
// forms that reference it when the policy is retired. The concrete adapter
// is wired in app.go using the forms repository.
//
// policyName and reason are stamped on each unlinked row so the form's
// compliance trail can surface "Policy X retired — unlinked" entries even
// after the archived policy is later renamed.
type FormLinker interface {
	DetachPolicyFromForms(ctx context.Context, policyID uuid.UUID, policyName string, reason *string) error
}

// Service handles business logic for the policy module.
type Service struct {
	repo       *Repository
	formLinker FormLinker
}

// NewService constructs a policy Service.
func NewService(repo *Repository, formLinker FormLinker) *Service {
	return &Service{repo: repo, formLinker: formLinker}
}

// ── Response types ────────────────────────────────────────────────────────────

// PolicyFolderResponse is the API-safe representation of a policy folder.
//
//nolint:revive
type PolicyFolderResponse struct {
	ID        string `json:"id"`
	ClinicID  string `json:"clinic_id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// PolicyFolderListResponse is a list of policy folders.
//
//nolint:revive
type PolicyFolderListResponse struct {
	Items []*PolicyFolderResponse `json:"items"`
}

// PolicyClauseResponse is the API-safe representation of a policy clause.
//
//nolint:revive
type PolicyClauseResponse struct {
	ID      string `json:"id"`
	BlockID string `json:"block_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Parity  string `json:"parity"`
	// SourceCitation is an AI-suggested verbatim regulator quote backing
	// the clause. The Flutter editor renders this with an explicit
	// "AI-suggested — verify against [regulator]" badge so reviewers
	// understand the quote is unverified by the system.
	SourceCitation *string `json:"source_citation,omitempty"`
}

// PolicyClauseListResponse is a list of clauses.
//
//nolint:revive
type PolicyClauseListResponse struct {
	Items []*PolicyClauseResponse `json:"items"`
}

// PolicyVersionResponse is the API-safe representation of a policy version.
//
//nolint:revive
type PolicyVersionResponse struct {
	ID            string          `json:"id"`
	PolicyID      string          `json:"policy_id"`
	Status        string          `json:"status"`
	VersionMajor  *int            `json:"version_major,omitempty"`
	VersionMinor  *int            `json:"version_minor,omitempty"`
	ChangeType    *string         `json:"change_type,omitempty"`
	ChangeSummary *string         `json:"change_summary,omitempty"`
	Changes       json.RawMessage `json:"changes"`
	Content       json.RawMessage `json:"content"`
	RollbackOf    *string         `json:"rollback_of,omitempty"`
	PublishedAt   *string         `json:"published_at,omitempty"`
	PublishedBy   *string         `json:"published_by,omitempty"`
	CreatedAt     string          `json:"created_at"`
	// GenerationMetadata is the AI-generation provenance JSONB. NULL/absent
	// for human-authored versions; present means the editor renders an
	// "AI drafted — review before publishing" pill.
	GenerationMetadata json.RawMessage `json:"generation_metadata,omitempty"`
}

// PolicyVersionListResponse is a list of policy versions.
//
//nolint:revive
type PolicyVersionListResponse struct {
	Items []*PolicyVersionResponse `json:"items"`
}

// PolicyResponse is the API-safe representation of a policy with its current state.
//
//nolint:revive
type PolicyResponse struct {
	ID           string  `json:"id"`
	ClinicID     string  `json:"clinic_id"`
	FolderID     *string `json:"folder_id,omitempty"`
	Name         string  `json:"name"`
	Description  *string `json:"description,omitempty"`
	CreatedBy    string  `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	ArchivedAt   *string `json:"archived_at,omitempty"`
	RetireReason *string `json:"retire_reason,omitempty"`
	// Draft is non-nil when an editable draft version exists.
	Draft *PolicyVersionResponse `json:"draft,omitempty"`
	// LatestPublished is the most recent frozen version; nil on a brand-new policy.
	LatestPublished *PolicyVersionResponse `json:"latest_published,omitempty"`
	// Salvia v1 prebuilt content lineage — non-empty only when the policy was
	// installed by the salvia_content materialiser. Powers the "Made by
	// Salvia v1" badge and the Library panel.
	SalviaTemplateID      *string `json:"salvia_template_id,omitempty"`
	SalviaTemplateVersion *int    `json:"salvia_template_version,omitempty"`
	SalviaTemplateState   *string `json:"salvia_template_state,omitempty" enum:"default,forked,deleted"`
	FrameworkCurrencyDate *string `json:"framework_currency_date,omitempty"`
}

// PolicyListResponse is a paginated list of policies.
//
//nolint:revive
type PolicyListResponse struct {
	Items  []*PolicyResponse `json:"items"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateFolderInput holds validated input for creating a policy folder.
type CreateFolderInput struct {
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Name     string
}

// UpdateFolderInput holds validated input for updating a policy folder.
type UpdateFolderInput struct {
	FolderID uuid.UUID
	ClinicID uuid.UUID
	Name     string
}

// CreatePolicyInput holds validated input for creating a new policy.
type CreatePolicyInput struct {
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	FolderID    *uuid.UUID
	Name        string
	Description *string
	// Salvia-provided-content lineage — supplied only by the salvia_content
	// materialiser at clinic-create. Mutually exclusive with marketplace
	// lineage.
	SalviaTemplateID      *string
	SalviaTemplateVersion *int
	SalviaTemplateState   *string // "default" | "forked" | "deleted"
	FrameworkCurrencyDate *time.Time
}

// UpdateDraftInput holds input for updating the draft version of a policy.
type UpdateDraftInput struct {
	PolicyID    uuid.UUID
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	FolderID    *uuid.UUID
	Name        string
	Description *string
	Content     json.RawMessage
}

// PublishPolicyInput holds input for publishing the draft version.
type PublishPolicyInput struct {
	PolicyID      uuid.UUID
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	ChangeType    string
	ChangeSummary *string
	Changes       json.RawMessage
}

// RollbackPolicyInput holds input for rolling a policy back to a prior version.
type RollbackPolicyInput struct {
	PolicyID        uuid.UUID
	ClinicID        uuid.UUID
	StaffID         uuid.UUID
	TargetVersionID uuid.UUID
}

// RetirePolicyInput holds input for retiring a policy.
type RetirePolicyInput struct {
	PolicyID uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Reason   *string
}

// ListPoliciesInput holds filter and pagination for listing policies.
type ListPoliciesInput struct {
	Limit           int
	Offset          int
	FolderID        *uuid.UUID
	IncludeArchived bool
}

// UpsertClausesInput holds clause data for a replace operation.
type UpsertClausesInput struct {
	PolicyID  uuid.UUID
	ClinicID  uuid.UUID
	VersionID uuid.UUID
	Clauses   []ClauseItemInput
}

// ClauseItemInput holds a single clause to upsert.
type ClauseItemInput struct {
	BlockID        string
	Title          string
	Body           string
	Parity         string
	SourceCitation *string
}

// ── Service methods ───────────────────────────────────────────────────────────

// CreateFolder creates a new policy folder for a clinic.
func (s *Service) CreateFolder(ctx context.Context, input CreateFolderInput) (*PolicyFolderResponse, error) {
	rec, err := s.repo.CreateFolder(ctx, CreateFolderParams{
		ID:        domain.NewID(),
		ClinicID:  input.ClinicID,
		Name:      input.Name,
		CreatedBy: input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreateFolder: %w", err)
	}
	return toFolderResponse(rec), nil
}

// UpdateFolder renames a policy folder.
func (s *Service) UpdateFolder(ctx context.Context, input UpdateFolderInput) (*PolicyFolderResponse, error) {
	rec, err := s.repo.UpdateFolder(ctx, UpdateFolderParams{
		ID:       input.FolderID,
		ClinicID: input.ClinicID,
		Name:     input.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.UpdateFolder: %w", err)
	}
	return toFolderResponse(rec), nil
}

// ListFolders returns all folders for a clinic.
func (s *Service) ListFolders(ctx context.Context, clinicID uuid.UUID) (*PolicyFolderListResponse, error) {
	recs, err := s.repo.ListFolders(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.ListFolders: %w", err)
	}
	items := make([]*PolicyFolderResponse, len(recs))
	for i, r := range recs {
		items[i] = toFolderResponse(r)
	}
	return &PolicyFolderListResponse{Items: items}, nil
}

// CreatePolicy creates a new policy and an empty draft version atomically
// in a single transaction so a partial failure can never leave the policy
// in a "no draft, no published" zombie state.
func (s *Service) CreatePolicy(ctx context.Context, input CreatePolicyInput) (*PolicyResponse, error) {
	policyID := domain.NewID()

	pol, draft, err := s.repo.CreatePolicyWithDraft(ctx, CreatePolicyWithDraftParams{
		Policy: CreatePolicyParams{
			ID:                    policyID,
			ClinicID:              input.ClinicID,
			FolderID:              input.FolderID,
			Name:                  input.Name,
			Description:           input.Description,
			CreatedBy:             input.StaffID,
			SalviaTemplateID:      input.SalviaTemplateID,
			SalviaTemplateVersion: input.SalviaTemplateVersion,
			SalviaTemplateState:   input.SalviaTemplateState,
			FrameworkCurrencyDate: input.FrameworkCurrencyDate,
		},
		DraftID:      domain.NewID(),
		DraftContent: json.RawMessage(`[]`),
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreatePolicy: %w", err)
	}

	resp := toPolicyResponse(pol)
	resp.Draft = toVersionResponse(draft)
	return resp, nil
}

// GetPolicy fetches a policy with its draft (if any) and latest published version (if any).
func (s *Service) GetPolicy(ctx context.Context, policyID, clinicID uuid.UUID) (*PolicyResponse, error) {
	pol, err := s.repo.GetPolicyByID(ctx, policyID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.GetPolicy: %w", err)
	}

	resp := toPolicyResponse(pol)

	draft, err := s.repo.GetDraftVersion(ctx, policyID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("policy.service.GetPolicy: draft: %w", err)
	}
	if draft != nil {
		resp.Draft = toVersionResponse(draft)
	}

	latest, err := s.repo.GetLatestPublishedVersion(ctx, policyID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("policy.service.GetPolicy: latest published: %w", err)
	}
	if latest != nil {
		resp.LatestPublished = toVersionResponse(latest)
	}

	return resp, nil
}

// ListPolicies returns a paginated list of policies for a clinic.
func (s *Service) ListPolicies(ctx context.Context, clinicID uuid.UUID, input ListPoliciesInput) (*PolicyListResponse, error) {
	input.Limit = clampLimit(input.Limit)

	recs, total, err := s.repo.ListPolicies(ctx, clinicID, ListPoliciesParams(input))
	if err != nil {
		return nil, fmt.Errorf("policy.service.ListPolicies: %w", err)
	}

	ids := make([]uuid.UUID, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	latestByPolicy, err := s.repo.GetLatestPublishedVersions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("policy.service.ListPolicies: latest published: %w", err)
	}

	items := make([]*PolicyResponse, len(recs))
	for i, r := range recs {
		resp := toPolicyResponse(r)
		if v, ok := latestByPolicy[r.ID]; ok {
			resp.LatestPublished = toVersionResponse(v)
		}
		items[i] = resp
	}
	return &PolicyListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// UpdateDraft updates policy metadata and replaces the draft content.
// If no draft exists (e.g. after a publish), a new one is created automatically.
func (s *Service) UpdateDraft(ctx context.Context, input UpdateDraftInput) (*PolicyResponse, error) {
	pol, err := s.repo.GetPolicyByID(ctx, input.PolicyID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.UpdateDraft: %w", err)
	}
	if pol.ArchivedAt != nil {
		return nil, fmt.Errorf("policy.service.UpdateDraft: %w", domain.ErrConflict)
	}

	pol, err = s.repo.UpdatePolicyMeta(ctx, UpdatePolicyMetaParams{
		ID:          input.PolicyID,
		ClinicID:    input.ClinicID,
		FolderID:    input.FolderID,
		Name:        input.Name,
		Description: input.Description,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.UpdateDraft: meta: %w", err)
	}

	content := input.Content
	if content == nil {
		content = json.RawMessage(`[]`)
	}

	// Ensure a draft exists.
	draft, err := s.repo.GetDraftVersion(ctx, input.PolicyID)
	if errors.Is(err, domain.ErrNotFound) {
		draft, err = s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
			ID:        domain.NewID(),
			PolicyID:  input.PolicyID,
			Content:   content,
			CreatedBy: input.StaffID,
		})
	} else if err == nil {
		draft, err = s.repo.UpdateDraftContent(ctx, UpdateDraftContentParams{
			PolicyID: input.PolicyID,
			Content:  content,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("policy.service.UpdateDraft: draft: %w", err)
	}

	resp := toPolicyResponse(pol)
	resp.Draft = toVersionResponse(draft)
	return resp, nil
}

// DiscardDraft deletes the current draft of a policy.
//
// Two semantics, branched on whether the policy has ever been published:
//
//   - **Has a published version** — the draft is dropped, the latest
//     published version remains the active one. Returns
//     [domain.ErrNotFound] if there's nothing to drop.
//   - **Never published** — the entire policy row is cascade-deleted
//     along with its draft. The "discard draft" affordance doubles as
//     "delete this policy" while it's still draft-only, since there's
//     no published artefact left to keep.
//
// Retired policies always reject — use the retire workflow's restore
// path if you need to bring one back.
func (s *Service) DiscardDraft(ctx context.Context, policyID, clinicID uuid.UUID) error {
	pol, err := s.repo.GetPolicyByID(ctx, policyID, clinicID)
	if err != nil {
		return fmt.Errorf("policy.service.DiscardDraft: %w", err)
	}
	if pol.ArchivedAt != nil {
		return fmt.Errorf("policy.service.DiscardDraft: policy is retired: %w", domain.ErrConflict)
	}

	// Detect "never published" by asking for the latest published row.
	// Anything other than ErrNotFound means a real publish exists OR the
	// repo blew up — surface both as errors.
	if _, err := s.repo.GetLatestPublishedVersion(ctx, policyID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			if err := s.repo.DeletePolicyCascade(ctx, policyID, clinicID); err != nil {
				return fmt.Errorf("policy.service.DiscardDraft: cascade: %w", err)
			}
			return nil
		}
		return fmt.Errorf("policy.service.DiscardDraft: check published: %w", err)
	}

	// Has a published version — drop only the draft.
	if err := s.repo.DeleteDraftVersion(ctx, policyID); err != nil {
		return fmt.Errorf("policy.service.DiscardDraft: %w", err)
	}
	return nil
}

// PublishPolicy freezes the current draft and assigns a semver number.
func (s *Service) PublishPolicy(ctx context.Context, input PublishPolicyInput) (*PolicyVersionResponse, error) {
	pol, err := s.repo.GetPolicyByID(ctx, input.PolicyID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.PublishPolicy: %w", err)
	}
	if pol.ArchivedAt != nil {
		return nil, fmt.Errorf("policy.service.PublishPolicy: %w", domain.ErrConflict)
	}

	if _, err := s.repo.GetDraftVersion(ctx, input.PolicyID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("policy.service.PublishPolicy: no draft to publish — edit the policy first: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("policy.service.PublishPolicy: %w", err)
	}

	// Compute next semver.
	major, minor := 1, 0
	prev, err := s.repo.GetLatestPublishedVersion(ctx, input.PolicyID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("policy.service.PublishPolicy: prev version: %w", err)
	}
	if prev != nil {
		major, minor = nextVersion(input.ChangeType, prev.VersionMajor, prev.VersionMinor)
	}

	published, err := s.repo.PublishDraftVersion(ctx, PublishDraftVersionParams{
		PolicyID:      input.PolicyID,
		VersionMajor:  major,
		VersionMinor:  minor,
		ChangeType:    input.ChangeType,
		ChangeSummary: input.ChangeSummary,
		Changes:       input.Changes,
		PublishedBy:   input.StaffID,
		PublishedAt:   domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.PublishPolicy: %w", err)
	}
	return toVersionResponse(published), nil
}

// RollbackPolicy creates a new draft copied from a prior published version.
func (s *Service) RollbackPolicy(ctx context.Context, input RollbackPolicyInput) (*PolicyVersionResponse, error) {
	pol, err := s.repo.GetPolicyByID(ctx, input.PolicyID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.RollbackPolicy: %w", err)
	}
	if pol.ArchivedAt != nil {
		return nil, fmt.Errorf("policy.service.RollbackPolicy: %w", domain.ErrConflict)
	}

	target, err := s.repo.GetVersionByID(ctx, input.TargetVersionID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.RollbackPolicy: target: %w", err)
	}
	if target.PolicyID != input.PolicyID {
		return nil, fmt.Errorf("policy.service.RollbackPolicy: version belongs to different policy: %w", domain.ErrForbidden)
	}
	if target.Status != "published" {
		return nil, fmt.Errorf("policy.service.RollbackPolicy: can only rollback to published version: %w", domain.ErrConflict)
	}

	draft, err := s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
		ID:         domain.NewID(),
		PolicyID:   input.PolicyID,
		Content:    target.Content,
		RollbackOf: &input.TargetVersionID,
		CreatedBy:  input.StaffID,
	})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, fmt.Errorf("policy.service.RollbackPolicy: discard existing draft before rollback: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("policy.service.RollbackPolicy: create draft: %w", err)
	}
	return toVersionResponse(draft), nil
}

// RetirePolicy archives a policy and removes it from all linked forms.
func (s *Service) RetirePolicy(ctx context.Context, input RetirePolicyInput) (*PolicyResponse, error) {
	pol, err := s.repo.GetPolicyByID(ctx, input.PolicyID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.RetirePolicy: %w", err)
	}
	if pol.ArchivedAt != nil {
		return nil, fmt.Errorf("policy.service.RetirePolicy: already retired: %w", domain.ErrConflict)
	}

	retired, err := s.repo.RetirePolicy(ctx, RetirePolicyParams{
		ID:           input.PolicyID,
		ClinicID:     input.ClinicID,
		RetireReason: input.Reason,
		ArchivedAt:   domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.RetirePolicy: %w", err)
	}

	// Detach from all linked forms. Best-effort: log but don't fail the retire.
	if err := s.formLinker.DetachPolicyFromForms(ctx, input.PolicyID, retired.Name, input.Reason); err != nil {
		// Non-fatal — the policy is already retired. Forms will surface stale
		// links on next load; cleanup can be retried manually.
		_ = err
	}

	return toPolicyResponse(retired), nil
}

// ListVersions returns published version history for a policy.
func (s *Service) ListVersions(ctx context.Context, policyID, clinicID uuid.UUID) (*PolicyVersionListResponse, error) {
	if _, err := s.repo.GetPolicyByID(ctx, policyID, clinicID); err != nil {
		return nil, fmt.Errorf("policy.service.ListVersions: %w", err)
	}
	versions, err := s.repo.ListPublishedVersions(ctx, policyID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.ListVersions: %w", err)
	}
	items := make([]*PolicyVersionResponse, len(versions))
	for i, v := range versions {
		items[i] = toVersionResponse(v)
	}
	return &PolicyVersionListResponse{Items: items}, nil
}

// GetVersion returns a specific version, verifying clinic ownership.
func (s *Service) GetVersion(ctx context.Context, policyID, clinicID, versionID uuid.UUID) (*PolicyVersionResponse, error) {
	if _, err := s.repo.GetPolicyByID(ctx, policyID, clinicID); err != nil {
		return nil, fmt.Errorf("policy.service.GetVersion: %w", err)
	}
	ver, err := s.repo.GetVersionByID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.GetVersion: %w", err)
	}
	if ver.PolicyID != policyID {
		return nil, fmt.Errorf("policy.service.GetVersion: %w", domain.ErrForbidden)
	}
	return toVersionResponse(ver), nil
}

// UpsertClauses replaces all clauses on a policy version.
// The version must belong to the given policy and clinic.
func (s *Service) UpsertClauses(ctx context.Context, input UpsertClausesInput) (*PolicyClauseListResponse, error) {
	if _, err := s.repo.GetPolicyByID(ctx, input.PolicyID, input.ClinicID); err != nil {
		return nil, fmt.Errorf("policy.service.UpsertClauses: %w", err)
	}
	ver, err := s.repo.GetVersionByID(ctx, input.VersionID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.UpsertClauses: version: %w", err)
	}
	if ver.PolicyID != input.PolicyID {
		return nil, fmt.Errorf("policy.service.UpsertClauses: %w", domain.ErrForbidden)
	}

	params := make([]ClauseInput, len(input.Clauses))
	for i, c := range input.Clauses {
		params[i] = ClauseInput(c)
	}

	recs, err := s.repo.ReplaceClauses(ctx, input.VersionID, params)
	if err != nil {
		return nil, fmt.Errorf("policy.service.UpsertClauses: %w", err)
	}
	return toClauseListResponse(recs), nil
}

// ListClauses returns all clauses for a policy version.
func (s *Service) ListClauses(ctx context.Context, policyID, clinicID, versionID uuid.UUID) (*PolicyClauseListResponse, error) {
	if _, err := s.repo.GetPolicyByID(ctx, policyID, clinicID); err != nil {
		return nil, fmt.Errorf("policy.service.ListClauses: %w", err)
	}
	ver, err := s.repo.GetVersionByID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.ListClauses: version: %w", err)
	}
	if ver.PolicyID != policyID {
		return nil, fmt.Errorf("policy.service.ListClauses: %w", domain.ErrForbidden)
	}

	recs, err := s.repo.ListClauses(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.ListClauses: %w", err)
	}
	return toClauseListResponse(recs), nil
}

// ── Marketplace import ───────────────────────────────────────────────────────

// ImportFromMarketplaceInput is the input for creating a tenant policy from
// a marketplace package. The content JSONB and clause block_ids are preserved
// verbatim so form extraction alignment continues to work post-import.
type ImportFromMarketplaceInput struct {
	ClinicID                   uuid.UUID
	StaffID                    uuid.UUID
	SourceMarketplaceVersionID uuid.UUID
	Name                       string
	Description                *string
	Content                    json.RawMessage
	Clauses                    []ClauseInput
	ChangeSummary              string
}

// ImportFromMarketplace materialises a marketplace policy snapshot into the
// tenant: creates the policy row stamped with source_marketplace_version_id,
// creates a draft version with the content + clauses, then publishes it as v1.0.
// Returns the new policy ID.
func (s *Service) ImportFromMarketplace(ctx context.Context, input ImportFromMarketplaceInput) (uuid.UUID, error) {
	policyID := domain.NewID()
	content := input.Content
	if content == nil {
		content = json.RawMessage(`[]`)
	}
	_, draft, err := s.repo.CreatePolicyWithDraft(ctx, CreatePolicyWithDraftParams{
		Policy: CreatePolicyParams{
			ID:                         policyID,
			ClinicID:                   input.ClinicID,
			Name:                       input.Name,
			Description:                input.Description,
			CreatedBy:                  input.StaffID,
			SourceMarketplaceVersionID: &input.SourceMarketplaceVersionID,
		},
		DraftID:      domain.NewID(),
		DraftContent: content,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("policy.service.ImportFromMarketplace: %w", err)
	}

	if _, err := s.repo.ReplaceClauses(ctx, draft.ID, input.Clauses); err != nil {
		return uuid.Nil, fmt.Errorf("policy.service.ImportFromMarketplace: clauses: %w", err)
	}

	summary := input.ChangeSummary
	summaryPtr := &summary
	if _, err := s.repo.PublishDraftVersion(ctx, PublishDraftVersionParams{
		PolicyID:      policyID,
		VersionMajor:  1,
		VersionMinor:  0,
		ChangeType:    "major",
		ChangeSummary: summaryPtr,
		PublishedBy:   input.StaffID,
		PublishedAt:   domain.TimeNow(),
	}); err != nil {
		return uuid.Nil, fmt.Errorf("policy.service.ImportFromMarketplace: publish: %w", err)
	}

	return policyID, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func nextVersion(changeType string, prevMajor, prevMinor *int) (major, minor int) {
	if prevMajor == nil || prevMinor == nil {
		return 1, 0
	}
	if changeType == "major" {
		return *prevMajor + 1, 0
	}
	return *prevMajor, *prevMinor + 1
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func toFolderResponse(r *PolicyFolderRecord) *PolicyFolderResponse {
	return &PolicyFolderResponse{
		ID:        r.ID.String(),
		ClinicID:  r.ClinicID.String(),
		Name:      r.Name,
		CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
}

func toPolicyResponse(p *PolicyRecord) *PolicyResponse {
	r := &PolicyResponse{
		ID:           p.ID.String(),
		ClinicID:     p.ClinicID.String(),
		Name:         p.Name,
		Description:  p.Description,
		CreatedBy:    p.CreatedBy.String(),
		CreatedAt:    p.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    p.UpdatedAt.Format(time.RFC3339),
		RetireReason: p.RetireReason,
	}
	if p.FolderID != nil {
		s := p.FolderID.String()
		r.FolderID = &s
	}
	if p.ArchivedAt != nil {
		s := p.ArchivedAt.Format(time.RFC3339)
		r.ArchivedAt = &s
	}
	r.SalviaTemplateID = p.SalviaTemplateID
	r.SalviaTemplateVersion = p.SalviaTemplateVersion
	r.SalviaTemplateState = p.SalviaTemplateState
	if p.FrameworkCurrencyDate != nil {
		s := p.FrameworkCurrencyDate.Format("2006-01-02")
		r.FrameworkCurrencyDate = &s
	}
	return r
}

func toVersionResponse(v *PolicyVersionRecord) *PolicyVersionResponse {
	changes := v.Changes
	if changes == nil {
		changes = json.RawMessage(`[]`)
	}
	r := &PolicyVersionResponse{
		ID:            v.ID.String(),
		PolicyID:      v.PolicyID.String(),
		Status:        v.Status,
		VersionMajor:  v.VersionMajor,
		VersionMinor:  v.VersionMinor,
		ChangeType:    v.ChangeType,
		ChangeSummary: v.ChangeSummary,
		Changes:       changes,
		Content:       v.Content,
		CreatedAt:     v.CreatedAt.Format(time.RFC3339),
	}
	if v.RollbackOf != nil {
		s := v.RollbackOf.String()
		r.RollbackOf = &s
	}
	if v.PublishedAt != nil {
		s := v.PublishedAt.Format(time.RFC3339)
		r.PublishedAt = &s
	}
	if v.PublishedBy != nil {
		s := v.PublishedBy.String()
		r.PublishedBy = &s
	}
	if len(v.GenerationMetadata) > 0 {
		r.GenerationMetadata = v.GenerationMetadata
	}
	return r
}

func toClauseListResponse(recs []*PolicyClauseRecord) *PolicyClauseListResponse {
	items := make([]*PolicyClauseResponse, len(recs))
	for i, r := range recs {
		items[i] = &PolicyClauseResponse{
			ID:             r.ID.String(),
			BlockID:        r.BlockID,
			Title:          r.Title,
			Body:           r.Body,
			Parity:         r.Parity,
			SourceCitation: r.SourceCitation,
		}
	}
	return &PolicyClauseListResponse{Items: items}
}
