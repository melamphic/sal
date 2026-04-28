package policy

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

// ── Service: persist AI-generated policy ─────────────────────────────────────

// CreateFromAIGenInput holds the validated AI-generation payload + provenance
// metadata that Service.CreateFromAIGen persists as a draft.
type CreateFromAIGenInput struct {
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	FolderID *uuid.UUID
	Policy   *aigen.GeneratedPolicy
	Metadata aigen.AIMetadata
}

// RegenerateAIDraftInput holds inputs for replacing an existing policy's
// draft (content + clauses) with AI-generated content. Used by the
// in-editor "Generate clauses" panel.
type RegenerateAIDraftInput struct {
	PolicyID uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Policy   *aigen.GeneratedPolicy
	Metadata aigen.AIMetadata
}

// CreateFromAIGen creates a new policy, populates its draft with the
// AI-generated content + clauses, and stores the AI provenance JSONB on the
// draft version. Returns the PolicyResponse the caller can return directly so
// the editor opens at the new draft.
func (s *Service) CreateFromAIGen(ctx context.Context, input CreateFromAIGenInput) (*PolicyResponse, error) {
	if input.Policy == nil {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: policy is required")
	}

	// 1. Create the policy + empty draft.
	desc := optionalString(input.Policy.Description)
	created, err := s.CreatePolicy(ctx, CreatePolicyInput{
		ClinicID:    input.ClinicID,
		StaffID:     input.StaffID,
		FolderID:    input.FolderID,
		Name:        input.Policy.Name,
		Description: desc,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: create: %w", err)
	}
	policyID, err := uuid.Parse(created.ID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: parse new policy id: %w", err)
	}

	// 2. Populate draft content.
	resp, err := s.UpdateDraft(ctx, UpdateDraftInput{
		PolicyID:    policyID,
		ClinicID:    input.ClinicID,
		StaffID:     input.StaffID,
		FolderID:    input.FolderID,
		Name:        input.Policy.Name,
		Description: desc,
		Content:     input.Policy.Content,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: populate draft: %w", err)
	}

	// 3. Upsert clauses on the draft version.
	if resp.Draft == nil || resp.Draft.ID == "" {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: draft missing after populate")
	}
	versionID, err := uuid.Parse(resp.Draft.ID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: parse draft id: %w", err)
	}

	clauseInputs := make([]ClauseItemInput, len(input.Policy.Clauses))
	for i, c := range input.Policy.Clauses {
		clauseInputs[i] = ClauseItemInput{
			BlockID:        c.BlockID,
			Title:          c.Title,
			Body:           c.Body,
			Parity:         c.Parity,
			SourceCitation: c.SourceCitation,
		}
	}
	if _, err := s.UpsertClauses(ctx, UpsertClausesInput{
		PolicyID:  policyID,
		ClinicID:  input.ClinicID,
		VersionID: versionID,
		Clauses:   clauseInputs,
	}); err != nil {
		return nil, fmt.Errorf("policy.service.CreateFromAIGen: clauses: %w", err)
	}

	// 4. Mark the draft as AI-authored. Best-effort.
	meta, marshalErr := json.Marshal(input.Metadata)
	if marshalErr == nil {
		if saveErr := s.repo.SaveGenerationMetadata(ctx, versionID, input.ClinicID, meta); saveErr != nil && !errors.Is(saveErr, domain.ErrNotFound) {
			return nil, fmt.Errorf("policy.service.CreateFromAIGen: save metadata: %w", saveErr)
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

// RegenerateAIDraft replaces an existing policy's draft with AI-generated
// content + clauses. Same "respect-user-input" rule as forms — existing
// non-default name / description preserved.
func (s *Service) RegenerateAIDraft(ctx context.Context, input RegenerateAIDraftInput) (*PolicyResponse, error) {
	if input.Policy == nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: policy is required")
	}

	pol, err := s.repo.GetPolicyByID(ctx, input.PolicyID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: %w", err)
	}
	if pol.ArchivedAt != nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: policy is retired: %w", domain.ErrConflict)
	}

	name := pol.Name
	if isPlaceholderPolicyName(name) {
		name = input.Policy.Name
	}
	if name == "" {
		name = "Untitled policy"
	}

	var description *string
	if pol.Description != nil && *pol.Description != "" {
		description = pol.Description
	} else {
		description = optionalString(input.Policy.Description)
	}

	resp, err := s.UpdateDraft(ctx, UpdateDraftInput{
		PolicyID:    input.PolicyID,
		ClinicID:    input.ClinicID,
		StaffID:     input.StaffID,
		FolderID:    pol.FolderID,
		Name:        name,
		Description: description,
		Content:     input.Policy.Content,
	})
	if err != nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: update draft: %w", err)
	}

	if resp.Draft == nil || resp.Draft.ID == "" {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: draft missing after update")
	}
	versionID, err := uuid.Parse(resp.Draft.ID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: parse draft id: %w", err)
	}

	clauseInputs := make([]ClauseItemInput, len(input.Policy.Clauses))
	for i, c := range input.Policy.Clauses {
		clauseInputs[i] = ClauseItemInput{
			BlockID:        c.BlockID,
			Title:          c.Title,
			Body:           c.Body,
			Parity:         c.Parity,
			SourceCitation: c.SourceCitation,
		}
	}
	if _, err := s.UpsertClauses(ctx, UpsertClausesInput{
		PolicyID:  input.PolicyID,
		ClinicID:  input.ClinicID,
		VersionID: versionID,
		Clauses:   clauseInputs,
	}); err != nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: clauses: %w", err)
	}

	meta, marshalErr := json.Marshal(input.Metadata)
	if marshalErr == nil {
		if saveErr := s.repo.SaveGenerationMetadata(ctx, versionID, input.ClinicID, meta); saveErr != nil && !errors.Is(saveErr, domain.ErrNotFound) {
			return nil, fmt.Errorf("policy.service.RegenerateAIDraft: save metadata: %w", saveErr)
		}
	}

	// Re-fetch so the returned response reflects the upserted clauses
	// (UpdateDraft doesn't know about clauses).
	final, err := s.GetPolicy(ctx, input.PolicyID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("policy.service.RegenerateAIDraft: refetch: %w", err)
	}
	return final, nil
}

// mapAIGenError mirrors the forms package helper. See forms/aigen.go for
// the rationale; kept duplicated rather than shared so the policy package
// has no cross-domain import.
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

func isPlaceholderPolicyName(s string) bool {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "", "untitled policy", "untitled":
		return true
	}
	return false
}

