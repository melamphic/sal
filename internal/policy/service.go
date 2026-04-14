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
type FormLinker interface {
	DetachPolicyFromForms(ctx context.Context, policyID uuid.UUID) error
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
	Parity  string `json:"parity"`
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
	Content       json.RawMessage `json:"content"`
	RollbackOf    *string         `json:"rollback_of,omitempty"`
	PublishedAt   *string         `json:"published_at,omitempty"`
	PublishedBy   *string         `json:"published_by,omitempty"`
	CreatedAt     string          `json:"created_at"`
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
	BlockID string
	Title   string
	Parity  string
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

// CreatePolicy creates a new policy and an empty draft version.
func (s *Service) CreatePolicy(ctx context.Context, input CreatePolicyInput) (*PolicyResponse, error) {
	policyID := domain.NewID()

	pol, err := s.repo.CreatePolicy(ctx, CreatePolicyParams{
		ID:          policyID,
		ClinicID:    input.ClinicID,
		FolderID:    input.FolderID,
		Name:        input.Name,
		Description: input.Description,
		CreatedBy:   input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreatePolicy: %w", err)
	}

	draft, err := s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
		ID:        domain.NewID(),
		PolicyID:  policyID,
		Content:   json.RawMessage(`[]`),
		CreatedBy: input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreatePolicy: draft version: %w", err)
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

	items := make([]*PolicyResponse, len(recs))
	for i, r := range recs {
		items[i] = toPolicyResponse(r)
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
		return nil, fmt.Errorf("policy.service.PublishPolicy: no draft: %w", err)
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
	if err := s.formLinker.DetachPolicyFromForms(ctx, input.PolicyID); err != nil {
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
	return r
}

func toVersionResponse(v *PolicyVersionRecord) *PolicyVersionResponse {
	r := &PolicyVersionResponse{
		ID:            v.ID.String(),
		PolicyID:      v.PolicyID.String(),
		Status:        v.Status,
		VersionMajor:  v.VersionMajor,
		VersionMinor:  v.VersionMinor,
		ChangeType:    v.ChangeType,
		ChangeSummary: v.ChangeSummary,
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
	return r
}

func toClauseListResponse(recs []*PolicyClauseRecord) *PolicyClauseListResponse {
	items := make([]*PolicyClauseResponse, len(recs))
	for i, r := range recs {
		items[i] = &PolicyClauseResponse{
			ID:      r.ID.String(),
			BlockID: r.BlockID,
			Title:   r.Title,
			Parity:  r.Parity,
		}
	}
	return &PolicyClauseListResponse{Items: items}
}
