package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/extraction"
)

// PolicyClauseFetcher retrieves enforceable clauses for all policies linked to a form.
// Implemented by an adapter in app.go that bridges to the policy repository.
type PolicyClauseFetcher interface {
	GetClausesForForm(ctx context.Context, formID uuid.UUID) ([]extraction.PolicyClause, error)
}

// Service handles business logic for the forms module.
type Service struct {
	repo    repo
	clauses PolicyClauseFetcher
	checker extraction.FormCoverageChecker
}

// NewService constructs a forms Service.
// Pass nil for clauses and checker to disable policy checking (tests, local dev).
func NewService(r repo, clauses PolicyClauseFetcher, checker extraction.FormCoverageChecker) *Service {
	return &Service{repo: r, clauses: clauses, checker: checker}
}

// ── Response types ────────────────────────────────────────────────────────────

// FieldResponse is the API-safe representation of a form field.
type FieldResponse struct {
	ID             string          `json:"id"`
	Position       int             `json:"position"`
	Title          string          `json:"title"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	AIPrompt       *string         `json:"ai_prompt,omitempty"`
	Required       bool            `json:"required"`
	Skippable      bool            `json:"skippable"`
	AllowInference bool            `json:"allow_inference"`
	MinConfidence  *float64        `json:"min_confidence,omitempty"`
}

// FormVersionResponse is the API-safe representation of a form version.
//
//nolint:revive
type FormVersionResponse struct {
	ID                string                   `json:"id"`
	FormID            string                   `json:"form_id"`
	Status            domain.FormVersionStatus `json:"status"`
	VersionMajor      *int                     `json:"version_major,omitempty"`
	VersionMinor      *int                     `json:"version_minor,omitempty"`
	ChangeType        *domain.ChangeType       `json:"change_type,omitempty"`
	ChangeSummary     *string                  `json:"change_summary,omitempty"`
	RollbackOf        *string                  `json:"rollback_of,omitempty"`
	PolicyCheckResult *string                  `json:"policy_check_result,omitempty"`
	PolicyCheckAt     *string                  `json:"policy_check_at,omitempty"`
	PublishedAt       *string                  `json:"published_at,omitempty"`
	PublishedBy       *string                  `json:"published_by,omitempty"`
	CreatedAt         string                   `json:"created_at"`
	Fields            []*FieldResponse         `json:"fields,omitempty"`
}

// FormVersionListResponse is a list of form versions.
//
//nolint:revive
type FormVersionListResponse struct {
	Items []*FormVersionResponse `json:"items"`
}

// FormResponse is the API-safe representation of a form with its current state.
//
//nolint:revive
type FormResponse struct {
	ID            string   `json:"id"`
	ClinicID      string   `json:"clinic_id"`
	GroupID       *string  `json:"group_id,omitempty"`
	Name          string   `json:"name"`
	Description   *string  `json:"description,omitempty"`
	OverallPrompt *string  `json:"overall_prompt,omitempty"`
	Tags          []string `json:"tags"`
	CreatedBy     string   `json:"created_by"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ArchivedAt    *string  `json:"archived_at,omitempty"`
	RetireReason  *string  `json:"retire_reason,omitempty"`
	// Draft is non-nil when an editable draft version exists.
	Draft *FormVersionResponse `json:"draft,omitempty"`
	// LatestPublished is the most recent frozen version; nil on a brand-new form.
	LatestPublished *FormVersionResponse `json:"latest_published,omitempty"`
}