// ── HTTP handler: POST /api/v1/policies/generate ─────────────────────────────

// AIGenClinicLookup mirrors forms.AIGenClinicLookup. Defined here to avoid
// cross-domain imports.
type AIGenClinicLookup interface {
	GetForAIGen(ctx context.Context, clinicID uuid.UUID) (vertical, country, tier string, err error)
}

// AIGenHandler exposes the policy-generation endpoint.
type AIGenHandler struct {
	svc       *Service
	policyGen *aigen.PolicyGenService
	clinics   AIGenClinicLookup
	rateLimit *mw.RateLimiterStore
}

// NewAIGenHandler constructs a policy AIGenHandler. Pass a nil policyGen to
// disable the route entirely. The optional rateLimit (per-IP) gates the
// /generate endpoint.
func NewAIGenHandler(svc *Service, policyGen *aigen.PolicyGenService, clinics AIGenClinicLookup, rateLimit *mw.RateLimiterStore) *AIGenHandler {
	return &AIGenHandler{svc: svc, policyGen: policyGen, clinics: clinics, rateLimit: rateLimit}
}

type generatePolicyBodyInput struct {
	Body struct {
		Description    string  `json:"description" minLength:"10" maxLength:"600" doc:"Free-text description of the policy to generate (compliance area, clauses, references). Capped at 600 characters to keep prompts compact."`
		FolderID       *string `json:"folder_id,omitempty"`
		UseMarketplace bool    `json:"use_marketplace,omitempty"`
		Vertical       *string `json:"vertical,omitempty" doc:"Per-policy vertical override (rare). Defaults to clinic.vertical."`
	}
}

