package forms

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires forms HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new forms Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared input types ────────────────────────────────────────────────────────

type formIDInput struct {
	FormID string `path:"form_id" doc:"The form's UUID."`
}

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Number of results to skip."`
}

// ── Field input type ──────────────────────────────────────────────────────────

type fieldBodyInput struct {
	Position  int             `json:"position" minimum:"1" doc:"1-based display order."`
	Title     string          `json:"title"    minLength:"1" doc:"Display label for the field."`
	Type      string          `json:"type"     minLength:"1" doc:"Field type (text, long_text, number, decimal, slider, select, button_group, percentage, image, etc.)."`
	Config    json.RawMessage `json:"config,omitempty"  doc:"Type-specific configuration (options, range bounds, etc.)."`
	AIPrompt  *string         `json:"ai_prompt,omitempty" doc:"Optional per-field prompt for AI extraction."`
	Required  bool            `json:"required"   doc:"Whether the field must be filled by AI or reviewer."`
	Skippable bool            `json:"skippable"  doc:"If true, this field is excluded from AI extraction entirely."`
}

// ── Forms ─────────────────────────────────────────────────────────────────────

type createFormBodyInput struct {
	Body struct {
		GroupID       *string          `json:"group_id,omitempty"    doc:"UUID of the group to place this form in."`
		Name          string           `json:"name"                  minLength:"1" doc:"Form name."`
		Description   *string          `json:"description,omitempty" doc:"Optional form description."`
		OverallPrompt *string          `json:"overall_prompt,omitempty" doc:"Context for the AI extraction model about this form's purpose."`
		Tags          []string         `json:"tags,omitempty"        doc:"Free-form label strings."`
		Fields        []fieldBodyInput `json:"fields,omitempty"      doc:"Initial field definitions (can be set or updated later)."`
	}
}

type formHTTPResponse struct {
	Body *FormResponse
}

type formListHTTPResponse struct {
	Body *FormListResponse
}

type listFormsInput struct {
	paginationInput
	GroupID         *string `query:"group_id"          doc:"Filter by group UUID."`
	IncludeArchived bool    `query:"include_archived"  doc:"Include retired forms in results."`
	Tag             *string `query:"tag"               doc:"Filter forms that have this tag."`
}

// createForm handles POST /api/v1/forms.
func (h *Handler) createForm(ctx context.Context, input *createFormBodyInput) (*formHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	svcInput := CreateFormInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		Name:          input.Body.Name,
		Description:   input.Body.Description,
		OverallPrompt: input.Body.OverallPrompt,
		Tags:          input.Body.Tags,
	}
	if input.Body.GroupID != nil {
		id, err := uuid.Parse(*input.Body.GroupID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid group_id")
		}
		svcInput.GroupID = &id
	}

	resp, err := h.svc.CreateForm(ctx, svcInput)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: resp}, nil
}

// getForm handles GET /api/v1/forms/{form_id}.
func (h *Handler) getForm(ctx context.Context, input *formIDInput) (*formHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	resp, err := h.svc.GetForm(ctx, formID, clinicID)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: resp}, nil
}

// listForms handles GET /api/v1/forms.
func (h *Handler) listForms(ctx context.Context, input *listFormsInput) (*formListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	svcInput := ListFormsInput{
		Limit:           input.Limit,
		Offset:          input.Offset,
		IncludeArchived: input.IncludeArchived,
		Tag:             input.Tag,
	}
	if input.GroupID != nil {
		id, err := uuid.Parse(*input.GroupID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid group_id")
		}
		svcInput.GroupID = &id
	}

	resp, err := h.svc.ListForms(ctx, clinicID, svcInput)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formListHTTPResponse{Body: resp}, nil
}

// ── Draft update ──────────────────────────────────────────────────────────────

type updateDraftBodyInput struct {
	FormID string `path:"form_id"`
	Body   struct {
		GroupID       *string          `json:"group_id,omitempty"`
		Name          string           `json:"name"          minLength:"1"`
		Description   *string          `json:"description,omitempty"`
		OverallPrompt *string          `json:"overall_prompt,omitempty"`
		Tags          []string         `json:"tags,omitempty"`
		Fields        []fieldBodyInput `json:"fields" doc:"Full replacement field list. Ordering reflects the position values."`
	}
}

type versionHTTPResponse struct {
	Body *FormVersionResponse
}

