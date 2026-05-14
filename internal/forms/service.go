package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/extraction"
)

// LinkedPolicyClauses is the set of enforceable clauses that belong to a single
// published policy version linked to a form. Grouping by policy_version lets the
// service persist per-policy check evidence with a stable version pointer.
type LinkedPolicyClauses struct {
	PolicyID        uuid.UUID
	PolicyVersionID uuid.UUID
	Clauses         []extraction.PolicyClause
}

// PolicyClauseFetcher retrieves enforceable clauses for all policies linked to a form,
// grouped by the policy version they came from.
// Implemented by an adapter in app.go that bridges to the policy repository.
type PolicyClauseFetcher interface {
	GetClausesForForm(ctx context.Context, formID uuid.UUID) ([]LinkedPolicyClauses, error)
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

// PolicyOwnershipVerifier confirms that a policy belongs to the given clinic.
// Used by LinkPolicy to reject cross-tenant links — without this, a malicious
// clinic could enumerate policy UUIDs and attach another tenant's policy to
// their own form, leaking the clause text through the policy-check endpoint.
// Implementations must return domain.ErrNotFound when the policy doesn't
// exist or belongs to a different clinic. Adapter lives in app.go.
type PolicyOwnershipVerifier interface {
	VerifyPolicyOwnership(ctx context.Context, policyID, clinicID uuid.UUID) error
}

// VerticalProvider returns the configured clinical vertical for a clinic
// ("veterinary", "dental", "aged_care", "general_clinic"). Used to frame the
// form-coverage prompt so it targets the right discipline. Adapter in app.go.
type VerticalProvider interface {
	GetClinicVertical(ctx context.Context, clinicID uuid.UUID) (string, error)
}

// TemplateOverlaySource yields prebuilt field specs for a Salvia-installed
// form whose lifecycle state is still "default". GetForm overlays these
// onto the empty draft so freshly-onboarded clinics see the canonical
// YAML content without a heavy at-signup write. Once the clinic edits the
// form (state flips to "forked"), the DB draft becomes authoritative and
// this lookup is bypassed.
//
// Implemented by an adapter in app.go that bridges to salvia_content;
// nil installations disable the overlay (the form simply renders empty).
type TemplateOverlaySource interface {
	FieldsForTemplate(ctx context.Context, templateID string, clinicID uuid.UUID) ([]TemplateField, bool)
}

// TemplateField is the cross-domain field shape the overlay source emits.
// Mirrors the YAML FieldSpec but lives in this package so the forms
// service has no compile-time dependency on salvia_content.
type TemplateField struct {
	Key       string
	Label     string
	Type      string
	Required  bool
	HelpText  string
	AIExtract bool
	PII       bool
	PHI       bool
	Source    string
	Config    map[string]any
}

// Service handles business logic for the forms module.
type Service struct {
	repo           repo
	clauses        PolicyClauseFetcher
	checker        extraction.FormCoverageChecker
	logoSigner     StyleLogoSigner
	logoUploader   StyleLogoUploader
	staffNames     StaffNameResolver
	policyVerifier PolicyOwnershipVerifier
	verticals      VerticalProvider
	templates      TemplateOverlaySource
}

// NewService constructs a forms Service.
// Pass nil for clauses/checker to disable policy checking (tests, local dev).
// Pass nil for signer/uploader to disable the doc-theme logo upload endpoint.
// Pass nil for staffNames to skip name resolution (API will return raw UUIDs).
// Pass nil for policyVerifier to skip cross-tenant policy link checks (unit
// tests); in production this MUST be wired to prevent clause-text leakage.
func NewService(r repo, clauses PolicyClauseFetcher, checker extraction.FormCoverageChecker, signer StyleLogoSigner, uploader StyleLogoUploader, staffNames StaffNameResolver, policyVerifier PolicyOwnershipVerifier) *Service {
	return &Service{repo: r, clauses: clauses, checker: checker, logoSigner: signer, logoUploader: uploader, staffNames: staffNames, policyVerifier: policyVerifier}
}

// SetVerticalProvider wires the clinic-vertical resolver so the form-coverage
// AI prompt can be framed for the right discipline. Optional — without it,
// the coverage check runs with a generic "clinic type not specified" preamble.
func (s *Service) SetVerticalProvider(v VerticalProvider) {
	s.verticals = v
}

// SetTemplateOverlaySource wires the YAML template lookup so GetForm can
// overlay prebuilt fields onto rows whose salvia_template_state is still
// "default". Optional — without it, default-state Salvia forms render with
// no fields (the same behaviour they shipped with).
func (s *Service) SetTemplateOverlaySource(t TemplateOverlaySource) {
	s.templates = t
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

// SystemHeaderConfig describes the per-form-version "patient header" card
// that renders at the top of every note (review screen + PDF). Values come
// from the linked subject row at note-render time, NOT from AI extraction —
// this is the system's source of truth for patient identity.
//
//   - Enabled: when false, the form has no patient header card and the form
//     fields render flush to the doc-theme chrome.
//   - Fields: ordered list of patient-row attributes to surface. Valid values
//     are validated in app code (see SystemHeaderField allow-list); adding a
//     new identifier is code-only, no migration.
//
//nolint:revive
type SystemHeaderConfig struct {
	Enabled bool     `json:"enabled"`
	Fields  []string `json:"fields"`
}

// FormVersionResponse is the API-safe representation of a form version.
//
//nolint:revive
type FormVersionResponse struct {
	ID     string                   `json:"id"`
	FormID string                   `json:"form_id"`
	Status domain.FormVersionStatus `json:"status"`
	// Kind distinguishes real version rows from synthetic timeline entries
	// the service injects for compliance (e.g. "retire"). Empty/absent means
	// a regular version row — front-ends should default to "version" in that
	// case. Values: "version" (default) · "retire".
	Kind          string             `json:"kind,omitempty"`
	VersionMajor  *int               `json:"version_major,omitempty"`
	VersionMinor  *int               `json:"version_minor,omitempty"`
	ChangeType    *domain.ChangeType `json:"change_type,omitempty"`
	ChangeSummary *string            `json:"change_summary,omitempty"`
	// Changes is a raw JSON array of typed ops ({op, ...}) computed by the editor
	// at publish time. Shape evolves over time; consumers should tolerate unknown
	// op kinds.
	Changes    json.RawMessage `json:"changes,omitempty"`
	RollbackOf *string         `json:"rollback_of,omitempty"`
	// PolicyCheckResult is a JSON array of PolicyCheckResultEntry — one entry
	// per linked policy at check time. Stored opaque so the shape can evolve
	// without migrations; nil until a check has run on the draft.
	PolicyCheckResult json.RawMessage  `json:"policy_check_result,omitempty"`
	PolicyCheckAt     *string          `json:"policy_check_at,omitempty"`
	PublishedAt       *string          `json:"published_at,omitempty"`
	PublishedBy       *string          `json:"published_by,omitempty"`
	PublishedByName   *string          `json:"published_by_name,omitempty"`
	CreatedAt         string           `json:"created_at"`
	Fields            []*FieldResponse `json:"fields,omitempty"`
	// SystemHeader carries the patient-header card config. Always populated
	// — the column has a non-null default — so clients can render the card
	// without falling back to inferred state.
	SystemHeader *SystemHeaderConfig `json:"system_header,omitempty"`
	// GenerationMetadata is the AI-generation provenance JSONB
	// (provider, model, prompt_hash, staff_id, timestamps, repair counts).
	// NULL/absent for human-authored versions; present means the Flutter
	// editor renders an "AI drafted — review before publishing" pill.
	GenerationMetadata json.RawMessage `json:"generation_metadata,omitempty"`
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
	RetiredBy     *string  `json:"retired_by,omitempty"`
	RetiredByName *string  `json:"retired_by_name,omitempty"`
	// Draft is non-nil when an editable draft version exists.
	Draft *FormVersionResponse `json:"draft,omitempty"`
	// LatestPublished is the most recent frozen version; nil on a brand-new form.
	LatestPublished *FormVersionResponse `json:"latest_published,omitempty"`
	// Marketplace lineage — non-empty only when the form was imported from a
	// marketplace listing. Powers the form-editor banner and the sibling-form
	// link in the buyer-side upgrade flow.
	SourceMarketplaceListingID     *string `json:"source_marketplace_listing_id,omitempty"`
	SourceMarketplaceVersionID     *string `json:"source_marketplace_version_id,omitempty"`
	SourceMarketplaceAcquisitionID *string `json:"source_marketplace_acquisition_id,omitempty"`
	// Salvia v1 prebuilt content lineage — non-empty only when the form was
	// installed by the salvia_content materialiser. Powers the "Made by
	// Salvia v1" badge and the Library panel.
	SalviaTemplateID      *string `json:"salvia_template_id,omitempty"`
	SalviaTemplateVersion *int    `json:"salvia_template_version,omitempty"`
	SalviaTemplateState   *string `json:"salvia_template_state,omitempty" enum:"default,forked,deleted"`
	FrameworkCurrencyDate *string `json:"framework_currency_date,omitempty"`
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
	// PerDocOverrides is a JSON object keyed by doc-type slug
	// (signed_note | cd_register | …) with partial DocTheme blobs
	// the renderer merges over Config when rendering that doc-type.
	// Always non-NULL ('{}'); omitted from JSON when empty.
	PerDocOverrides json.RawMessage `json:"per_doc_overrides,omitempty"`
	IsActive        bool            `json:"is_active"`
	UpdatedAt       string          `json:"updated_at"`
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

// PolicyCheckClauseEntry is a single clause's pass/fail result inside a policy
// check. BlockID is stable across policy versions so clients can key by it to
// render diffs, and parity lets the UI colour-code results.
//
//nolint:revive
type PolicyCheckClauseEntry struct {
	BlockID   string `json:"block_id"`
	Status    string `json:"status"`    // "satisfied" | "violated"
	Reasoning string `json:"reasoning"` // one-sentence explanation
	Parity    string `json:"parity"`    // "high" | "medium" | "low"
}

// PolicyCheckResultEntry is one linked policy's contribution to a form's policy
// check. ResultPct is parity-weighted coverage (high=3, medium=2, low=1).
// Narrative is included once per group (duplicated when the model produces a
// single overall analysis). Stored inside form_versions.policy_check_result as
// one element of a JSONB array.
//
//nolint:revive
type PolicyCheckResultEntry struct {
	PolicyID        string                   `json:"policy_id"`
	PolicyVersionID string                   `json:"policy_version_id"`
	ResultPct       float64                  `json:"result_pct"`
	Narrative       string                   `json:"narrative,omitempty"`
	Clauses         []PolicyCheckClauseEntry `json:"clauses"`
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
	// Marketplace lineage — supplied only by the marketplace importer adapter.
	// Nil for clinic-authored forms.
	SourceMarketplaceListingID     *uuid.UUID
	SourceMarketplaceVersionID     *uuid.UUID
	SourceMarketplaceAcquisitionID *uuid.UUID
	// Salvia-provided-content lineage — supplied only by the salvia_content
	// materialiser at clinic-create. Mutually exclusive with marketplace
	// lineage.
	SalviaTemplateID      *string
	SalviaTemplateVersion *int
	SalviaTemplateState   *string // "default" | "forked" | "deleted"
	FrameworkCurrencyDate *time.Time
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
	// SystemHeader is the patient-header card config. nil means "leave the
	// existing config alone" — UpdateDraft does NOT clobber the column when
	// the input is absent. Clients that want to disable the card send
	// `{enabled: false, fields: []}` explicitly.
	SystemHeader *SystemHeaderConfig
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

// DeleteGroupInput holds the IDs needed to delete a form group.
type DeleteGroupInput struct {
	GroupID  uuid.UUID
	ClinicID uuid.UUID
}

// DeleteGroupResponse reports how many forms got moved out of the deleted
// folder (their group_id was set to NULL so they appear in "All forms").
type DeleteGroupResponse struct {
	ReparentedForms int `json:"reparented_forms" doc:"Number of forms that were moved out of this folder before deletion."`
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
	// PerDocOverrides is the optional per-doc-type override map
	// (slug → partial DocTheme JSON). nil leaves the existing
	// column value untouched; '{}' clears it.
	PerDocOverrides json.RawMessage
}

// ── Service methods ───────────────────────────────────────────────────────────

// CreateForm creates a new form and an empty draft version.
// Returns the created form with the draft attached.
func (s *Service) CreateForm(ctx context.Context, input CreateFormInput) (*FormResponse, error) {
	// Verify the target folder belongs to the caller's clinic. Without this
	// a caller could attach their form to another clinic's group_id (the
	// forms↔form_groups FK only enforces "real row", not tenant). Mirrors
	// the policy-ownership check in LinkPolicy.
	if input.GroupID != nil {
		if _, err := s.repo.GetGroupByID(ctx, *input.GroupID, input.ClinicID); err != nil {
			return nil, fmt.Errorf("forms.service.CreateForm: verify group: %w", err)
		}
	}

	formID := domain.NewID()
	tags := normalizeTags(input.Tags)

	form, draft, err := s.repo.CreateFormWithDraft(ctx, CreateFormWithDraftParams{
		Form: CreateFormParams{
			ID:                             formID,
			ClinicID:                       input.ClinicID,
			GroupID:                        input.GroupID,
			Name:                           input.Name,
			Description:                    input.Description,
			OverallPrompt:                  input.OverallPrompt,
			Tags:                           tags,
			CreatedBy:                      input.StaffID,
			SourceMarketplaceListingID:     input.SourceMarketplaceListingID,
			SourceMarketplaceVersionID:     input.SourceMarketplaceVersionID,
			SourceMarketplaceAcquisitionID: input.SourceMarketplaceAcquisitionID,
			SalviaTemplateID:               input.SalviaTemplateID,
			SalviaTemplateVersion:          input.SalviaTemplateVersion,
			SalviaTemplateState:            input.SalviaTemplateState,
			FrameworkCurrencyDate:          input.FrameworkCurrencyDate,
		},
		DraftID: domain.NewID(),
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateForm: %w", err)
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
		s.overlayTemplateFields(ctx, form, resp.Draft)
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
// ListFormsByMarketplaceListing returns every form in a clinic that descended
// from a given marketplace listing (sibling forms across imported versions).
// Used by the form-editor banner and the marketplace upgrade UX so the buyer
// can see their pre-existing forms when a new version arrives.
//
// Light response — does NOT attach draft/version data, just the form metadata
// the UI needs to render a "your other forms from this listing" link list.
func (s *Service) ListFormsByMarketplaceListing(ctx context.Context, clinicID, listingID uuid.UUID) ([]*FormResponse, error) {
	forms, err := s.repo.ListByMarketplaceListing(ctx, clinicID, listingID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListFormsByMarketplaceListing: %w", err)
	}
	out := make([]*FormResponse, len(forms))
	for i, f := range forms {
		out[i] = toFormResponse(f)
	}
	return out, nil
}

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
		return nil, fmt.Errorf("forms.service.UpdateDraft: form is retired: %w", domain.ErrConflict)
	}

	// Verify the target folder belongs to the caller's clinic before
	// re-parenting the form. Without this a caller could move their form
	// under another clinic's group_id (FK enforces real row only, not
	// tenant). Mirrors the CreateForm guard.
	if input.GroupID != nil {
		if _, err := s.repo.GetGroupByID(ctx, *input.GroupID, input.ClinicID); err != nil {
			return nil, fmt.Errorf("forms.service.UpdateDraft: verify group: %w", err)
		}
	}

	// Update form-level metadata.
	tags := normalizeTags(input.Tags)
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

	// Ensure a draft version exists. After a publish, no draft is in the
	// table, so the first save back into the editor needs to create one.
	// Two concurrent saves can both observe NotFound and both try to
	// INSERT — the partial unique index `idx_form_versions_one_draft`
	// keeps only the first, and the loser comes back as ErrConflict. When
	// that happens we re-read the winner's draft and continue: the
	// caller's intent ("write these fields onto the current draft") is
	// satisfied regardless of which transaction created it.
	draft, err := s.repo.GetDraftVersion(ctx, input.FormID)
	if errors.Is(err, domain.ErrNotFound) {
		draft, err = s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
			ID:        domain.NewID(),
			FormID:    input.FormID,
			CreatedBy: input.StaffID,
		})
		if errors.Is(err, domain.ErrConflict) {
			draft, err = s.repo.GetDraftVersion(ctx, input.FormID)
		}
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

	if input.SystemHeader != nil {
		cfg, err := encodeSystemHeader(input.SystemHeader)
		if err != nil {
			return nil, fmt.Errorf("forms.service.UpdateDraft: system_header: %w", err)
		}
		draft, err = s.repo.UpdateDraftSystemHeader(ctx, draft.ID, form.ClinicID, cfg)
		if err != nil {
			return nil, fmt.Errorf("forms.service.UpdateDraft: system_header: %w", err)
		}
	}

	resp := toFormResponse(form)
	resp.Draft = toVersionResponse(draft, fields)
	return resp, nil
}

// DiscardDraft deletes the current draft of a form.
//
// Two semantics, branched on whether the form has ever been published:
//
//   - **Has a published version** — the draft is dropped, the latest
//     published version remains the active one. Returns
//     [domain.ErrNotFound] if there's nothing to drop.
//   - **Never published** — the entire form row is cascade-deleted along
//     with its draft, draft fields, and `form_policies` links. The
//     "discard draft" affordance doubles as "delete this form" while
//     it's still draft-only, since there's no published artefact left
//     to keep.
//
// Retired forms always reject — use the rollback workflow if you need
// the fields back from a prior published version.
func (s *Service) DiscardDraft(ctx context.Context, formID, clinicID uuid.UUID) error {
	form, err := s.repo.GetFormByID(ctx, formID, clinicID)
	if err != nil {
		return fmt.Errorf("forms.service.DiscardDraft: %w", err)
	}
	if form.ArchivedAt != nil {
		return fmt.Errorf("forms.service.DiscardDraft: form is retired: %w", domain.ErrConflict)
	}

	// Detect "never published" by asking for the latest published row.
	// Anything other than ErrNotFound means a real publish exists OR the
	// repo blew up — surface both as errors.
	if _, err := s.repo.GetLatestPublishedVersion(ctx, formID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			if err := s.repo.DeleteFormCascade(ctx, formID, clinicID); err != nil {
				return fmt.Errorf("forms.service.DiscardDraft: cascade: %w", err)
			}
			return nil
		}
		return fmt.Errorf("forms.service.DiscardDraft: check published: %w", err)
	}

	// Has a published version — drop only the draft.
	if err := s.repo.DeleteDraftVersion(ctx, formID); err != nil {
		return fmt.Errorf("forms.service.DiscardDraft: %w", err)
	}
	return nil
}

// PublishForm freezes the current draft, assigns a semver number, and makes the
// form available for use in audio processing. Returns the full form resource
// (with the new latest_published attached) so the editor can refresh without
// a follow-up GET.
//
// Rejects an empty draft: publishing a zero-field form would produce a
// template that AI extraction can't populate and the PDF renderer has
// nothing to render. The client gates on this too but a direct API call
// would otherwise poison the version history.
//
// Tolerates a concurrent publish racing the same version number by
// recomputing and retrying once when the DB's partial unique index rejects
// the insert — simpler than a SELECT FOR UPDATE and good enough for the
// two-tab case that triggers it in practice.
func (s *Service) PublishForm(ctx context.Context, input PublishFormInput) (*FormResponse, error) {
	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: form is retired: %w", domain.ErrConflict)
	}

	draft, err := s.repo.GetDraftVersion(ctx, input.FormID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("forms.service.PublishForm: no draft to publish — edit the form first: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("forms.service.PublishForm: %w", err)
	}

	fields, err := s.repo.GetFieldsByVersionID(ctx, draft.ID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.PublishForm: draft fields: %w", err)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("forms.service.PublishForm: draft has no fields: %w", domain.ErrConflict)
	}

	for attempt := 0; attempt < 2; attempt++ {
		major, minor := NextVersion(input.ChangeType, nil, nil)
		prev, err := s.repo.GetLatestPublishedVersion(ctx, input.FormID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("forms.service.PublishForm: prev version: %w", err)
		}
		if prev != nil {
			major, minor = NextVersion(input.ChangeType, prev.VersionMajor, prev.VersionMinor)
		}

		_, err = s.repo.PublishDraftVersion(ctx, PublishDraftVersionParams{
			ID:            draft.ID,
			ClinicID:      input.ClinicID,
			VersionMajor:  major,
			VersionMinor:  minor,
			ChangeType:    input.ChangeType,
			ChangeSummary: input.ChangeSummary,
			Changes:       input.Changes,
			PublishedBy:   input.StaffID,
			PublishedAt:   domain.TimeNow(),
		})
		if err == nil {
			return s.GetForm(ctx, input.FormID, input.ClinicID)
		}
		if errors.Is(err, domain.ErrConflict) && attempt == 0 {
			continue
		}
		return nil, fmt.Errorf("forms.service.PublishForm: %w", err)
	}
	return nil, fmt.Errorf("forms.service.PublishForm: could not assign version number: %w", domain.ErrConflict)
}

// RunPolicyCheck calls the AI to assess whether the form's fields cover the
// requirements of all linked policy clauses. Saves the result on the draft.
// Returns ErrConflict if no policies are linked or no checker is configured.
func (s *Service) RunPolicyCheck(ctx context.Context, formID, clinicID, staffID uuid.UUID) (*FormResponse, error) {
	form, err := s.repo.GetFormByID(ctx, formID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: form is retired: %w", domain.ErrConflict)
	}

	draft, err := s.repo.GetDraftVersion(ctx, formID)
	if errors.Is(err, domain.ErrNotFound) {
		// No draft yet — clone the latest published version so the user can
		// run policy-check against the current form without a manual edit
		// step. Errors out if the form has never been published.
		latest, lerr := s.repo.GetLatestPublishedVersion(ctx, formID)
		if lerr != nil {
			if errors.Is(lerr, domain.ErrNotFound) {
				return nil, fmt.Errorf("forms.service.RunPolicyCheck: form has no draft or published version — add fields first: %w", domain.ErrConflict)
			}
			return nil, fmt.Errorf("forms.service.RunPolicyCheck: latest published: %w", lerr)
		}
		latestFields, lerr := s.repo.GetFieldsByVersionID(ctx, latest.ID)
		if lerr != nil {
			return nil, fmt.Errorf("forms.service.RunPolicyCheck: latest fields: %w", lerr)
		}
		if rerr := s.resetDraftToFields(ctx, formID, staffID, latestFields); rerr != nil {
			return nil, fmt.Errorf("forms.service.RunPolicyCheck: seed draft: %w", rerr)
		}
		draft, err = s.repo.GetDraftVersion(ctx, formID)
	}
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: %w", err)
	}

	if s.clauses == nil || s.checker == nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: policy checker not configured: %w", domain.ErrConflict)
	}

	groups, err := s.clauses.GetClausesForForm(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: get clauses: %w", err)
	}
	// Flatten for the checker — the group shape is preserved on `groups` and
	// regrouped after the AI call.
	var flatClauses []extraction.PolicyClause
	for _, g := range groups {
		flatClauses = append(flatClauses, g.Clauses...)
	}
	if len(flatClauses) == 0 {
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

	vertical := ""
	if s.verticals != nil {
		v, vErr := s.verticals.GetClinicVertical(ctx, clinicID)
		if vErr == nil {
			vertical = v
		}
	}

	coverage, err := s.checker.CheckFormCoverage(ctx, vertical, overallPrompt, specs, flatClauses)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: checker: %w", err)
	}

	entries := buildPolicyCheckEntries(groups, coverage)
	payload, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: marshal result: %w", err)
	}

	if _, err := s.repo.SavePolicyCheckResult(ctx, SavePolicyCheckParams{
		VersionID: draft.ID,
		ClinicID:  clinicID,
		Result:    string(payload),
		CheckedBy: staffID,
		CheckedAt: domain.TimeNow(),
	}); err != nil {
		return nil, fmt.Errorf("forms.service.RunPolicyCheck: save: %w", err)
	}

	// Return the full form so the editor rehydrates draft + latest_published
	// in a single call — same pattern as updateDraft / publish / rollback.
	return s.GetForm(ctx, formID, clinicID)
}