// Mount registers the policy-generation route. Requires manage_policies. If
// the feature is not configured, the route is omitted.
func (h *AIGenHandler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	if h == nil || h.policyGen == nil {
		return
	}
	auth := mw.AuthenticateHuma(api, jwtSecret)
	managePolicies := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManagePolicies })
	security := []map[string][]string{{"bearerAuth": {}}}

	mws := huma.Middlewares{auth, managePolicies}
	if h.rateLimit != nil {
		mws = append(huma.Middlewares{mw.RateLimitHuma(api, h.rateLimit)}, mws...)
	}

	huma.Register(api, huma.Operation{
		OperationID:   "generate-policy",
		Method:        http.MethodPost,
		Path:          "/api/v1/policies/generate",
		Summary:       "AI-draft a policy from a description",
		Description:   "Generates a draft policy (content + clauses) from a free-text description plus the clinic's vertical / country. Lands as a draft (NOT published), provenance metadata stored. Source citations are AI-suggested only — UI marks them 'verify against [regulator]'.",
		Tags:          []string{"Policy"},
		Security:      security,
		Middlewares:   mws,
		DefaultStatus: http.StatusCreated,
	}, h.generatePolicy)

	huma.Register(api, huma.Operation{
		OperationID: "regenerate-policy",
		Method:      http.MethodPost,
		Path:        "/api/v1/policies/{policy_id}/regenerate",
		Summary:     "Replace an existing policy's draft with AI-generated content",
		Description: "Used by the in-editor 'Generate clauses' panel. Replaces the draft's content + clauses; preserves user-set name/description.",
		Tags:        []string{"Policy"},
		Security:    security,
		Middlewares: mws,
	}, h.regeneratePolicy)
}

func (h *AIGenHandler) generatePolicy(ctx context.Context, input *generatePolicyBodyInput) (*policyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	vertical, country, tier, err := h.clinics.GetForAIGen(ctx, clinicID)
	if err != nil {
		return nil, mapPolicyError(fmt.Errorf("policy.handler.generatePolicy.clinic_lookup: %w", err))
	}

	var folderID *uuid.UUID
	if input.Body.FolderID != nil {
		id, err := uuid.Parse(*input.Body.FolderID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid folder_id")
		}
		folderID = &id
	}

	req := aigen.PolicyGenRequest{
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
	res, err := h.policyGen.Generate(ctx, req)
	if err != nil {
		return nil, mapAIGenError(err)
	}

	policyResp, err := h.svc.CreateFromAIGen(ctx, CreateFromAIGenInput{
		ClinicID: clinicID,
		StaffID:  staffID,
		FolderID: folderID,
		Policy:   res.Policy,
		Metadata: res.Metadata,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyHTTPResponse{Body: policyResp}, nil
}

type regeneratePolicyBodyInput struct {
	PolicyID string `path:"policy_id"`
	Body     struct {
		Description string  `json:"description" minLength:"10" maxLength:"600" doc:"Free-text description of the policy to generate."`
		Vertical    *string `json:"vertical,omitempty" doc:"Per-policy vertical override (rare). Defaults to clinic.vertical."`
	}
}

func (h *AIGenHandler) regeneratePolicy(ctx context.Context, input *regeneratePolicyBodyInput) (*policyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	policyID, err := uuid.Parse(input.PolicyID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid policy_id")
	}

	vertical, country, tier, err := h.clinics.GetForAIGen(ctx, clinicID)
	if err != nil {
		return nil, mapPolicyError(fmt.Errorf("policy.handler.regeneratePolicy.clinic_lookup: %w", err))
	}

	req := aigen.PolicyGenRequest{
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
	res, err := h.policyGen.Generate(ctx, req)
	if err != nil {
		return nil, mapAIGenError(err)
	}

	policyResp, err := h.svc.RegenerateAIDraft(ctx, RegenerateAIDraftInput{
		PolicyID: policyID,
		ClinicID: clinicID,
		StaffID:  staffID,
		Policy:   res.Policy,
		Metadata: res.Metadata,
	})
	if err != nil {
		return nil, mapPolicyError(err)
	}
	return &policyHTTPResponse{Body: policyResp}, nil
}
