package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/aigen"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// ── Service: persist AI-generated form ───────────────────────────────────────

// CreateFromAIGenInput holds the validated AI-generation payload + provenance
// metadata that Service.CreateFromAIGen persists as a draft.
type CreateFromAIGenInput struct {
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	GroupID  *uuid.UUID
	Form     *aigen.GeneratedForm
	Metadata aigen.AIMetadata
}

// RegenerateAIDraftInput holds inputs for replacing an existing form's draft
// with AI-generated content. Used by the in-editor "Generate fields" panel
// — the user already created an empty form (or a form with a stale draft)
// and now wants the AI to populate its fields.
type RegenerateAIDraftInput struct {
	FormID   uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Form     *aigen.GeneratedForm
	Metadata aigen.AIMetadata
}

// CreateFromAIGen creates a new form, populates its draft with the
// AI-generated fields, and stores the AI provenance JSONB on the draft
// version. Returns the FormResponse the caller can return directly to the
// client so the editor opens at the new draft.
//
// This composes existing CreateForm + UpdateDraft so the AI path goes through
// the same validation, draft-state machine, and audit hooks as a manual
// create. The only difference is the trailing SaveGenerationMetadata call
// that marks the draft as AI-authored.
func (s *Service) CreateFromAIGen(ctx context.Context, input CreateFromAIGenInput) (*FormResponse, error) {
	if input.Form == nil {
		return nil, fmt.Errorf("forms.service.CreateFromAIGen: form is required")
	}

	// 1. Create the form + empty draft via the existing path.
	desc := optionalString(input.Form.Description)
	overall := optionalString(input.Form.OverallPrompt)
	created, err := s.CreateForm(ctx, CreateFormInput{
		ClinicID:      input.ClinicID,
		StaffID:       input.StaffID,
		GroupID:       input.GroupID,
		Name:          input.Form.Name,
		Description:   desc,
		OverallPrompt: overall,
		Tags:          input.Form.Tags,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateFromAIGen: create: %w", err)
	}

	formID, err := uuid.Parse(created.ID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateFromAIGen: parse new form id: %w", err)
	}

	// 2. Populate the draft fields via the existing UpdateDraft path.
	fieldInputs := make([]FieldInput, len(input.Form.Fields))
	for i, f := range input.Form.Fields {
		fi := FieldInput{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         f.Config,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
		}
		if f.AIPrompt != "" {
			p := f.AIPrompt
			fi.AIPrompt = &p
		}
		fieldInputs[i] = fi
	}
	resp, err := s.UpdateDraft(ctx, UpdateDraftInput{
		FormID:        formID,
		ClinicID:      input.ClinicID,
		StaffID:       input.StaffID,
		GroupID:       input.GroupID,
		Name:          input.Form.Name,
		Description:   desc,
		OverallPrompt: overall,
		Tags:          input.Form.Tags,
		Fields:        fieldInputs,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.CreateFromAIGen: populate draft: %w", err)
	}

	// 3. Mark the draft as AI-authored. Best-effort: a failure here means the
	// form is created and usable; we just don't badge it. Log loud and move on.
	if resp.Draft != nil && resp.Draft.ID != "" {
		versionID, parseErr := uuid.Parse(resp.Draft.ID)
		if parseErr == nil {
			meta, marshalErr := json.Marshal(input.Metadata)
			if marshalErr == nil {
				if saveErr := s.repo.SaveGenerationMetadata(ctx, versionID, input.ClinicID, meta); saveErr != nil && !errors.Is(saveErr, domain.ErrNotFound) {
					return nil, fmt.Errorf("forms.service.CreateFromAIGen: save metadata: %w", saveErr)
				}
			}
		}
	}

	return resp, nil
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// RegenerateAIDraft replaces an existing form's draft with AI-generated
// content. The form must already exist and be owned by the caller's clinic.
//
// Field replacement is total — the AI's fields, overall_prompt, and tags
// fully overwrite the draft. Form metadata follows a "respect-user-input"
// rule: existing non-default name and description are preserved (so a user
// who renamed the form to "SOAP Note - Surgery" keeps their name even if the
// AI suggests something generic), and only an empty / placeholder name like
// "Untitled form" is overwritten with the AI's suggestion.
func (s *Service) RegenerateAIDraft(ctx context.Context, input RegenerateAIDraftInput) (*FormResponse, error) {
	if input.Form == nil {
		return nil, fmt.Errorf("forms.service.RegenerateAIDraft: form is required")
	}

	form, err := s.repo.GetFormByID(ctx, input.FormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("forms.service.RegenerateAIDraft: %w", err)
	}
	if form.ArchivedAt != nil {
		return nil, fmt.Errorf("forms.service.RegenerateAIDraft: form is retired: %w", domain.ErrConflict)
	}

	// Decide whether to overwrite the form name. We treat empty and the
	// system placeholder as "no real name yet"; anything else is a user
	// choice and we preserve it.
	name := form.Name
	if isPlaceholderFormName(name) {
		name = input.Form.Name
	}
	if name == "" {
		name = "Untitled form"
	}

	// Same logic for description: preserve user-set description; only fill
	// in the AI's when the field is currently empty.
	var description *string
	if form.Description != nil && *form.Description != "" {
		description = form.Description
	} else {
		description = optionalString(input.Form.Description)
	}

	// Build the field input list from the AI payload — same shape as
	// CreateFromAIGen.
	fieldInputs := make([]FieldInput, len(input.Form.Fields))
	for i, f := range input.Form.Fields {
		fi := FieldInput{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         f.Config,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
		}
		if f.AIPrompt != "" {
			p := f.AIPrompt
			fi.AIPrompt = &p
		}
		fieldInputs[i] = fi
	}

	resp, err := s.UpdateDraft(ctx, UpdateDraftInput{
		FormID:        input.FormID,
		ClinicID:      input.ClinicID,
		StaffID:       input.StaffID,
		GroupID:       form.GroupID,
		Name:          name,
		Description:   description,
		OverallPrompt: optionalString(input.Form.OverallPrompt),
		Tags:          input.Form.Tags,
		Fields:        fieldInputs,
	})
	if err != nil {
		return nil, fmt.Errorf("forms.service.RegenerateAIDraft: update draft: %w", err)
	}

	if resp.Draft != nil && resp.Draft.ID != "" {
		versionID, parseErr := uuid.Parse(resp.Draft.ID)
		if parseErr == nil {
			meta, marshalErr := json.Marshal(input.Metadata)
			if marshalErr == nil {
				if saveErr := s.repo.SaveGenerationMetadata(ctx, versionID, input.ClinicID, meta); saveErr != nil && !errors.Is(saveErr, domain.ErrNotFound) {
					return nil, fmt.Errorf("forms.service.RegenerateAIDraft: save metadata: %w", saveErr)
				}
			}
		}
	}
	return resp, nil
}

// isPlaceholderFormName reports whether the supplied name is the system
// "no real name yet" sentinel — at the moment, blank or "Untitled form".
// Kept inline so the placeholder list lives next to the regenerate logic
// that uses it.
func isPlaceholderFormName(s string) bool {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "", "untitled form", "untitled":
		return true
	}
	return false
}

// ── HTTP handler: POST /api/v1/forms/generate ────────────────────────────────

// AIGenClinicLookup resolves the clinic-context fields the aigen prompt
// builder needs. App wiring provides an adapter that calls the clinic service.
type AIGenClinicLookup interface {
	GetForAIGen(ctx context.Context, clinicID uuid.UUID) (vertical, country, tier string, err error)
}

// AIGenHandler exposes the form-generation endpoint. Kept as a separate type
// so the existing Handler signature does not change — the caller mounts both
// in the same router.
type AIGenHandler struct {
	svc       *Service
	formGen   *aigen.FormGenService
	clinics   AIGenClinicLookup
	rateLimit *mw.RateLimiterStore
}

// NewAIGenHandler constructs a form AIGenHandler. Pass a nil formGen to
// signal the feature is disabled (the route returns 503 in that case).
// The optional rateLimit (per-IP) gates the /generate endpoint to stop
// abusive callers from running the model loop hot.
func NewAIGenHandler(svc *Service, formGen *aigen.FormGenService, clinics AIGenClinicLookup, rateLimit *mw.RateLimiterStore) *AIGenHandler {
	return &AIGenHandler{svc: svc, formGen: formGen, clinics: clinics, rateLimit: rateLimit}
}

type generateFormBodyInput struct {
	Body struct {
		Description    string  `json:"description" minLength:"10" maxLength:"600" doc:"Free-text description of the form to generate (the protocol it captures, fields you want, etc.). Capped at 600 characters to keep prompts compact and predictable; longer requests should be split or simplified."`
		GroupID        *string `json:"group_id,omitempty" doc:"Optional folder UUID to place the new form in."`
		UseMarketplace bool    `json:"use_marketplace,omitempty" doc:"Include marketplace examples for the clinic's vertical as additional few-shot context. Defaults true if omitted."`
		Vertical       *string `json:"vertical,omitempty" doc:"Per-form vertical override (rare — mixed-practice clinics). Defaults to clinic.vertical."`
	}
}

// Mount registers the form-generation route on the router. Permission:
// manage_forms. If the feature is not configured (no AI provider key), the
// route is NOT registered — clients will see a 404 rather than a 503, which
// keeps the OpenAPI surface honest about what's actually callable.
func (h *AIGenHandler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	if h == nil || h.formGen == nil {
		return
	}
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageForms := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageForms })
	security := []map[string][]string{{"bearerAuth": {}}}

	mws := huma.Middlewares{auth, manageForms}
	if h.rateLimit != nil {
		mws = append(huma.Middlewares{mw.RateLimitHuma(api, h.rateLimit)}, mws...)
	}

	huma.Register(api, huma.Operation{
		OperationID: "generate-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/generate",
		Summary:     "AI-draft a form from a description",
		Description: "Generates a draft form from a free-text description plus the clinic's vertical / country / tier. The output goes through 8-layer schema-safety validation, lands as a draft (NOT published), and is marked with provenance metadata so the editor renders an 'AI drafted' badge.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: mws,
		DefaultStatus: http.StatusCreated,
	}, h.generateForm)

	huma.Register(api, huma.Operation{
		OperationID: "regenerate-form",
		Method:      http.MethodPost,
		Path:        "/api/v1/forms/{form_id}/regenerate",
		Summary:     "Replace an existing form's draft with AI-generated fields",
		Description: "Used by the in-editor 'Generate fields' panel: the user already created a (typically empty) form and now wants the AI to populate fields. Replaces the draft's fields, overall_prompt, and tags. Form name and description are preserved if the user has set them; only the placeholder 'Untitled form' is overwritten with the AI's suggested name.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: mws,
	}, h.regenerateForm)
}