// buildPolicyCheckEntries regroups a flat list of clause results back into one
// entry per linked policy, computing a parity-weighted result_pct per policy.
// Weights: high=3, medium=2, low=1. An entry with no clauses scores 100 (the
// policy has nothing to violate).
func buildPolicyCheckEntries(groups []LinkedPolicyClauses, coverage *extraction.FormCoverageResult) []PolicyCheckResultEntry {
	// Index clause results by block_id for regrouping.
	resultByBlock := make(map[string]extraction.ClauseCheckResult, len(coverage.Clauses))
	for _, cr := range coverage.Clauses {
		resultByBlock[cr.BlockID] = cr
	}

	parityWeight := func(p string) float64 {
		switch p {
		case "high":
			return 3
		case "medium":
			return 2
		case "low":
			return 1
		default:
			return 1
		}
	}

	entries := make([]PolicyCheckResultEntry, 0, len(groups))
	for _, g := range groups {
		clauses := make([]PolicyCheckClauseEntry, 0, len(g.Clauses))
		var total, earned float64
		for _, c := range g.Clauses {
			w := parityWeight(c.Parity)
			total += w
			res, ok := resultByBlock[c.BlockID]
			status := "violated"
			reasoning := "clause not assessed"
			if ok {
				status = res.Status
				reasoning = res.Reasoning
			}
			if status == "satisfied" {
				earned += w
			}
			clauses = append(clauses, PolicyCheckClauseEntry{
				BlockID:   c.BlockID,
				Status:    status,
				Reasoning: reasoning,
				Parity:    c.Parity,
			})
		}
		pct := 100.0
		if total > 0 {
			pct = (earned / total) * 100.0
		}
		entries = append(entries, PolicyCheckResultEntry{
			PolicyID:        g.PolicyID.String(),
			PolicyVersionID: g.PolicyVersionID.String(),
			ResultPct:       pct,
			Narrative:       coverage.Narrative,
			Clauses:         clauses,
		})
	}
	return entries
}

