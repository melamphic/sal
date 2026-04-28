package aigen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// PolicyGenService orchestrates policy generation. Mirrors FormGenService —
// returns a validated GeneratedPolicy + AIMetadata for the caller (policy
// package handler) to persist via the existing policy.Service.
type PolicyGenService struct {
	provider Provider
	logger   *slog.Logger
}

// NewPolicyGenService constructs a PolicyGenService.
func NewPolicyGenService(p Provider, l *slog.Logger) *PolicyGenService {
	if l == nil {
		l = slog.Default()
	}
	return &PolicyGenService{provider: p, logger: l}
}

// PolicyGenRequest is the assembled context the policy handler provides.
type PolicyGenRequest struct {
	Clinic         ClinicContext
	StaffID        string
	UserAsk        string
	FewShot        []json.RawMessage // optional; service will load default if nil
	ClinicPolicies []ReferenceForm   // existing clinic policies (lightweight summary)
	Marketplace    []ReferenceForm   // marketplace examples
	Override       *string           // per-request vertical override (rare)
}

// PolicyGenResult is what the service returns to the caller.
type PolicyGenResult struct {
	Policy   *GeneratedPolicy
	Metadata AIMetadata
	Repairs  []RepairLogEntry
}

// Generate runs the full pipeline and returns the validated GeneratedPolicy
// plus AIMetadata to persist on policy_versions.generation_metadata.
func (s *PolicyGenService) Generate(ctx context.Context, req PolicyGenRequest) (*PolicyGenResult, error) {
	startedAt := time.Now()
	vertical := req.Clinic.Vertical
	if req.Override != nil && *req.Override != "" {
		vertical = *req.Override
	}
	if strings.TrimSpace(req.UserAsk) == "" {
		return nil, fmt.Errorf("aigen.policies.Generate: user description is required")
	}

	regulator := LookupRegulator(req.Clinic.Country, vertical)
	fewshot := req.FewShot
	if fewshot == nil {
		if raw := LoadFewShotPolicy(vertical, req.Clinic.Country); raw != nil {
			fewshot = []json.RawMessage{raw}
		}
	}

	prompt, err := RenderPolicyPrompt(PromptContext{
		Clinic:               req.Clinic,
		Vertical:             vertical,
		Regulator:            regulator,
		UserAsk:              req.UserAsk,
		FewShotExamples:      fewshot,
		ReferenceClinicForms: req.ClinicPolicies,
		ReferenceMarketplace: req.Marketplace,
	})
	if err != nil {
		return nil, fmt.Errorf("aigen.policies.Generate.render: %w", err)
	}

	policy, pipe, genErr := generateAndValidatePolicy(ctx, s.provider, prompt)
	logEntry := s.buildLog(req, vertical, "policy", pipe, startedAt, genErr)
	logEntry.Emit(s.logger)

	if genErr != nil {
		if errors.Is(genErr, context.Canceled) || errors.Is(genErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("aigen.policies.Generate: %w", genErr)
		}
		return nil, fmt.Errorf("aigen.policies.Generate: %w", genErr)
	}

	return &PolicyGenResult{
		Policy: policy,
		Metadata: AIMetadata{
			Source:             "ai_generated",
			Provider:           s.provider.Name(),
			Model:              s.provider.Model(),
			PromptHash:         pipe.PromptHash,
			GeneratedByStaffID: req.StaffID,
			GeneratedAt:        startedAt,
			RepairsApplied:     len(pipe.RepairsApplied),
			RetryCount:         pipe.RetryCount,
		},
		Repairs: pipe.RepairsApplied,
	}, nil
}

func (s *PolicyGenService) buildLog(req PolicyGenRequest, vertical, kind string, pipe *PipelineResult, startedAt time.Time, genErr error) GenerationLog {
	outcome := "success"
	reason := ""
	if genErr != nil {
		switch {
		case errors.Is(genErr, context.Canceled):
			outcome, reason = "cancelled", "ctx.Canceled"
		case errors.Is(genErr, context.DeadlineExceeded):
			outcome, reason = "cancelled", "ctx.DeadlineExceeded"
		case strings.Contains(genErr.Error(), "still_invalid_after_retry") || strings.Contains(genErr.Error(), "unrepairable"):
			outcome, reason = "validation_failed", classifyValidationError(genErr)
		default:
			outcome, reason = "provider_error", genErr.Error()
		}
	}
	return GenerationLog{
		ClinicID:         req.Clinic.ClinicID,
		StaffID:          req.StaffID,
		Vertical:         vertical,
		Country:          req.Clinic.Country,
		Kind:             kind,
		Provider:         s.provider.Name(),
		Model:            s.provider.Model(),
		PromptHash:       pipe.PromptHash,
		LatencyMS:        time.Since(startedAt).Milliseconds(),
		Repairs:          repairActions(pipe.RepairsApplied),
		RetryCount:       pipe.RetryCount,
		Outcome:          outcome,
		OutcomeReason:    reason,
		StartedAt:        startedAt,
		Duration:         time.Since(startedAt),
	}
}
