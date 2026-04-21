package forms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"

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
	Position       int             `json:"position" minimum:"1" doc:"1-based display order."`
	Title          string          `json:"title"    minLength:"1" doc:"Display label for the field."`
	Type           string          `json:"type"     minLength:"1" doc:"Field type (text, long_text, number, decimal, slider, select, button_group, percentage, image, etc.)."`
	Config         json.RawMessage `json:"config,omitempty"  doc:"Type-specific configuration (options, range bounds, etc.)."`
	AIPrompt       *string         `json:"ai_prompt,omitempty" doc:"Optional per-field prompt for AI extraction."`
	Required       bool            `json:"required"   doc:"Whether the field must be filled by AI or reviewer."`
	Skippable      bool            `json:"skippable"  doc:"If true, this field is excluded from AI extraction entirely."`
	AllowInference bool            `json:"allow_inference" doc:"When false, AI inference is rejected; only verbatim quotes accepted."`
	MinConfidence  *float64        `json:"min_confidence,omitempty" doc:"ASR confidence floor (0.0–1.0). Results below this threshold are flagged for review."`
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
	GroupID         string `query:"group_id"          doc:"Filter by group UUID."`
	IncludeArchived bool   `query:"include_archived"  doc:"Include retired forms in results."`
	Tag             string `query:"tag"               doc:"Filter forms that have this tag."`
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
	}
	if input.Tag != "" {
		svcInput.Tag = &input.Tag
	}
	if input.GroupID != "" {
		id, err := uuid.Parse(input.GroupID)
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
		ChangeType    string          `json:"change_type" enum:"minor,major" doc:"Semver bump type."`
		ChangeSummary *string         `json:"change_summary,omitempty" doc:"Human-readable summary of what changed."`
		Changes       json.RawMessage `json:"changes,omitempty" doc:"Array of typed change ops the editor diffed from the previous published version. Display-only: rollback still targets whole versions."`
	}
}