// RollbackForm publishes a new immutable version whose fields are copied from
// a prior published version. The new version gets its own semver number
// (minor bump over the current latest) and records rollback_of so the history
// shows the provenance. The draft is also overwritten with the target's
// fields so the editor reflects the restored state — users expect clicking
// "Rollback to v1" to bring back those fields in the editor, not preserve
// orphan WIP from the discarded version.
//
// Version insert + field copy happen in a single transaction in the repo
// layer, so a partial failure can't leave a zero-field published version in
// the history. A unique-index collision on the semver pair (two concurrent
// rollbacks, or a rollback racing a publish) is retried once with a
// recomputed version number.
func (s *Service) RollbackForm(ctx context.Context, input RollbackFormInput) (*FormResponse, error) {
	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.RollbackForm: form is retired: %w", domain.ErrConflict)
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

	for attempt := 0; attempt < 2; attempt++ {
		// Compute next version. Minor bump over the current latest — rollback
		// is conceptually "a small course correction", not a breaking
		// re-architecture. Fall back to 1.0 when there is somehow no latest
		// (shouldn't happen since the target itself is published).
		latest, err := s.repo.GetLatestPublishedVersion(ctx, input.FormID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("forms.service.RollbackForm: latest: %w", err)
		}
		major, minor := NextVersion(domain.ChangeTypeMinor, nil, nil)
		if latest != nil {
			major, minor = NextVersion(domain.ChangeTypeMinor, latest.VersionMajor, latest.VersionMinor)
		}

		newID := domain.NewID()
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

		_, _, err = s.repo.CreatePublishedVersionWithFields(ctx, CreatePublishedVersionParams{
			ID:            newID,
			FormID:        input.FormID,
			VersionMajor:  major,
			VersionMinor:  minor,
			ChangeType:    domain.ChangeTypeMinor,
			ChangeSummary: &summary,
			RollbackOf:    &input.TargetVersionID,
			PublishedBy:   input.StaffID,
			PublishedAt:   domain.TimeNow(),
		}, fieldParams)
		if err == nil {
			// Restore draft to mirror target so editor reopens on rolled-back
			// fields. Published rollback already committed at this point — a
			// draft-sync failure must NOT bubble up as a 5xx, or the caller
			// thinks rollback failed when the published row exists. Log and
			// continue; user can re-sync by reloading the editor.
			if draftErr := s.resetDraftToFields(ctx, input.FormID, input.StaffID, sourceFields); draftErr != nil {
				slog.Warn("forms.service.RollbackForm: draft sync failed after publish",
					"form_id", input.FormID,
					"version_id", newID,
					"error", draftErr.Error(),
				)
			}
			return s.GetForm(ctx, input.FormID, input.ClinicID)
		}
		if errors.Is(err, domain.ErrConflict) && attempt == 0 {
			continue
		}
		return nil, fmt.Errorf("forms.service.RollbackForm: %w", err)
	}
	return nil, fmt.Errorf("forms.service.RollbackForm: could not assign version number: %w", domain.ErrConflict)
}

