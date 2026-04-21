package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// StyleLogoSigner produces a short-lived signed GET URL for a doc-theme logo key.
// Distinct from clinic.LogoSigner — doc-theme logos live under their own prefix
// so clinics can set a different mark for documents (wordmark/square) than the
// top-nav clinic logo. Implemented by platform/storage in app.go.
type StyleLogoSigner interface {
	SignStyleLogoURL(ctx context.Context, key string) (string, error)
}

// StyleLogoUploader uploads bytes to object storage under the doc-theme prefix
// and returns the persisted key. Implemented by platform/storage in app.go.
type StyleLogoUploader interface {
	UploadStyleLogo(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (string, error)
}

// StaffNameResolver turns a staff UUID into a displayable full name so the
// version history can read "by Dr. Nadine Patel" instead of a raw ID. Adapter
// lives in app.go and delegates to the staff service; nil implementations are
// tolerated (we fall back to the UUID string).
type StaffNameResolver interface {
	ResolveStaffName(ctx context.Context, staffID, clinicID uuid.UUID) (string, error)
}

// Service handles business logic for the forms module.
type Service struct {
	repo         repo
	clauses      PolicyClauseFetcher
	checker      extraction.FormCoverageChecker
	logoSigner   StyleLogoSigner
	logoUploader StyleLogoUploader
	staffNames   StaffNameResolver
}

// NewService constructs a forms Service.
// Pass nil for clauses/checker to disable policy checking (tests, local dev).
// Pass nil for signer/uploader to disable the doc-theme logo upload endpoint.
// Pass nil for staffNames to skip name resolution (API will return raw UUIDs).
func NewService(r repo, clauses PolicyClauseFetcher, checker extraction.FormCoverageChecker, signer StyleLogoSigner, uploader StyleLogoUploader, staffNames StaffNameResolver) *Service {
	return &Service{repo: r, clauses: clauses, checker: checker, logoSigner: signer, logoUploader: uploader, staffNames: staffNames}
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
	// Changes is a raw JSON array of typed ops ({op, ...}) computed by the editor
	// at publish time. Shape evolves over time; consumers should tolerate unknown
	// op kinds.
	Changes           json.RawMessage          `json:"changes,omitempty"`
	RollbackOf        *string                  `json:"rollback_of,omitempty"`
	PolicyCheckResult *string                  `json:"policy_check_result,omitempty"`
	PolicyCheckAt     *string                  `json:"policy_check_at,omitempty"`
	PublishedAt       *string                  `json:"published_at,omitempty"`
	PublishedBy       *string                  `json:"published_by,omitempty"`
	PublishedByName   *string                  `json:"published_by_name,omitempty"`
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
// Config is the rich doc-theme blob consumed by the three-pane designer. Flat
// fields remain the source of truth for the simple onboarding step and for any
// renderer that only needs a logo + accent colour + font.
//
//nolint:revive
type FormStyleResponse struct {
	Version       int             `json:"version"`
	LogoKey       *string         `json:"logo_key,omitempty"`
	HeaderLogoURL *string         `json:"header_logo_url,omitempty"`
	PrimaryColor  *string         `json:"primary_color,omitempty"`
	FontFamily    *string         `json:"font_family,omitempty"`
	HeaderExtra   *string         `json:"header_extra,omitempty"`
	FooterText    *string         `json:"footer_text,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
	PresetID      *string         `json:"preset_id,omitempty"`
	IsActive      bool            `json:"is_active"`
	UpdatedAt     string          `json:"updated_at"`
}

// FormStyleVersionsResponse lists every published style version for a clinic.
//
//nolint:revive
type FormStyleVersionsResponse struct {
	Items []*FormStyleResponse `json:"items"`
}

// FormStylePresetResponse is a single vertical-specific starter template.
//
//nolint:revive
type FormStylePresetResponse struct {
	ID          string          `json:"id"`
	Vertical    string          `json:"vertical"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      json.RawMessage `json:"config"`
}

// FormStylePresetsResponse is a list of preset templates.
//
//nolint:revive
type FormStylePresetsResponse struct {
	Items []*FormStylePresetResponse `json:"items"`
}

// FormStyleLogoUploadResponse is returned after a successful doc-theme logo
// upload. Key is the persisted object-storage key (write into
// config.header.logo_key via PUT /form-style to save the choice); URL is a
// short-lived signed GET URL for immediate preview in the designer.
//
//nolint:revive
type FormStyleLogoUploadResponse struct {
	Key string `json:"key"`
	URL string `json:"url"`
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
	// Changes is the JSONB array of typed ops the editor diffed from the
	// previous published version. Empty when absent; shape is opaque to server.
	Changes json.RawMessage
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
	Config       json.RawMessage
	PresetID     *string
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

	s.resolvePublisherName(ctx, clinicID, resp.Draft)
	s.resolvePublisherName(ctx, clinicID, resp.LatestPublished)

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
		resp := toFormResponse(f)

		// Attach draft + latest published metadata so the list UI can render
		// the correct status pill without a per-form GetForm round-trip.
		// Fields are omitted to keep the list endpoint light.
		draft, err := s.repo.GetDraftVersion(ctx, f.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("forms.service.ListForms: draft: %w", err)
		}
		if draft != nil {
			resp.Draft = toVersionResponse(draft, nil)
		}

		latest, err := s.repo.GetLatestPublishedVersion(ctx, f.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("forms.service.ListForms: latest published: %w", err)
		}
		if latest != nil {
			resp.LatestPublished = toVersionResponse(latest, nil)
		}

		items[i] = resp
	}

	// Batch-resolve publisher names across every version we're returning so
	// a clinic with dozens of forms doesn't fan out into dozens of staff
	// lookups — the cache in resolvePublisherNames dedupes by UUID.
	toEnrich := make([]*FormVersionResponse, 0, len(items)*2)
	for _, it := range items {
		if it.Draft != nil {
			toEnrich = append(toEnrich, it.Draft)
		}
		if it.LatestPublished != nil {
			toEnrich = append(toEnrich, it.LatestPublished)
		}
	}
	s.resolvePublisherNames(ctx, clinicID, toEnrich)

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
// form available for use in audio processing. Returns the full form resource
// (with the new latest_published attached) so the editor can refresh without
// a follow-up GET.
func (s *Service) PublishForm(ctx context.Context, input PublishFormInput) (*FormResponse, error) {
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
	major, minor := NextVersion(input.ChangeType, nil, nil)
	prev, err := s.repo.GetLatestPublishedVersion(ctx, input.FormID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.PublishForm: prev version: %w", err)
	}
	if prev != nil {
		major, minor = NextVersion(input.ChangeType, prev.VersionMajor, prev.VersionMinor)
	}

	if _, err := s.repo.PublishDraftVersion(ctx, PublishDraftVersionParams{
		ID:            draft.ID,
		VersionMajor:  major,
		VersionMinor:  minor,
		ChangeType:    input.ChangeType,
		ChangeSummary: input.ChangeSummary,
		Changes:       input.Changes,
		PublishedBy:   input.StaffID,
		PublishedAt:   domain.TimeNow(),
	}); err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: %w", err)
	}

	return s.GetForm(ctx, input.FormID, input.ClinicID)
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

// RollbackForm publishes a new immutable version whose fields are copied from
// a prior published version. The new version gets its own semver number
// (minor bump over the current latest) and records rollback_of so the history
// shows the provenance. Any in-flight draft is left alone — rolling back is
// independent of the user's WIP and never destroys it.
func (s *Service) RollbackForm(ctx context.Context, input RollbackFormInput) (*FormResponse, error) {
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

	sourceFields, err := s.repo.GetFieldsByVersionID(ctx, target.ID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: source fields: %w", err)
	}

	// Compute next version. Minor bump over the current latest — rollback is
	// conceptually "a small course correction", not a breaking re-architecture.
	// Fall back to 1.0 when there is somehow no latest (shouldn't happen since
	// the target itself is published).
	latest, err := s.repo.GetLatestPublishedVersion(ctx, input.FormID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.RollbackForm: latest: %w", err)
	}
	major, minor := NextVersion(domain.ChangeTypeMinor, nil, nil)
	if latest != nil {
		major, minor = NextVersion(domain.ChangeTypeMinor, latest.VersionMajor, latest.VersionMinor)
	}

	// Build a human-readable summary. If the caller supplied one, keep it as
	// the user's note and prepend the canonical rollback prefix so the history
	// entry reads well at a glance.
	targetLabel := "earlier version"
	if target.VersionMajor != nil && target.VersionMinor != nil {
		targetLabel = fmt.Sprintf("v%d.%d", *target.VersionMajor, *target.VersionMinor)
	}
	summary := "Rolled back to " + targetLabel
	if input.Reason != nil && *input.Reason != "" {
		summary = fmt.Sprintf("%s — %s", summary, *input.Reason)
	}

	newID := domain.NewID()
	if _, err := s.repo.CreatePublishedVersion(ctx, CreatePublishedVersionParams{
		ID:            newID,
		FormID:        input.FormID,
		VersionMajor:  major,
		VersionMinor:  minor,
		ChangeType:    domain.ChangeTypeMinor,
		ChangeSummary: &summary,
		RollbackOf:    &input.TargetVersionID,
		PublishedBy:   input.StaffID,
		PublishedAt:   domain.TimeNow(),
	}); err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: create version: %w", err)
	}

	fieldParams := make([]CreateFieldParams, len(sourceFields))
	for i, f := range sourceFields {
		fieldParams[i] = CreateFieldParams{
			ID:             domain.NewID(),
			FormVersionID:  newID,
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
	if _, err := s.repo.ReplaceFields(ctx, newID, fieldParams); err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: fields: %w", err)
	}

	return s.GetForm(ctx, input.FormID, input.ClinicID)
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
	s.resolvePublisherNames(ctx, clinicID, items)
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
	return s.buildStyleResponse(ctx, style)
}

// UpdateStyle saves a new style version for the clinic, incrementing the version counter.
// If Config is supplied, its top-level colour/font/header/footer are mirrored into the
// flat columns so legacy renderers stay in sync.
func (s *Service) UpdateStyle(ctx context.Context, input UpdateStyleInput) (*FormStyleResponse, error) {
	nextVer := 1
	current, err := s.repo.GetCurrentStyle(ctx, input.ClinicID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("forms.service.UpdateStyle: %w", err)
	}
	if current != nil {
		nextVer = current.Version + 1
	}

	logoKey, primary, font, header, footer := input.LogoKey, input.PrimaryColor, input.FontFamily, input.HeaderExtra, input.FooterText
	if len(input.Config) > 0 {
		if m := mirrorFromConfig(input.Config); m != nil {
			if logoKey == nil && m.LogoKey != nil {
				logoKey = m.LogoKey
			}
			if primary == nil && m.PrimaryColor != nil {
				primary = m.PrimaryColor
			}
			if font == nil && m.FontFamily != nil {
				font = m.FontFamily
			}
			if header == nil && m.HeaderExtra != nil {
				header = m.HeaderExtra
			}
			if footer == nil && m.FooterText != nil {
				footer = m.FooterText
			}
		}
	}

	style, err := s.repo.CreateStyleVersion(ctx, CreateStyleVersionParams{
		ID:           domain.NewID(),
		ClinicID:     input.ClinicID,
		Version:      nextVer,
		LogoKey:      logoKey,
		PrimaryColor: primary,
		FontFamily:   font,
		HeaderExtra:  header,
		FooterText:   footer,
		Config:       input.Config,
		PresetID:     input.PresetID,
		CreatedBy:    input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.UpdateStyle: %w", err)
	}
	return s.buildStyleResponse(ctx, style)
}

// ListStyleVersions returns the full version history for the clinic.
func (s *Service) ListStyleVersions(ctx context.Context, clinicID uuid.UUID) (*FormStyleVersionsResponse, error) {
	recs, err := s.repo.ListStyleVersions(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListStyleVersions: %w", err)
	}
	items := make([]*FormStyleResponse, len(recs))
	for i, r := range recs {
		built, err := s.buildStyleResponse(ctx, r)
		if err != nil {
			return nil, fmt.Errorf("forms.service.ListStyleVersions: %w", err)
		}
		items[i] = built
	}
	return &FormStyleVersionsResponse{Items: items}, nil
}

// ListStylePresets returns the curated set of starter themes for a given vertical.
// Unknown verticals fall back to the "general_clinic" set.
func (s *Service) ListStylePresets(_ context.Context, vertical string) *FormStylePresetsResponse {
	return &FormStylePresetsResponse{Items: presetsFor(vertical)}
}

// UploadStyleLogo persists a doc-theme logo to object storage and returns the
// key plus a signed preview URL. The key is NOT written to the style config
// here — the client is expected to place it at config.header.logo_key and
// persist via UpdateStyle. This lets the user try a logo in the live preview
// and cancel without polluting the version history.
func (s *Service) UploadStyleLogo(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (*FormStyleLogoUploadResponse, error) {
	if s.logoUploader == nil {
		return nil, fmt.Errorf("forms.service.UploadStyleLogo: storage not configured")
	}
	key, err := s.logoUploader.UploadStyleLogo(ctx, clinicID, contentType, body, size)
	if err != nil {
		return nil, fmt.Errorf("forms.service.UploadStyleLogo: upload: %w", err)
	}
	var url string
	if s.logoSigner != nil {
		url, err = s.logoSigner.SignStyleLogoURL(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("forms.service.UploadStyleLogo: sign url: %w", err)
		}
	}
	return &FormStyleLogoUploadResponse{Key: key, URL: url}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// NextVersion computes the next semver numbers based on the change type and
// the most recently published version numbers. Exported so other modules
// (e.g. marketplace) can reuse the same semver bump logic.
func NextVersion(ct domain.ChangeType, prevMajor, prevMinor *int) (major, minor int) {
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

// resolvePublisherName looks up the staff display name for a version's
// published_by UUID so the API returns "Dr. Nadine Patel" instead of a raw
// ID. No-op when the resolver is nil (tests, local dev), when the version
// isn't published yet, or when the lookup fails — a missing name is not
// worth failing the whole request, since PublishedBy still round-trips.
func (s *Service) resolvePublisherName(ctx context.Context, clinicID uuid.UUID, v *FormVersionResponse) {
	if v == nil || v.PublishedBy == nil || s.staffNames == nil {
		return
	}
	staffID, err := uuid.Parse(*v.PublishedBy)
	if err != nil {
		return
	}
	name, err := s.staffNames.ResolveStaffName(ctx, staffID, clinicID)
	if err != nil || name == "" {
		return
	}
	v.PublishedByName = &name
}

// resolvePublisherNames enriches a batch of version responses. Uses a
// per-call cache so listing many versions by the same author only hits the
// staff service once.
func (s *Service) resolvePublisherNames(ctx context.Context, clinicID uuid.UUID, versions []*FormVersionResponse) {
	if s.staffNames == nil {
		return
	}
	cache := make(map[string]string)
	for _, v := range versions {
		if v == nil || v.PublishedBy == nil {
			continue
		}
		if cached, ok := cache[*v.PublishedBy]; ok {
			if cached != "" {
				n := cached
				v.PublishedByName = &n
			}
			continue
		}
		staffID, err := uuid.Parse(*v.PublishedBy)
		if err != nil {
			cache[*v.PublishedBy] = ""
			continue
		}
		name, err := s.staffNames.ResolveStaffName(ctx, staffID, clinicID)
		if err != nil {
			cache[*v.PublishedBy] = ""
			continue
		}
		cache[*v.PublishedBy] = name
		if name != "" {
			n := name
			v.PublishedByName = &n
		}
	}
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
		Changes:           v.Changes,
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

// buildStyleResponse wraps toStyleResponse and attaches a short-lived signed
// GET URL for the header logo when the record has a logo_key and a signer is
// configured. Callers that have a signer-less Service (tests, local dev) get
// the response back without HeaderLogoURL populated.
func (s *Service) buildStyleResponse(ctx context.Context, rec *StyleVersionRecord) (*FormStyleResponse, error) {
	resp := toStyleResponse(rec)
	if rec.LogoKey != nil && s.logoSigner != nil {
		url, err := s.logoSigner.SignStyleLogoURL(ctx, *rec.LogoKey)
		if err != nil {
			return nil, fmt.Errorf("forms.service.buildStyleResponse: sign url: %w", err)
		}
		resp.HeaderLogoURL = &url
	}
	return resp, nil
}

func toStyleResponse(s *StyleVersionRecord) *FormStyleResponse {
	return &FormStyleResponse{
		Version:      s.Version,
		LogoKey:      s.LogoKey,
		PrimaryColor: s.PrimaryColor,
		FontFamily:   s.FontFamily,
		HeaderExtra:  s.HeaderExtra,
		FooterText:   s.FooterText,
		Config:       s.Config,
		PresetID:     s.PresetID,
		IsActive:     s.IsActive,
		UpdatedAt:    s.CreatedAt.Format(time.RFC3339),
	}
}

// flatMirror holds the top-level fields we keep synced between Config JSON and
// the legacy flat columns.
type flatMirror struct {
	LogoKey      *string
	PrimaryColor *string
	FontFamily   *string
	HeaderExtra  *string
	FooterText   *string
}

// mirrorFromConfig extracts the canonical flat fields from a doc-theme Config
// blob. Returns nil if the blob is not a JSON object. The shape mirrors
// DocThemeConfig on the Flutter side — only the fields we read are parsed.
func mirrorFromConfig(cfg json.RawMessage) *flatMirror {
	var parsed struct {
		Header *struct {
			LogoKey *string `json:"logo_key"`
			Extra   *string `json:"extra_text"`
		} `json:"header"`
		Theme *struct {
			PrimaryColor *string `json:"primary_color"`
			BodyFont     *string `json:"body_font"`
		} `json:"theme"`
		Footer *struct {
			Text *string `json:"text"`
		} `json:"footer"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		return nil
	}
	m := &flatMirror{}
	if parsed.Header != nil {
		m.LogoKey = parsed.Header.LogoKey
		m.HeaderExtra = parsed.Header.Extra
	}
	if parsed.Theme != nil {
		m.PrimaryColor = parsed.Theme.PrimaryColor
		m.FontFamily = parsed.Theme.BodyFont
	}
	if parsed.Footer != nil {
		m.FooterText = parsed.Footer.Text
	}
	return m
}