func (h *AIGenHandler) generateForm(ctx context.Context, input *generateFormBodyInput) (*formHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	vertical, country, tier, err := h.clinics.GetForAIGen(ctx, clinicID)
	if err != nil {
		return nil, mapFormError(fmt.Errorf("forms.handler.generateForm.clinic_lookup: %w", err))
	}

	var groupID *uuid.UUID
	if input.Body.GroupID != nil {
		id, err := uuid.Parse(*input.Body.GroupID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid group_id")
		}
		groupID = &id
	}

	req := aigen.FormGenRequest{
		Clinic: aigen.ClinicContext{
			ClinicID: clinicID.String(),
			Vertical: vertical,
			Country:  country,
			Tier:     tier,
		},
		StaffID:  staffID.String(),
		UserAsk:  input.Body.Description,
		Override: input.Body.Vertical,
	}
	res, err := h.formGen.Generate(ctx, req)
	if err != nil {
		return nil, mapAIGenError(err)
	}

	formResp, err := h.svc.CreateFromAIGen(ctx, CreateFromAIGenInput{
		ClinicID: clinicID,
		StaffID:  staffID,
		GroupID:  groupID,
		Form:     res.Form,
		Metadata: res.Metadata,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: formResp}, nil
}

// mapAIGenError translates a generation-pipeline error into a Huma HTTP
// error with a useful status + user-facing message. Specifically:
//
//   - context.Canceled            → 408 Request Timeout
//   - context.DeadlineExceeded    → 504 Gateway Timeout
//   - upstream RESOURCE_EXHAUSTED → 429 Too Many Requests with copy that
//     explains the user has hit a provider quota (Gemini free tier is the
//     usual culprit; the message tells them to retry later or switch
//     provider)
//   - everything else             → 502 Bad Gateway with the raw cause
func mapAIGenError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return huma.NewError(http.StatusRequestTimeout, "request cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return huma.NewError(http.StatusGatewayTimeout, "generation timed out")
	}
	msg := err.Error()
	if strings.Contains(msg, "RESOURCE_EXHAUSTED") ||
		strings.Contains(msg, "exceeded your current quota") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "Error 429") {
		return huma.NewError(
			http.StatusTooManyRequests,
			"AI provider quota exhausted. Wait for the quota to reset (free Gemini = 20 requests/day) or switch provider — set AIGEN_PROVIDER=openai with an OPENAI_API_KEY.",
		)
	}
	return huma.Error502BadGateway(fmt.Sprintf("aigen: %v", err))
}