// resetDraftToFields replaces (or creates) the form's draft so it carries an
// independent copy of the supplied source fields. Used by rollback to keep
// the editor aligned with the just-published rolled-back version.
func (s *Service) resetDraftToFields(ctx context.Context, formID, staffID uuid.UUID, sourceFields []*FieldRecord) error {
	draft, err := s.repo.GetDraftVersion(ctx, formID)
	if errors.Is(err, domain.ErrNotFound) {
		draft, err = s.repo.CreateDraftVersion(ctx, CreateDraftVersionParams{
			ID:        domain.NewID(),
			FormID:    formID,
			CreatedBy: staffID,
		})
	}
	if err != nil {
		return fmt.Errorf("resetDraftToFields: draft: %w", err)
	}
	fieldParams := make([]CreateFieldParams, len(sourceFields))
	for i, f := range sourceFields {
		fieldParams[i] = CreateFieldParams{
			ID:             domain.NewID(),
			FormVersionID:  draft.ID,
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
	if _, err := s.repo.ReplaceFields(ctx, draft.ID, fieldParams); err != nil {
		return fmt.Errorf("resetDraftToFields: replace: %w", err)
	}
	return nil
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
		RetiredBy:    input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.RetireForm: %w", err)
	}

	return toFormResponse(retired), nil
}

// GetVersion returns a single version (draft, published, or archived) along
// with its fields. Used by the note review page when a note was filed against
// a version that is no longer the current draft or latest published — list
// endpoints omit fields to keep payloads small, so the renderer needs a
// targeted lookup that includes them.
func (s *Service) GetVersion(ctx context.Context, formID, clinicID, versionID uuid.UUID) (*FormVersionResponse, error) {
	if _, err := s.repo.GetFormByID(ctx, formID, clinicID); err != nil {
		return nil, fmt.Errorf("forms.service.GetVersion: %w", err)
	}
	v, err := s.repo.GetVersionByID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.GetVersion: %w", err)
	}
	if v.FormID != formID {
		return nil, fmt.Errorf("forms.service.GetVersion: %w", domain.ErrForbidden)
	}
	fields, err := s.repo.GetFieldsByVersionID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.GetVersion: fields: %w", err)
	}
	resp := toVersionResponse(v, fields)
	s.resolvePublisherName(ctx, clinicID, resp)
	return resp, nil
}

