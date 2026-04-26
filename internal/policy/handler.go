package policy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires policy HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new policy Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Folders ───────────────────────────────────────────────────────────────────

type createFolderBody struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"255" doc:"Folder name."`
	}
}

type policyFolderHTTPResponse struct {
	Body *PolicyFolderResponse
}

type policyFolderListHTTPResponse struct {
	Body *PolicyFolderListResponse
}

func (h *Handler) createFolder(ctx context.Context, input *createFolderBody) (*policyFolderHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	resp, err := h.svc.CreateFolder(ctx, CreateFolderInput{
		ClinicID: clinicID,
		StaffID:  staffID,
		Name:     input.Body.Name,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyFolderHTTPResponse{Body: resp}, nil
}

type updateFolderInput struct {
	FolderID string `path:"folder_id" doc:"Folder UUID."`
	Body     struct {
		Name string `json:"name" minLength:"1" maxLength:"255"`
	}
}

func (h *Handler) updateFolder(ctx context.Context, input *updateFolderInput) (*policyFolderHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	folderID, err := uuid.Parse(input.FolderID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid folder_id")
	}

	resp, err := h.svc.UpdateFolder(ctx, UpdateFolderInput{
		FolderID: folderID,
		ClinicID: clinicID,
		Name:     input.Body.Name,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyFolderHTTPResponse{Body: resp}, nil
}

type listFoldersInput struct{}

func (h *Handler) listFolders(ctx context.Context, _ *listFoldersInput) (*policyFolderListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	resp, err := h.svc.ListFolders(ctx, clinicID)
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyFolderListHTTPResponse{Body: resp}, nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

type policyHTTPResponse struct {
	Body *PolicyResponse
}

type policyListHTTPResponse struct {
	Body *PolicyListResponse
}

type createPolicyBody struct {
	Body struct {
		Name        string  `json:"name"                  minLength:"1" maxLength:"255" doc:"Policy name."`
		Description *string `json:"description,omitempty"                              doc:"Optional description."`
		FolderID    *string `json:"folder_id,omitempty"                                doc:"Optional folder UUID."`
	}
}

func (h *Handler) createPolicy(ctx context.Context, input *createPolicyBody) (*policyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	var folderID *uuid.UUID
	if input.Body.FolderID != nil {
		id, err := uuid.Parse(*input.Body.FolderID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid folder_id")
		}
		folderID = &id
	}

	resp, err := h.svc.CreatePolicy(ctx, CreatePolicyInput{
		ClinicID:    clinicID,
		StaffID:     staffID,
		FolderID:    folderID,
		Name:        input.Body.Name,
		Description: input.Body.Description,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyHTTPResponse{Body: resp}, nil
}

type getPolicyInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
}

func (h *Handler) getPolicy(ctx context.Context, input *getPolicyInput) (*policyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	resp, err := h.svc.GetPolicy(ctx, policyID, clinicID)
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyHTTPResponse{Body: resp}, nil
}

type listPoliciesInput struct {
	Limit           int    `query:"limit"            minimum:"1" maximum:"100" default:"20"`
	Offset          int    `query:"offset"           minimum:"0"               default:"0"`
	FolderID        string `query:"folder_id"        doc:"Filter by folder UUID."`
	IncludeArchived bool   `query:"include_archived" doc:"Include retired policies."`
}

func (h *Handler) listPolicies(ctx context.Context, input *listPoliciesInput) (*policyListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	var folderID *uuid.UUID
	if input.FolderID != "" {
		id, err := uuid.Parse(input.FolderID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid folder_id")
		}
		folderID = &id
	}

	resp, err := h.svc.ListPolicies(ctx, clinicID, ListPoliciesInput{
		Limit:           input.Limit,
		Offset:          input.Offset,
		FolderID:        folderID,
		IncludeArchived: input.IncludeArchived,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyListHTTPResponse{Body: resp}, nil
}

// ── Draft ─────────────────────────────────────────────────────────────────────

// PolicyContent is a JSON array of AppFlowy-compatible editor blocks.
// The backend treats it as opaque; rendering is handled by the Flutter client.
type PolicyContent = []map[string]interface{}

type updateDraftInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
	Body     struct {
		Name        string        `json:"name"                  minLength:"1" maxLength:"255"`
		Description *string       `json:"description,omitempty"`
		FolderID    *string       `json:"folder_id,omitempty"`
		Content     PolicyContent `json:"content"               doc:"AppFlowy-compatible block array."`
	}
}

type policyVersionHTTPResponse struct {
	Body *PolicyVersionResponse
}

func (h *Handler) updateDraft(ctx context.Context, input *updateDraftInput) (*policyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	var folderID *uuid.UUID
	if input.Body.FolderID != nil {
		id, err := uuid.Parse(*input.Body.FolderID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid folder_id")
		}
		folderID = &id
	}

	contentJSON, err := json.Marshal(input.Body.Content)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid content")
	}

	resp, err := h.svc.UpdateDraft(ctx, UpdateDraftInput{
		PolicyID:    policyID,
		ClinicID:    clinicID,
		StaffID:     staffID,
		FolderID:    folderID,
		Name:        input.Body.Name,
		Description: input.Body.Description,
		Content:     contentJSON,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyHTTPResponse{Body: resp}, nil
}

// ── Publish ───────────────────────────────────────────────────────────────────

type publishPolicyInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
	Body     struct {
		ChangeType    string  `json:"change_type"              enum:"minor,major" doc:"Version bump type."`
		ChangeSummary *string `json:"change_summary,omitempty"                   doc:"Optional summary of changes."`
	}
}

func (h *Handler) publishPolicy(ctx context.Context, input *publishPolicyInput) (*policyVersionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	resp, err := h.svc.PublishPolicy(ctx, PublishPolicyInput{
		PolicyID:      policyID,
		ClinicID:      clinicID,
		StaffID:       staffID,
		ChangeType:    input.Body.ChangeType,
		ChangeSummary: input.Body.ChangeSummary,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyVersionHTTPResponse{Body: resp}, nil
}

// ── Discard draft ─────────────────────────────────────────────────────────────

type discardDraftInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
}

type emptyHTTPResponse struct{}

// discardDraft handles DELETE /api/v1/policies/{policy_id}/draft.
func (h *Handler) discardDraft(ctx context.Context, input *discardDraftInput) (*emptyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	if err := h.svc.DiscardDraft(ctx, policyID, clinicID); err != nil {
		return nil, mapPolicyError(err)
	}
	return &emptyHTTPResponse{}, nil
}

// ── Rollback ──────────────────────────────────────────────────────────────────

type rollbackPolicyInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
	Body     struct {
		TargetVersionID string `json:"target_version_id" doc:"Published version UUID to roll back to."`
	}
}

func (h *Handler) rollbackPolicy(ctx context.Context, input *rollbackPolicyInput) (*policyVersionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}
	targetID, err := uuid.Parse(input.Body.TargetVersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid target_version_id")
	}

	resp, err := h.svc.RollbackPolicy(ctx, RollbackPolicyInput{
		PolicyID:        policyID,
		ClinicID:        clinicID,
		StaffID:         staffID,
		TargetVersionID: targetID,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyVersionHTTPResponse{Body: resp}, nil
}

// ── Retire ────────────────────────────────────────────────────────────────────

type retirePolicyInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
	Body     struct {
		Reason *string `json:"reason,omitempty" doc:"Optional reason for retiring the policy."`
	}
}

func (h *Handler) retirePolicy(ctx context.Context, input *retirePolicyInput) (*policyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	resp, err := h.svc.RetirePolicy(ctx, RetirePolicyInput{
		PolicyID: policyID,
		ClinicID: clinicID,
		StaffID:  staffID,
		Reason:   input.Body.Reason,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyHTTPResponse{Body: resp}, nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

type policyVersionListHTTPResponse struct {
	Body *PolicyVersionListResponse
}

type listVersionsInput struct {
	PolicyID string `path:"policy_id" doc:"Policy UUID."`
}

func (h *Handler) listVersions(ctx context.Context, input *listVersionsInput) (*policyVersionListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	resp, err := h.svc.ListVersions(ctx, policyID, clinicID)
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyVersionListHTTPResponse{Body: resp}, nil
}

type getVersionInput struct {
	PolicyID  string `path:"policy_id"  doc:"Policy UUID."`
	VersionID string `path:"version_id" doc:"Version UUID."`
}

func (h *Handler) getVersion(ctx context.Context, input *getVersionInput) (*policyVersionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}
	versionID, err := uuid.Parse(input.VersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid version_id")
	}

	resp, err := h.svc.GetVersion(ctx, policyID, clinicID, versionID)
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyVersionHTTPResponse{Body: resp}, nil
}

// ── Clauses ───────────────────────────────────────────────────────────────────

type policyClauseListHTTPResponse struct {
	Body *PolicyClauseListResponse
}

type upsertClausesInput struct {
	PolicyID  string `path:"policy_id"  doc:"Policy UUID."`
	VersionID string `path:"version_id" doc:"Version UUID."`
	Body      struct {
		Clauses []clauseInputItem `json:"clauses" doc:"Full replacement list of clauses for this version."`
	}
}

type clauseInputItem struct {
	BlockID string `json:"block_id"                   minLength:"1" doc:"Client-assigned block identifier within the content JSONB."`
	Title   string `json:"title"                      minLength:"1" doc:"Human-readable clause title."`
	Body    string `json:"body"                                    doc:"Optional rich body using a lightweight markdown subset (bold/italic/bullets)."`
	Parity  string `json:"parity"  enum:"high,medium,low"          doc:"Enforcement level: high=must, medium=should, low=try."`
}

func (h *Handler) upsertClauses(ctx context.Context, input *upsertClausesInput) (*policyClauseListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}
	versionID, err := uuid.Parse(input.VersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid version_id")
	}

	clauses := make([]ClauseItemInput, len(input.Body.Clauses))
	for i, c := range input.Body.Clauses {
		clauses[i] = ClauseItemInput(c)
	}

	resp, err := h.svc.UpsertClauses(ctx, UpsertClausesInput{
		PolicyID:  policyID,
		ClinicID:  clinicID,
		VersionID: versionID,
		Clauses:   clauses,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyClauseListHTTPResponse{Body: resp}, nil
}

type listClausesInput struct {
	PolicyID  string `path:"policy_id"  doc:"Policy UUID."`
	VersionID string `path:"version_id" doc:"Version UUID."`
}

func (h *Handler) listClauses(ctx context.Context, input *listClausesInput) (*policyClauseListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}
	versionID, err := uuid.Parse(input.VersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid version_id")
	}

	resp, err := h.svc.ListClauses(ctx, policyID, clinicID, versionID)
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyClauseListHTTPResponse{Body: resp}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapPolicyError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		slog.Warn("policy: not found", "error", err.Error())
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("conflict")
	default:
		slog.Error("policy: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}