// publishForm handles POST /api/v1/forms/{form_id}/publish.
// Returns the full form resource so the editor can refresh its draft and
// latest_published in a single call.
func (h *Handler) publishForm(ctx context.Context, input *publishFormBodyInput) (*formHTTPResponse, error) {
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
		Changes:       input.Body.Changes,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: resp}, nil
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
// Returns the full form resource so the editor can rehydrate the new draft
// in a single call.
func (h *Handler) rollbackForm(ctx context.Context, input *rollbackFormBodyInput) (*formHTTPResponse, error) {
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
	return &formHTTPResponse{Body: resp}, nil
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

type styleVersionsHTTPResponse struct {
	Body *FormStyleVersionsResponse
}

type stylePresetsHTTPResponse struct {
	Body *FormStylePresetsResponse
}

type updateStyleBodyInput struct {
	Body struct {
		LogoKey      *string         `json:"logo_key,omitempty"      doc:"Object-storage key for the clinic logo image."`
		PrimaryColor *string         `json:"primary_color,omitempty" pattern:"^#[0-9A-Fa-f]{6}$" doc:"Hex colour e.g. #3B82F6."`
		FontFamily   *string         `json:"font_family,omitempty"   doc:"Font family name recognised by the Flutter PDF renderer."`
		HeaderExtra  *string         `json:"header_extra,omitempty"  doc:"Extra header text shown below the clinic name/logo."`
		FooterText   *string         `json:"footer_text,omitempty"   doc:"Custom footer text; form version and approver are appended automatically."`
		Config       json.RawMessage `json:"config,omitempty"        doc:"Rich doc-theme config blob produced by the three-pane designer. Free-form JSON; the top-level colour/font/header/footer fields are mirrored into the flat columns."`
		PresetID     *string         `json:"preset_id,omitempty"     doc:"ID of the preset this config was derived from, e.g. 'dental.clean_clinical'."`
	}
}

type stylePresetsQueryInput struct {
	Vertical string `query:"vertical" doc:"Clinic vertical to return presets for. Matches clinics.vertical (aged_care|veterinary|dental|general_clinic). Falls back to general_clinic when empty or unknown." enum:"aged_care,veterinary,dental,general_clinic" required:"false"`
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
		Config:       input.Body.Config,
		PresetID:     input.Body.PresetID,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &styleHTTPResponse{Body: resp}, nil
}

// listStyleVersions handles GET /api/v1/clinic/form-style/versions.
func (h *Handler) listStyleVersions(ctx context.Context, _ *struct{}) (*styleVersionsHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	resp, err := h.svc.ListStyleVersions(ctx, clinicID)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &styleVersionsHTTPResponse{Body: resp}, nil
}

// listStylePresets handles GET /api/v1/clinic/form-style/presets.
// Accepts optional ?vertical=, defaulting to "general_clinic" when absent (a future
// patch can resolve the caller's clinic vertical from the JWT-bound clinic
// row; we keep this handler dependency-free for now).
func (h *Handler) listStylePresets(ctx context.Context, input *stylePresetsQueryInput) (*stylePresetsHTTPResponse, error) {
	vertical := input.Vertical
	if vertical == "" {
		vertical = "general_clinic"
	}
	return &stylePresetsHTTPResponse{Body: h.svc.ListStylePresets(ctx, vertical)}, nil
}

// ── Style logo upload ─────────────────────────────────────────────────────────

type uploadStyleLogoInput struct {
	RawBody multipart.Form
}

// maxStyleLogoBytes caps doc-theme logo uploads at 4 MiB.
const maxStyleLogoBytes int64 = 4 << 20

type styleLogoUploadHTTPResponse struct {
	Body *FormStyleLogoUploadResponse
}

// uploadStyleLogo handles POST /api/v1/clinic/form-style/logo
// (multipart/form-data, field "file").
//
// Validates in two phases: first the client-declared Content-Type, then the
// file's magic bytes. The header alone isn't trustworthy — a caller can claim
// "image/png" while uploading an HTML/SVG/executable — and the downstream
// signed-URL flow serves whatever we stored with whatever content-type we
// persisted, so an unverified upload becomes an XSS/SSRF vector when the
// browser fetches it back. Also: SVG is rejected outright (scriptable XML);
// the designer only needs raster logos for PDF rendering.
func (h *Handler) uploadStyleLogo(ctx context.Context, input *uploadStyleLogoInput) (*styleLogoUploadHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	files := input.RawBody.File["file"]
	if len(files) == 0 {
		return nil, huma.Error400BadRequest("missing form field \"file\"")
	}
	hdr := files[0]
	if hdr.Size > maxStyleLogoBytes {
		return nil, huma.Error400BadRequest(fmt.Sprintf("logo too large (max %d bytes)", maxStyleLogoBytes))
	}

	declared := hdr.Header.Get("Content-Type")
	if !isAllowedStyleLogoType(declared) {
		return nil, huma.Error415UnsupportedMediaType("logo must be png, jpeg, or webp")
	}

	f, err := hdr.Open()
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read uploaded file")
	}
	defer func() { _ = f.Close() }()

	// Peek enough bytes to cover every supported format's magic sequence
	// (WEBP needs 12: "RIFF????WEBP"). Putting them back in front of the
	// reader via io.MultiReader means the storage layer still sees the
	// whole file.
	head := make([]byte, 12)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, huma.Error500InternalServerError("could not read uploaded file")
	}
	head = head[:n]
	detected := sniffImageType(head)
	if detected == "" || detected != canonicalStyleLogoType(declared) {
		return nil, huma.Error415UnsupportedMediaType("logo content does not match its declared type — must be a real png, jpeg, or webp image")
	}

	body := io.MultiReader(bytes.NewReader(head), f)
	resp, err := h.svc.UploadStyleLogo(ctx, clinicID, detected, body, hdr.Size)
	if err != nil {
		return nil, mapFormError(err)
	}
	return &styleLogoUploadHTTPResponse{Body: resp}, nil
}

func isAllowedStyleLogoType(ct string) bool {
	return canonicalStyleLogoType(ct) != ""
}

// canonicalStyleLogoType collapses the aliases browsers send (e.g. "image/jpg"
// from old Windows apps) into the one MIME string we store and return. Empty
// means "not on the allowlist".
func canonicalStyleLogoType(ct string) string {
	switch ct {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/webp":
		return "image/webp"
	}
	return ""
}

// sniffImageType returns the canonical MIME type that matches the file's
// magic bytes, or "" if the bytes don't match any supported format. Callers
// compare the result to canonicalStyleLogoType(declared) so a spoofed
// Content-Type on a real image of a different kind still rejects.
func sniffImageType(head []byte) string {
	switch {
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
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