// ListVersions returns the compliance trail for a form: every published
// version (newest first), a synthetic "retire" entry when the form itself is
// archived, and one synthetic "policy_unlinked" entry per policy that was
// retired while linked to this form. The trail is a single flat list keyed
// by timestamp so the UI can render it top-to-bottom.
func (s *Service) ListVersions(ctx context.Context, formID, clinicID uuid.UUID) (*FormVersionListResponse, error) {
	form, err := s.repo.GetFormByID(ctx, formID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListVersions: %w", err)
	}

	versions, err := s.repo.ListPublishedVersions(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListVersions: %w", err)
	}

	unlinks, err := s.repo.ListPolicyUnlinkEvents(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.ListVersions: %w", err)
	}

	items := make([]*FormVersionResponse, 0, len(versions)+len(unlinks)+1)
	if form.ArchivedAt != nil {
		items = append(items, buildRetireEntry(form))
	}
	for _, u := range unlinks {
		items = append(items, buildPolicyUnlinkEntry(form.ID, u))
	}
	for _, v := range versions {
		items = append(items, toVersionResponse(v, nil))
	}
	// Synthetic entries carry their own timestamps; keep the list newest-first
	// so the drawer renders chronologically without bespoke UI sort logic.
	sortVersionsDesc(items)
	s.resolvePublisherNames(ctx, clinicID, items)
	return &FormVersionListResponse{Items: items}, nil
}