// FormListResponse is a paginated list of forms.
//
//nolint:revive
type FormListResponse struct {
	Items  []*FormResponse `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// FormGroupResponse is the API-safe representation of a form group.
//
//nolint:revive
type FormGroupResponse struct {
	ID          string  `json:"id"`
	ClinicID    string  `json:"clinic_id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// FormGroupListResponse is a list of form groups.
//
//nolint:revive
type FormGroupListResponse struct {
	Items []*FormGroupResponse `json:"items"`
}

// FormStyleResponse is the API-safe representation of the clinic's current PDF style.
//
//nolint:revive
type FormStyleResponse struct {
	Version      int     `json:"version"`
	LogoKey      *string `json:"logo_key,omitempty"`
	PrimaryColor *string `json:"primary_color,omitempty"`
	FontFamily   *string `json:"font_family,omitempty"`
	HeaderExtra  *string `json:"header_extra,omitempty"`
	FooterText   *string `json:"footer_text,omitempty"`
	UpdatedAt    string  `json:"updated_at"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateFormInput holds validated input for creating a new form.
type CreateFormInput struct {
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	GroupID       *uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
}

// UpdateDraftInput holds input for updating the draft version of a form.
type UpdateDraftInput struct {
	FormID        uuid.UUID
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	GroupID       *uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
	Fields        []FieldInput
}

// FieldInput holds the values for a single field in a draft update.
type FieldInput struct {
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

// PublishFormInput holds input for publishing the draft version.
type PublishFormInput struct {
	FormID        uuid.UUID
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	ChangeType    domain.ChangeType
	ChangeSummary *string
}

// RollbackFormInput holds input for rolling a form back to a prior version.
type RollbackFormInput struct {
	FormID          uuid.UUID
	ClinicID        uuid.UUID
	StaffID         uuid.UUID
	TargetVersionID uuid.UUID
	Reason          *string
}

// RetireFormInput holds input for retiring a form.
type RetireFormInput struct {
	FormID   uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Reason   *string
}

// ListFormsInput holds filter and pagination parameters for listing forms.
type ListFormsInput struct {
	Limit           int
	Offset          int
	GroupID         *uuid.UUID
	IncludeArchived bool
	Tag             *string
}

// CreateGroupInput holds validated input for creating a form group.
type CreateGroupInput struct {
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	Name        string
	Description *string
}

// UpdateGroupInput holds validated input for updating a form group.
type UpdateGroupInput struct {
	GroupID     uuid.UUID
	ClinicID    uuid.UUID
	Name        string
	Description *string
}

// UpdateStyleInput holds the desired style settings for the clinic.
type UpdateStyleInput struct {
	ClinicID     uuid.UUID
	StaffID      uuid.UUID
	LogoKey      *string
	PrimaryColor *string
	FontFamily   *string
	HeaderExtra  *string
	FooterText   *string
}

// ── Service methods ───────────────────────────────────────────────────────────

// CreateForm creates a new form and an empty draft version.
// Returns the created form with the draft attached.
func (s *Service) CreateForm(ctx context.Context, input CreateFormInput) (*FormResponse, error) {
	formID := domain.NewID()
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	form, err := s.repo.CreateForm(ctx, CreateFormParams{
		ID:            formID,
		ClinicID:      input.ClinicID,
		GroupID:       input.GroupID,
		Name:          input.Name,
		Description:   input.Description,
		OverallPrompt: input.OverallPrompt,
		Tags:          tags,
		CreatedBy:     input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateForm: %w", err)
	}

	draft, err := s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
		ID:        domain.NewID(),
		FormID:    formID,
		CreatedBy: input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateForm: draft version: %w", err)
	}

	resp := toFormResponse(form)
	resp.Draft = toVersionResponse(draft, nil)
	return resp, nil
}

// GetForm fetches a form with its draft (if any) and latest published version (if any).
func (s *Service) GetForm(ctx context.Context, formID, clinicID uuid.UUID) (*FormResponse, error) {
	form, err := s.repo.GetFormByID(ctx, formID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.GetForm: %w", err)
	}

	resp := toFormResponse(form)

	draft, err := s.repo.GetDraftVersion(ctx, formID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.GetForm: draft: %w", err)
	}
	if draft != nil {
		fields, err := s.repo.GetFieldsByVersionID(ctx, draft.ID)
		if err != nil {
			return nil, fmt.Errorf("forms.service.GetForm: draft fields: %w", err)
		}
		resp.Draft = toVersionResponse(draft, fields)
	}

	latest, err := s.repo.GetLatestPublishedVersion(ctx, formID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.GetForm: latest published: %w", err)
	}
	if latest != nil {
		fields, err := s.repo.GetFieldsByVersionID(ctx, latest.ID)
		if err != nil {
			return nil, fmt.Errorf("forms.service.GetForm: latest fields: %w", err)
		}
		resp.LatestPublished = toVersionResponse(latest, fields)
	}

	return resp, nil
}

// ListForms returns a paginated list of forms for a clinic.
func (s *Service) ListForms(ctx context.Context, clinicID uuid.UUID, input ListFormsInput) (*FormListResponse, error) {
	input.Limit = clampLimit(input.Limit)

	forms, total, err := s.repo.ListForms(ctx, clinicID, ListFormsParams(input))
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListForms: %w", err)
	}

	items := make([]*FormResponse, len(forms))
	for i, f := range forms {
		items[i] = toFormResponse(f)
	}

	return &FormListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// UpdateDraft updates form metadata and replaces all fields on the draft version.
// If no draft exists (e.g. after a publish), a new draft is created automatically.
func (s *Service) UpdateDraft(ctx context.Context, input UpdateDraftInput) (*FormResponse, error) {
	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateDraft: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.UpdateDraft: %w", domain.ErrConflict)
	}

	// Update form-level metadata.
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	form, err = s.repo.UpdateFormMeta(ctx, UpdateFormMetaParams{
		ID:            input.FormID,
		ClinicID:      input.ClinicID,
		GroupID:       input.GroupID,
		Name:          input.Name,
		Description:   input.Description,
		OverallPrompt: input.OverallPrompt,
		Tags:          tags,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateDraft: meta: %w", err)
	}

	// Ensure a draft version exists.
	draft, err := s.repo.GetDraftVersion(ctx, input.FormID)
	if errors.Is(err, domain.ErrNotFound) {
		draft, err = s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
			ID:        domain.NewID(),
			FormID:    input.FormID,
			CreatedBy: input.StaffID,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateDraft: draft: %w", err)
	}

	// Replace fields.
	fieldParams := make([]CreateFieldParams, len(input.Fields))
	for i, fi := range input.Fields {
		cfg := fi.Config
		if cfg == nil {
			cfg = json.RawMessage(`{}`)
		}
		fieldParams[i] = CreateFieldParams{
			ID:             domain.NewID(),
			FormVersionID:  draft.ID,
			Position:       fi.Position,
			Title:          fi.Title,
			Type:           fi.Type,
			Config:         cfg,
			AIPrompt:       fi.AIPrompt,
			Required:       fi.Required,
			Skippable:      fi.Skippable,
			AllowInference: fi.AllowInference,
			MinConfidence:  fi.MinConfidence,
		}
	}

	fields, err := s.repo.ReplaceFields(ctx, draft.ID, fieldParams)
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateDraft: fields: %w", err)
	}

	resp := toFormResponse(form)
	resp.Draft = toVersionResponse(draft, fields)
	return resp, nil
}

// PublishForm freezes the current draft, assigns a semver number, and makes the
// form available for use in audio processing.
func (s *Service) PublishForm(ctx context.Context, input PublishFormInput) (*FormVersionResponse, error) {
	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: %w", domain.ErrConflict)
	}

	draft, err := s.repo.GetDraftVersion(ctx, input.FormID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: no draft: %w", err)
	}

	// Compute next version number.
	major, minor := nextVersion(input.ChangeType, nil, nil)
	prev, err := s.repo.GetLatestPublishedVersion(ctx, input.FormID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.PublishForm: prev version: %w", err)
	}
	if prev != nil {
		major, minor = nextVersion(input.ChangeType, prev.VersionMajor, prev.VersionMinor)
	}

	published, err := s.repo.PublishDraftVersion(ctx, PublishDraftVersionParams{
		ID:            draft.ID,
		VersionMajor:  major,
		VersionMinor:  minor,
		ChangeType:    input.ChangeType,
		ChangeSummary: input.ChangeSummary,
		PublishedBy:   input.StaffID,
		PublishedAt:   domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: %w", err)
	}

	fields, err := s.repo.GetFieldsByVersionID(ctx, published.ID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: fields: %w", err)
	}

	return toVersionResponse(published, fields), nil
}

// RunPolicyCheck calls the AI to assess whether the form's fields cover the
// requirements of all linked policy clauses. Saves the result on the draft.
// Returns ErrConflict if no policies are linked or no checker is configured.
func (s *Service) RunPolicyCheck(ctx context.Context, formID, clinicID, staffID uuid.UUID) (*FormVersionResponse, error) {
	form, err := s.repo.GetFormByID(ctx, formID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: form is retired: %w", domain.ErrConflict)
	}

	draft, err := s.repo.GetDraftVersion(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: %w", err)
	}

	if s.clauses == nil || s.checker == nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: policy checker not configured: %w", domain.ErrConflict)
	}

	clauses, err := s.clauses.GetClausesForForm(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: get clauses: %w", err)
	}
	if len(clauses) == 0 {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: no policy clauses found — link policies with clauses first: %w", domain.ErrConflict)
	}

	fields, err := s.repo.GetFieldsByVersionID(ctx, draft.ID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: get fields: %w", err)
	}

	// Build FieldSpec slice for the checker — exclude skippable fields.
	specs := make([]extraction.FieldSpec, 0, len(fields))
	for _, f := range fields {
		if f.Skippable {
			continue
		}
		prompt := ""
		if f.AIPrompt != nil {
			prompt = *f.AIPrompt
		}
		specs = append(specs, extraction.FieldSpec{
			ID:             f.ID.String(),
			Title:          f.Title,
			Type:           f.Type,
			AIPrompt:       prompt,
			Required:       f.Required,
			AllowInference: f.AllowInference,
		})
	}

	overallPrompt := ""
	if form.OverallPrompt != nil {
		overallPrompt = *form.OverallPrompt
	}

	result, err := s.checker.CheckFormCoverage(ctx, overallPrompt, specs, clauses)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: checker: %w", err)
	}

	version, err := s.repo.SavePolicyCheckResult(ctx, SavePolicyCheckParams{
		VersionID: draft.ID,
		Result:    result,
		CheckedBy: staffID,
		CheckedAt: domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: save: %w", err)
	}

	return toVersionResponse(version, nil), nil
}

// RollbackForm creates a new draft that copies all fields from a prior published version.
// Any existing draft is discarded.
func (s *Service) RollbackForm(ctx context.Context, input RollbackFormInput) (*FormVersionResponse, error) {
	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: %w", domain.ErrConflict)
	}

	target, err := s.repo.GetVersionByID(ctx, input.TargetVersionID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: target: %w", err)
	}
	if target.FormID != input.FormID {
		return nil, fmt.Errorf("forms.service.RollbackForm: version belongs to different form: %w", domain.ErrForbidden)
	}
	if target.Status != domain.FormVersionStatusPublished {
		return nil, fmt.Errorf("forms.service.RollbackForm: can only rollback to published version: %w", domain.ErrConflict)
	}

	// Copy fields from the target version.
	sourceFields, err := s.repo.GetFieldsByVersionID(ctx, target.ID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: source fields: %w", err)
	}

	newDraftID := domain.NewID()
	draft, err := s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
		ID:         newDraftID,
		FormID:     input.FormID,
		RollbackOf: &input.TargetVersionID,
		CreatedBy:  input.StaffID,
	})
	if err != nil {
		// If a draft already exists, it blocks the rollback.
		if errors.Is(err, domain.ErrConflict) {
			return nil, fmt.Errorf("forms.service.RollbackForm: discard existing draft before rollback: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("forms.service.RollbackForm: create draft: %w", err)
	}

	// Copy fields into the new draft.
	fieldParams := make([]CreateFieldParams, len(sourceFields))
	for i, f := range sourceFields {
		fieldParams[i] = CreateFieldParams{
			ID:             domain.NewID(),
			FormVersionID:  newDraftID,
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         f.Config,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  f.MinConfidence,
		}
	}
	fields, err := s.repo.ReplaceFields(ctx, newDraftID, fieldParams)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: fields: %w", err)
	}

	return toVersionResponse(draft, fields), nil
}

// RetireForm archives a form so it can no longer be used for new notes.
// In-flight notes are unaffected; they complete normally.
func (s *Service) RetireForm(ctx context.Context, input RetireFormInput) (*FormResponse, error) {
	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RetireForm: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.RetireForm: already retired: %w", domain.ErrConflict)
	}

	retired, err := s.repo.RetireForm(ctx, RetireFormParams{
		ID:           input.FormID,
		ClinicID:     input.ClinicID,
		RetireReason: input.Reason,
		ArchivedAt:   domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.RetireForm: %w", err)
	}

	return toFormResponse(retired), nil
}

// ListVersions returns the full version history (published only) for a form.
func (s *Service) ListVersions(ctx context.Context, formID, clinicID uuid.UUID) (*FormVersionListResponse, error) {
	// Verify clinic ownership.
	if _, err := s.repo.GetFormByID(ctx, formID, clinicID); err != nil {
		return nil, fmt.Errorf("forms.service.ListVersions: %w", err)
	}

	versions, err := s.repo.ListPublishedVersions(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListVersions: %w", err)
	}

	items := make([]*FormVersionResponse, len(versions))
	for i, v := range versions {
		items[i] = toVersionResponse(v, nil)
	}
	return &FormVersionListResponse{Items: items}, nil
}

// ── Groups ────────────────────────────────────────────────────────────────────

// CreateGroup creates a new form group (folder) for a clinic.
func (s *Service) CreateGroup(ctx context.Context, input CreateGroupInput) (*FormGroupResponse, error) {
	g, err := s.repo.CreateGroup(ctx, CreateGroupParams{
		ID:          domain.NewID(),
		ClinicID:    input.ClinicID,
		Name:        input.Name,
		Description: input.Description,
		CreatedBy:   input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateGroup: %w", err)
	}
	return toGroupResponse(g), nil
}

// ListGroups returns all groups for a clinic.
func (s *Service) ListGroups(ctx context.Context, clinicID uuid.UUID) (*FormGroupListResponse, error) {
	groups, err := s.repo.ListGroups(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListGroups: %w", err)
	}

	items := make([]*FormGroupResponse, len(groups))
	for i, g := range groups {
		items[i] = toGroupResponse(g)
	}
	return &FormGroupListResponse{Items: items}, nil
}

// UpdateGroup updates a group's name and description.
func (s *Service) UpdateGroup(ctx context.Context, input UpdateGroupInput) (*FormGroupResponse, error) {
	g, err := s.repo.UpdateGroup(ctx, UpdateGroupParams{
		ID:          input.GroupID,
		ClinicID:    input.ClinicID,
		Name:        input.Name,
		Description: input.Description,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateGroup: %w", err)
	}
	return toGroupResponse(g), nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

// LinkPolicy attaches a policy to a form.
func (s *Service) LinkPolicy(ctx context.Context, formID, clinicID, policyID, staffID uuid.UUID) error {
	// Verify clinic ownership.
	if _, err := s.repo.GetFormByID(ctx, formID, clinicID); err != nil {
		return fmt.Errorf("forms.service.LinkPolicy: %w", err)
	}
	if err := s.repo.LinkPolicy(ctx, formID, policyID, staffID); err != nil {
		return fmt.Errorf("forms.service.LinkPolicy: %w", err)
	}
	return nil
}

// UnlinkPolicy removes a policy from a form.
func (s *Service) UnlinkPolicy(ctx context.Context, formID, clinicID, policyID uuid.UUID) error {
	if _, err := s.repo.GetFormByID(ctx, formID, clinicID); err != nil {
		return fmt.Errorf("forms.service.UnlinkPolicy: %w", err)
	}
	if err := s.repo.UnlinkPolicy(ctx, formID, policyID); err != nil {
		return fmt.Errorf("forms.service.UnlinkPolicy: %w", err)
	}
	return nil
}

// ListLinkedPolicies returns all policy IDs linked to a form.
func (s *Service) ListLinkedPolicies(ctx context.Context, formID, clinicID uuid.UUID) ([]string, error) {
	if _, err := s.repo.GetFormByID(ctx, formID, clinicID); err != nil {
		return nil, fmt.Errorf("forms.service.ListLinkedPolicies: %w", err)
	}
	ids, err := s.repo.ListLinkedPolicies(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListLinkedPolicies: %w", err)
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out, nil
}

// ── Style ─────────────────────────────────────────────────────────────────────

// GetCurrentStyle returns the clinic's active PDF style settings.
// Returns nil response (no error) if no style has been configured yet.
func (s *Service) GetCurrentStyle(ctx context.Context, clinicID uuid.UUID) (*FormStyleResponse, error) {
	style, err := s.repo.GetCurrentStyle(ctx, clinicID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("forms.service.GetCurrentStyle: %w", err)
	}
	return toStyleResponse(style), nil
}

// UpdateStyle saves a new style version for the clinic, incrementing the version counter.
func (s *Service) UpdateStyle(ctx context.Context, input UpdateStyleInput) (*FormStyleResponse, error) {
	nextVer := 1
	current, err := s.repo.GetCurrentStyle(ctx, input.ClinicID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.UpdateStyle: %w", err)
	}
	if current != nil {
		nextVer = current.Version + 1
	}

	style, err := s.repo.CreateStyleVersion(ctx, CreateStyleVersionParams{
		ID:           domain.NewID(),
		ClinicID:     input.ClinicID,
		Version:      nextVer,
		LogoKey:      input.LogoKey,
		PrimaryColor: input.PrimaryColor,
		FontFamily:   input.FontFamily,
		HeaderExtra:  input.HeaderExtra,
		FooterText:   input.FooterText,
		CreatedBy:    input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateStyle: %w", err)
	}
	return toStyleResponse(style), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// nextVersion computes the next semver numbers based on the change type and
// the most recently published version numbers.
func nextVersion(ct domain.ChangeType, prevMajor, prevMinor *int) (major, minor int) {
	if prevMajor == nil || prevMinor == nil {
		return 1, 0 // first publish
	}
	if ct == domain.ChangeTypeMajor {
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

func toFormResponse(f *FormRecord) *FormResponse {
	r := &FormResponse{
		ID:            f.ID.String(),
		ClinicID:      f.ClinicID.String(),
		Name:          f.Name,
		Description:   f.Description,
		OverallPrompt: f.OverallPrompt,
		Tags:          f.Tags,
		CreatedBy:     f.CreatedBy.String(),
		CreatedAt:     f.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     f.UpdatedAt.Format(time.RFC3339),
		RetireReason:  f.RetireReason,
	}
	if f.Tags == nil {
		r.Tags = []string{}
	}
	if f.GroupID != nil {
		s := f.GroupID.String()
		r.GroupID = &s
	}
	if f.ArchivedAt != nil {
		s := f.ArchivedAt.Format(time.RFC3339)
		r.ArchivedAt = &s
	}
	return r
}

func toVersionResponse(v *FormVersionRecord, fields []*FieldRecord) *FormVersionResponse {
	r := &FormVersionResponse{
		ID:                v.ID.String(),
		FormID:            v.FormID.String(),
		Status:            v.Status,
		VersionMajor:      v.VersionMajor,
		VersionMinor:      v.VersionMinor,
		ChangeType:        v.ChangeType,
		ChangeSummary:     v.ChangeSummary,
		PolicyCheckResult: v.PolicyCheckResult,
		CreatedAt:         v.CreatedAt.Format(time.RFC3339),
	}
	if v.RollbackOf != nil {
		s := v.RollbackOf.String()
		r.RollbackOf = &s
	}
	if v.PolicyCheckAt != nil {
		s := v.PolicyCheckAt.Format(time.RFC3339)
		r.PolicyCheckAt = &s
	}
	if v.PublishedAt != nil {
		s := v.PublishedAt.Format(time.RFC3339)
		r.PublishedAt = &s
	}
	if v.PublishedBy != nil {
		s := v.PublishedBy.String()
		r.PublishedBy = &s
	}
	if fields != nil {
		r.Fields = make([]*FieldResponse, len(fields))
		for i, f := range fields {
			r.Fields[i] = toFieldResponse(f)
		}
	}
	return r
}

func toFieldResponse(f *FieldRecord) *FieldResponse {
	return &FieldResponse{
		ID:             f.ID.String(),
		Position:       f.Position,
		Title:          f.Title,
		Type:           f.Type,
		Config:         f.Config,
		AIPrompt:       f.AIPrompt,
		Required:       f.Required,
		Skippable:      f.Skippable,
		AllowInference: f.AllowInference,
		MinConfidence:  f.MinConfidence,
	}
}

func toGroupResponse(g *GroupRecord) *FormGroupResponse {
	return &FormGroupResponse{
		ID:          g.ID.String(),
		ClinicID:    g.ClinicID.String(),
		Name:        g.Name,
		Description: g.Description,
		CreatedAt:   g.CreatedAt.Format(time.RFC3339),
	}
}

func toStyleResponse(s *StyleVersionRecord) *FormStyleResponse {
	return &FormStyleResponse{
		Version:      s.Version,
		LogoKey:      s.LogoKey,
		PrimaryColor: s.PrimaryColor,
		FontFamily:   s.FontFamily,
		HeaderExtra:  s.HeaderExtra,
		FooterText:   s.FooterText,
		UpdatedAt:    s.CreatedAt.Format(time.RFC3339),
	}
}