// updateDraft handles PUT /api/v1/forms/{form_id}/draft.
func (h *Handler) updateDraft(ctx context.Context, input *updateDraftBodyInput) (*formHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	var groupID *uuid.UUID
	if input.Body.GroupID != nil {
		id, err := uuid.Parse(*input.Body.GroupID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid group_id")
		}
		groupID = &id
	}

	fields := make([]FieldInput, len(input.Body.Fields))
	for i, f := range input.Body.Fields {
		fields[i] = FieldInput(f)
	}

	resp, err := h.svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:        formID,
		ClinicID:      clinicID,
		StaffID:       staffID,
		GroupID:       groupID,
		Name:          input.Body.Name,
		Description:   input.Body.Description,
		OverallPrompt: input.Body.OverallPrompt,
		Tags:          input.Body.Tags,
		Fields:        fields,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: resp}, nil
}

// ── Publish ───────────────────────────────────────────────────────────────────

type publishFormBodyInput struct {
	FormID string `path:"form_id"`
	Body   struct {
		ChangeType    string  `json:"change_type" enum:"minor,major" doc:"Semver bump type."`
		ChangeSummary *string `json:"change_summary,omitempty" doc:"Human-readable summary of what changed."`
	}
}

// publishForm handles POST /api/v1/forms/{form_id}/publish.
func (h *Handler) publishForm(ctx context.Context, input *publishFormBodyInput) (*versionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	resp, err := h.svc.PublishForm(ctx, PublishFormInput{
		FormID:        formID,
		ClinicID:      clinicID,
		StaffID:       staffID,
		ChangeType:    domain.ChangeType(input.Body.ChangeType),
		ChangeSummary: input.Body.ChangeSummary,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &versionHTTPResponse{Body: resp}, nil
}

// ── Policy check ──────────────────────────────────────────────────────────────

// policyCheckForm handles POST /api/v1/forms/{form_id}/policy-check.
func (h *Handler) policyCheckForm(ctx context.Context, input *formIDInput) (*versionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	resp, err := h.svc.RunPolicyCheck(ctx, formID, clinicID, staffID)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &versionHTTPResponse{Body: resp}, nil
}

// ── Rollback ──────────────────────────────────────────────────────────────────

type rollbackFormBodyInput struct {
	FormID string `path:"form_id"`
	Body   struct {
		TargetVersionID string  `json:"target_version_id" doc:"UUID of the published version to rollback to."`
		Reason          *string `json:"reason,omitempty"  doc:"Optional reason recorded in the version history."`
	}
}

// rollbackForm handles POST /api/v1/forms/{form_id}/rollback.
func (h *Handler) rollbackForm(ctx context.Context, input *rollbackFormBodyInput) (*versionHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}
	targetID, err := uuid.Parse(input.Body.TargetVersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid target_version_id")
	}

	resp, err := h.svc.RollbackForm(ctx, RollbackFormInput{
		FormID:          formID,
		ClinicID:        clinicID,
		StaffID:         staffID,
		TargetVersionID: targetID,
		Reason:          input.Body.Reason,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &versionHTTPResponse{Body: resp}, nil
}

// ── Retire ────────────────────────────────────────────────────────────────────

type retireFormBodyInput struct {
	FormID string `path:"form_id"`
	Body   struct {
		Reason *string `json:"reason,omitempty" doc:"Reason for retiring this form, recorded in the audit trail."`
	}
}

// retireForm handles POST /api/v1/forms/{form_id}/retire.
func (h *Handler) retireForm(ctx context.Context, input *retireFormBodyInput) (*formHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	resp, err := h.svc.RetireForm(ctx, RetireFormInput{
		FormID:   formID,
		ClinicID: clinicID,
		StaffID:  staffID,
		Reason:   input.Body.Reason,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: resp}, nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

type versionListHTTPResponse struct {
	Body *FormVersionListResponse
}

// listVersions handles GET /api/v1/forms/{form_id}/versions.
func (h *Handler) listVersions(ctx context.Context, input *formIDInput) (*versionListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	resp, err := h.svc.ListVersions(ctx, formID, clinicID)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &versionListHTTPResponse{Body: resp}, nil
}

// ── Groups ────────────────────────────────────────────────────────────────────

type createGroupBodyInput struct {
	Body struct {
		Name        string  `json:"name"                  minLength:"1" doc:"Group name."`
		Description *string `json:"description,omitempty" doc:"Optional group description."`
	}
}

type groupHTTPResponse struct {
	Body *FormGroupResponse
}

type groupListHTTPResponse struct {
	Body *FormGroupListResponse
}

type updateGroupBodyInput struct {
	GroupID string `path:"group_id"`
	Body    struct {
		Name        string  `json:"name"                  minLength:"1"`
		Description *string `json:"description,omitempty"`
	}
}

// createGroup handles POST /api/v1/form-groups.
func (h *Handler) createGroup(ctx context.Context, input *createGroupBodyInput) (*groupHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	resp, err := h.svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID:    clinicID,
		StaffID:     staffID,
		Name:        input.Body.Name,
		Description: input.Body.Description,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &groupHTTPResponse{Body: resp}, nil
}

// listGroups handles GET /api/v1/form-groups.
func (h *Handler) listGroups(ctx context.Context, _ *struct{}) (*groupListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	resp, err := h.svc.ListGroups(ctx, clinicID)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &groupListHTTPResponse{Body: resp}, nil
}

// updateGroup handles PATCH /api/v1/form-groups/{group_id}.
func (h *Handler) updateGroup(ctx context.Context, input *updateGroupBodyInput) (*groupHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	groupID, err := uuid.Parse(input.GroupID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid group_id")
	}

	resp, err := h.svc.UpdateGroup(ctx, UpdateGroupInput{
		GroupID:     groupID,
		ClinicID:    clinicID,
		Name:        input.Body.Name,
		Description: input.Body.Description,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &groupHTTPResponse{Body: resp}, nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

type linkPolicyBodyInput struct {
	FormID string `path:"form_id"`
	Body   struct {
		PolicyID string `json:"policy_id" doc:"UUID of the policy to link."`
	}
}

type policyIDInput struct {
	FormID   string `path:"form_id"`
	PolicyID string `path:"policy_id"`
}

type linkedPoliciesResponse struct {
	Body struct {
		PolicyIDs []string `json:"policy_ids"`
	}
}

// listLinkedPolicies handles GET /api/v1/forms/{form_id}/policies.
func (h *Handler) listLinkedPolicies(ctx context.Context, input *formIDInput) (*linkedPoliciesResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	ids, err := h.svc.ListLinkedPolicies(ctx, formID, clinicID)
	if err != nil {
		return nil, mapFormError(err)
	}

	resp := &linkedPoliciesResponse{}
	resp.Body.PolicyIDs = ids
	if ids == nil {
		resp.Body.PolicyIDs = []string{}
	}
	return resp, nil
}

// linkPolicy handles POST /api/v1/forms/{form_id}/policies.
func (h *Handler) linkPolicy(ctx context.Context, input *linkPolicyBodyInput) (*struct{}, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}
	policyID, err := uuid.Parse(input.Body.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	if err := h.svc.LinkPolicy(ctx, formID, clinicID, policyID, staffID); err != nil {
		return nil, mapFormError(err)
	}
	return &struct{}{}, nil
}

// unlinkPolicy handles DELETE /api/v1/forms/{form_id}/policies/{policy_id}.
func (h *Handler) unlinkPolicy(ctx context.Context, input *policyIDInput) (*struct{}, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}
	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	if err := h.svc.UnlinkPolicy(ctx, formID, clinicID, policyID); err != nil {
		return nil, mapFormError(err)
	}
	return &struct{}{}, nil
}

// ── Style ─────────────────────────────────────────────────────────────────────

type styleHTTPResponse struct {
	Body *FormStyleResponse
}

type updateStyleBodyInput struct {
	Body struct {
		LogoKey      *string `json:"logo_key,omitempty"      doc:"Object-storage key for the clinic logo image."`
		PrimaryColor *string `json:"primary_color,omitempty" pattern:"^#[0-9A-Fa-f]{6}$" doc:"Hex colour e.g. #3B82F6."`
		FontFamily   *string `json:"font_family,omitempty"   doc:"Font family name recognised by the Flutter PDF renderer."`
		HeaderExtra  *string `json:"header_extra,omitempty"  doc:"Extra header text shown below the clinic name/logo."`
		FooterText   *string `json:"footer_text,omitempty"   doc:"Custom footer text; form version and approver are appended automatically."`
	}
}

// getStyle handles GET /api/v1/clinic/form-style.
func (h *Handler) getStyle(ctx context.Context, _ *struct{}) (*styleHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	resp, err := h.svc.GetCurrentStyle(ctx, clinicID)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &styleHTTPResponse{Body: resp}, nil
}

// updateStyle handles PUT /api/v1/clinic/form-style.
func (h *Handler) updateStyle(ctx context.Context, input *updateStyleBodyInput) (*styleHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	resp, err := h.svc.UpdateStyle(ctx, UpdateStyleInput{
		ClinicID:     clinicID,
		StaffID:      staffID,
		LogoKey:      input.Body.LogoKey,
		PrimaryColor: input.Body.PrimaryColor,
		FontFamily:   input.Body.FontFamily,
		HeaderExtra:  input.Body.HeaderExtra,
		FooterText:   input.Body.FooterText,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &styleHTTPResponse{Body: resp}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapFormError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