// buildPolicyUnlinkEntry fabricates a timeline row for "policy X was unlinked
// because the policy was retired." Not a real form_versions row — no semver,
// no publishedBy (the retire is a policy-side action by a staff member whose
// identity belongs in the policy trail, not the form trail).
func buildPolicyUnlinkEntry(formID uuid.UUID, e *PolicyUnlinkEventRecord) *FormVersionResponse {
	name := "Linked policy"
	if e.PolicyNameSnapshot != nil && *e.PolicyNameSnapshot != "" {
		name = *e.PolicyNameSnapshot
	}
	summary := fmt.Sprintf("%s retired — unlinked from form", name)
	if e.UnlinkedReason != nil && *e.UnlinkedReason != "" {
		summary = fmt.Sprintf("%s retired — unlinked (%s)", name, *e.UnlinkedReason)
	}
	ts := e.UnlinkedAt.Format(time.RFC3339)
	return &FormVersionResponse{
		// Deterministic ID so the front-end can key on it without colliding
		// with real version UUIDs or other synthetic entries.
		ID:            formID.String() + "-unlink-" + e.PolicyID.String(),
		FormID:        formID.String(),
		Status:        domain.FormVersionStatusPublished,
		Kind:          "policy_unlinked",
		ChangeSummary: &summary,
		PublishedAt:   &ts,
		CreatedAt:     ts,
	}
}

// sortVersionsDesc sorts trail entries newest-first by their CreatedAt timestamp.
// Entries without a timestamp (defensive — shouldn't happen in practice) sink
// to the bottom rather than breaking the sort.
func sortVersionsDesc(items []*FormVersionResponse) {
	sort.SliceStable(items, func(i, j int) bool {
		a := items[i].CreatedAt
		b := items[j].CreatedAt
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		return a > b
	})
}

// buildRetireEntry fabricates a timeline row representing the form's retire
// event. It isn't a real form_versions row — retirement is a form-level
// state change — but the UI renders the trail from a single list, so we
// synthesise one here. ID is deterministic (formID suffixed with "-retire")
// so the front-end can key on it without colliding with real version UUIDs.
func buildRetireEntry(f *FormRecord) *FormVersionResponse {
	summary := "Form retired"
	if f.RetireReason != nil && *f.RetireReason != "" {
		summary = fmt.Sprintf("Form retired — %s", *f.RetireReason)
	}
	var publishedAt *string
	if f.ArchivedAt != nil {
		s := f.ArchivedAt.Format(time.RFC3339)
		publishedAt = &s
	}
	var publishedBy *string
	if f.RetiredBy != nil {
		s := f.RetiredBy.String()
		publishedBy = &s
	}
	createdAt := ""
	if f.ArchivedAt != nil {
		createdAt = f.ArchivedAt.Format(time.RFC3339)
	}
	return &FormVersionResponse{
		ID:            f.ID.String() + "-retire",
		FormID:        f.ID.String(),
		Status:        domain.FormVersionStatusArchived,
		Kind:          "retire",
		ChangeSummary: &summary,
		PublishedAt:   publishedAt,
		PublishedBy:   publishedBy,
		CreatedAt:     createdAt,
	}
}

// ── Groups ────────────────────────────────────────────────────────────────────