// regenerateFormBodyInput is the body shape for the in-editor "Generate
// fields" panel. The form_id comes from the URL path.
type regenerateFormBodyInput struct {
	FormID string `path:"form_id"`
	Body   struct {
		Description string  `json:"description" minLength:"10" maxLength:"600" doc:"Free-text description of the form to generate."`
		Vertical    *string `json:"vertical,omitempty" doc:"Per-form vertical override (rare). Defaults to clinic.vertical."`
	}
}

func (h *AIGenHandler) regenerateForm(ctx context.Context, input *regenerateFormBodyInput) (*formHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	formID, err := uuid.Parse(input.FormID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_id")
	}

	vertical, country, tier, err := h.clinics.GetForAIGen(ctx, clinicID)
	if err != nil {
		return nil, mapFormError(fmt.Errorf("forms.handler.regenerateForm.clinic_lookup: %w", err))
	}

	req := aigen.FormGenRequest{
		Clinic: aigen.ClinicContext{
			ClinicID: clinicID.String(),
			Vertical: vertical,
			Country:  country,
			Tier:     tier,
		},
		StaffID:  staffID.String(),
		UserAsk:  input.Body.Description,
		Override: input.Body.Vertical,
	}
	res, err := h.formGen.Generate(ctx, req)
	if err != nil {
		return nil, mapAIGenError(err)
	}

	formResp, err := h.svc.RegenerateAIDraft(ctx, RegenerateAIDraftInput{
		FormID:   formID,
		ClinicID: clinicID,
		StaffID:  staffID,
		Form:     res.Form,
		Metadata: res.Metadata,
	})
	if err != nil {
		return nil, mapFormError(err)
	}
	return &formHTTPResponse{Body: formResp}, nil
}