// CreateGroup creates a new form group (folder) for a clinic.
//
// Pre-checks that no existing group in the clinic carries the same name
// (case-insensitive) — the schema does not enforce this and duplicate
// folder names cause confusing UI ("Post op" / "Post op" twice in the
// sidebar). Returns domain.ErrConflict on collision.
func (s *Service) CreateGroup(ctx context.Context, input CreateGroupInput) (*FormGroupResponse, error) {
	if err := s.assertGroupNameAvailable(ctx, input.ClinicID, input.Name, uuid.Nil); err != nil {
		return nil, fmt.Errorf("forms.service.CreateGroup: %w", err)
	}
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

// normalizeTags trims surrounding whitespace, drops empty entries, and
// deduplicates case-insensitively while preserving the first-seen case
// + original ordering. Without this the tags column can accumulate noise
// like ["urgent", "urgent", "Urgent", ""] from repeated saves, which
// degrades search recall and clutters the chip row in the UI.
//
// Returns a non-nil empty slice (never nil) so the column always lands
// as `'{}'` rather than NULL, matching the existing contract.
func normalizeTags(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	return out
}

// assertGroupNameAvailable returns domain.ErrConflict if another group in
// the same clinic already uses the given name (case-insensitive). Pass
// uuid.Nil for `excludeID` on create; on rename pass the renaming group's
// id so it doesn't collide with itself.
func (s *Service) assertGroupNameAvailable(ctx context.Context, clinicID uuid.UUID, name string, excludeID uuid.UUID) error {
	groups, err := s.repo.ListGroups(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("forms.service.assertGroupNameAvailable: %w", err)
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, g := range groups {
		if g.ID == excludeID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(g.Name)) == want {
			return fmt.Errorf("a folder named %q already exists: %w", g.Name, domain.ErrConflict)
		}
	}
	return nil
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

// UpdateGroup updates a group's name and description. Rejects renames
// that would collide with another folder in the same clinic
// (case-insensitive). Renaming a folder to its own current name is fine.
func (s *Service) UpdateGroup(ctx context.Context, input UpdateGroupInput) (*FormGroupResponse, error) {
	if err := s.assertGroupNameAvailable(ctx, input.ClinicID, input.Name, input.GroupID); err != nil {
		return nil, fmt.Errorf("forms.service.UpdateGroup: %w", err)
	}
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

// DeleteGroup removes a form group (folder) and reparents the forms that
// lived inside it to "no folder" so they remain visible in the unsorted
// "All forms" list. Reparent + delete happen atomically inside the
// repository's transaction.
func (s *Service) DeleteGroup(ctx context.Context, input DeleteGroupInput) (*DeleteGroupResponse, error) {
	reparented, err := s.repo.DeleteGroup(ctx, input.GroupID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.DeleteGroup: %w", err)
	}
	return &DeleteGroupResponse{ReparentedForms: reparented}, nil
}

// ── Policies ──────────────────────────────────────────────────────────────────

// LinkPolicy attaches a policy to a form.
//
// Verifies both sides belong to the caller's clinic: the form is fetched
// scoped to clinicID, and the policy is verified via PolicyOwnershipVerifier.
// Without the second check, a caller with permission to edit their own forms
// could attach any policy UUID they can guess and then exfiltrate its clause
// text through the policy-check endpoint.
func (s *Service) LinkPolicy(ctx context.Context, formID, clinicID, policyID, staffID uuid.UUID) error {
	if _, err := s.repo.GetFormByID(ctx, formID, clinicID); err != nil {
		return fmt.Errorf("forms.service.LinkPolicy: %w", err)
	}
	if s.policyVerifier != nil {
		if err := s.policyVerifier.VerifyPolicyOwnership(ctx, policyID, clinicID); err != nil {
			return fmt.Errorf("forms.service.LinkPolicy: verify policy: %w", err)
		}
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
//
// Concurrency: the (clinic_id, version) unique constraint can collide if two
// staff hit save in the same instant — both reads of the current version land
// on the same `nextVer` and one INSERT loses. The repo maps that to
// domain.ErrConflict; this loop recomputes the next version and retries once
// so the second save lands on the actual next-next version instead of bubbling
// a 409 up to the UI. Mirrors the retry loop in PublishForm / RollbackForm.
func (s *Service) UpdateStyle(ctx context.Context, input UpdateStyleInput) (*FormStyleResponse, error) {
	for attempt := 0; attempt < 2; attempt++ {
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

		// Per-doc overrides — when caller didn't supply, carry the
		// existing column value forward so a save that only edits the
		// base config doesn't clobber per-doc tweaks.
		perDoc := input.PerDocOverrides
		if perDoc == nil && current != nil {
			perDoc = current.PerDocOverrides
		}

		style, err := s.repo.CreateStyleVersion(ctx, CreateStyleVersionParams{
			ID:              domain.NewID(),
			ClinicID:        input.ClinicID,
			Version:         nextVer,
			LogoKey:         logoKey,
			PrimaryColor:    primary,
			FontFamily:      font,
			HeaderExtra:     header,
			FooterText:      footer,
			Config:          input.Config,
			PresetID:        input.PresetID,
			PerDocOverrides: perDoc,
			CreatedBy:       input.StaffID,
		})
		if err == nil {
			return s.buildStyleResponse(ctx, style)
		}
		if errors.Is(err, domain.ErrConflict) && attempt == 0 {
			continue
		}
		return nil, fmt.Errorf("forms.service.UpdateStyle: %w", err)
	}
	return nil, fmt.Errorf("forms.service.UpdateStyle: could not assign version number: %w", domain.ErrConflict)
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

// overlayTemplateFields paints the YAML field specs onto a Salvia-installed
// form's empty draft. No-op when:
//   - the overlay source is not wired (tests, local dev),
//   - the form isn't from Salvia (SalviaTemplateID nil), or
//   - the form has been edited (SalviaTemplateState != "default"), at which
//     point the DB draft is authoritative and the YAML view would lie.
//
// The overlay only fills in fields when the persisted draft has none — once
// the clinic forks and writes real fields, we render those verbatim.
func (s *Service) overlayTemplateFields(ctx context.Context, form *FormRecord, draft *FormVersionResponse) {
	if s.templates == nil || form == nil || draft == nil {
		return
	}
	if form.SalviaTemplateID == nil || *form.SalviaTemplateID == "" {
		return
	}
	if form.SalviaTemplateState == nil || *form.SalviaTemplateState != "default" {
		return
	}
	if len(draft.Fields) > 0 {
		return
	}
	tfs, ok := s.templates.FieldsForTemplate(ctx, *form.SalviaTemplateID, form.ClinicID)
	if !ok || len(tfs) == 0 {
		return
	}
	overlaid := make([]*FieldResponse, 0, len(tfs))
	for i, tf := range tfs {
		cfg := json.RawMessage(`{}`)
		if len(tf.Config) > 0 {
			if buf, err := json.Marshal(tf.Config); err == nil {
				cfg = buf
			}
		}
		var prompt *string
		if tf.HelpText != "" {
			h := tf.HelpText
			prompt = &h
		}
		overlaid = append(overlaid, &FieldResponse{
			// Synthetic ID: deterministic per (template_id, key) so the FE
			// can use it as a React-style key without colliding across forms.
			ID:             fmt.Sprintf("tpl:%s:%s", *form.SalviaTemplateID, tf.Key),
			Position:       i,
			Title:          tf.Label,
			Type:           tf.Type,
			Config:         cfg,
			AIPrompt:       prompt,
			Required:       tf.Required,
			AllowInference: tf.AIExtract,
		})
	}
	draft.Fields = overlaid
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
	if f.RetiredBy != nil {
		s := f.RetiredBy.String()
		r.RetiredBy = &s
	}
	if f.SourceMarketplaceListingID != nil {
		s := f.SourceMarketplaceListingID.String()
		r.SourceMarketplaceListingID = &s
	}
	if f.SourceMarketplaceVersionID != nil {
		s := f.SourceMarketplaceVersionID.String()
		r.SourceMarketplaceVersionID = &s
	}
	if f.SourceMarketplaceAcquisitionID != nil {
		s := f.SourceMarketplaceAcquisitionID.String()
		r.SourceMarketplaceAcquisitionID = &s
	}
	r.SalviaTemplateID = f.SalviaTemplateID
	r.SalviaTemplateVersion = f.SalviaTemplateVersion
	r.SalviaTemplateState = f.SalviaTemplateState
	if f.FrameworkCurrencyDate != nil {
		s := f.FrameworkCurrencyDate.Format("2006-01-02")
		r.FrameworkCurrencyDate = &s
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
		ID:            v.ID.String(),
		FormID:        v.FormID.String(),
		Status:        v.Status,
		VersionMajor:  v.VersionMajor,
		VersionMinor:  v.VersionMinor,
		ChangeType:    v.ChangeType,
		ChangeSummary: v.ChangeSummary,
		Changes:       v.Changes,
		CreatedAt:     v.CreatedAt.Format(time.RFC3339),
	}
	if v.PolicyCheckResult != nil {
		r.PolicyCheckResult = json.RawMessage(*v.PolicyCheckResult)
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
	if hdr, err := decodeSystemHeader(v.SystemHeaderConfig); err == nil {
		r.SystemHeader = hdr
	}
	if len(v.GenerationMetadata) > 0 {
		r.GenerationMetadata = v.GenerationMetadata
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
	// Surface PerDocOverrides only when it carries a non-empty
	// object (`'{}'` is the column default and not interesting to
	// the FE).
	var perDoc json.RawMessage
	if len(s.PerDocOverrides) > 0 && string(s.PerDocOverrides) != "{}" {
		perDoc = s.PerDocOverrides
	}
	return &FormStyleResponse{
		Version:         s.Version,
		LogoKey:         s.LogoKey,
		PrimaryColor:    s.PrimaryColor,
		FontFamily:      s.FontFamily,
		HeaderExtra:     s.HeaderExtra,
		FooterText:      s.FooterText,
		Config:          s.Config,
		PresetID:        s.PresetID,
		PerDocOverrides: perDoc,
		IsActive:        s.IsActive,
		UpdatedAt:       s.CreatedAt.Format(time.RFC3339),
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

// SystemHeaderFieldAllowList enumerates every patient-row attribute the
// system-header card knows how to render. Form authors pick a subset of
// these for their form's header. The set is enforced at the service layer
// (not the DB) so adding a new identifier — say a new aged-care funding
// code — is a code change, not a migration.
//
// Vertical applicability is enforced by the Flutter editor, which only
// surfaces fields that match the clinic's vertical when the picker opens.
// The backend stays vertical-agnostic and accepts any name on this list;
// renderers skip entries that resolve to empty for the linked subject.
//
//nolint:revive
var SystemHeaderFieldAllowList = map[string]struct{}{
	// Universal
	"name":       {},
	"photo":      {},
	"id":         {},
	"dob":        {},
	"age":        {},
	"sex":        {},
	"visit_date": {},
	"clinician":  {},
	// General / dental
	"medical_alerts": {},
	"medications":    {},
	"allergies":      {},
	// Vet
	"species":   {},
	"breed":     {},
	"microchip": {},
	"weight":    {},
	"desexed":   {},
	"color":     {},
	// Aged care
	"room":               {},
	"nhi_number":         {},
	"medicare_number":    {},
	"preferred_language": {},
	"funding_level":      {},
	"admission_date":     {},
}

// encodeSystemHeader validates and serialises a SystemHeaderConfig for
// persistence. Unknown field identifiers are rejected as ErrValidation so
// a misbehaving client cannot silently store identifiers the renderer can't
// resolve.
func encodeSystemHeader(h *SystemHeaderConfig) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("encodeSystemHeader: %w", domain.ErrValidation)
	}
	cleaned := &SystemHeaderConfig{Enabled: h.Enabled, Fields: make([]string, 0, len(h.Fields))}
	seen := make(map[string]struct{}, len(h.Fields))
	for _, f := range h.Fields {
		if _, ok := SystemHeaderFieldAllowList[f]; !ok {
			return nil, fmt.Errorf("encodeSystemHeader: unknown field %q: %w", f, domain.ErrValidation)
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		cleaned.Fields = append(cleaned.Fields, f)
	}
	out, err := json.Marshal(cleaned)
	if err != nil {
		return nil, fmt.Errorf("encodeSystemHeader: %w", err)
	}
	return out, nil
}

// decodeSystemHeader parses the stored JSONB into a SystemHeaderConfig. A
// missing or malformed blob returns the default config so older rows that
// somehow slipped through the migration default still render the header
// rather than disappearing.
func decodeSystemHeader(raw json.RawMessage) (*SystemHeaderConfig, error) {
	if len(raw) == 0 {
		return defaultSystemHeader(), nil
	}
	var h SystemHeaderConfig
	if err := json.Unmarshal(raw, &h); err != nil {
		return defaultSystemHeader(), fmt.Errorf("decodeSystemHeader: %w", err)
	}
	if h.Fields == nil {
		h.Fields = []string{}
	}
	return &h, nil
}

func defaultSystemHeader() *SystemHeaderConfig {
	return &SystemHeaderConfig{
		Enabled: true,
		Fields:  []string{"name", "photo", "id", "dob", "age", "sex", "visit_date"},
	}
}
