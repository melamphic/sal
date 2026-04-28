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

// FormGenService orchestrates form generation: assemble prompt context, run
// the schema-safe generation pipeline, return a validated GeneratedForm plus
// the metadata needed for AIMetadata persistence and audit logging.
//
// The service does NOT touch the forms repository directly — it returns the
// payload for the caller (forms package handler) to persist via the existing
// forms.Service. This keeps the cross-domain boundary clean (per CLAUDE.md:
// "call exported service interfaces only — never import another domain's
// types or query another domain's tables").
type FormGenService struct {
	provider Provider
	logger   *slog.Logger
}

// NewFormGenService constructs a FormGenService.
func NewFormGenService(p Provider, l *slog.Logger) *FormGenService {
	if l == nil {
		l = slog.Default()
	}
	return &FormGenService{provider: p, logger: l}
}

// FormGenRequest is the assembled context the caller (forms handler) provides
// to the service. The handler is responsible for reading clinic state and
// gathering reference forms before calling here — aigen does not query other
// domains.
type FormGenRequest struct {
	Clinic      ClinicContext
	StaffID     string
	UserAsk     string
	Budget      FieldBudget
	FewShot     []json.RawMessage // optional; service will load default if nil
	ClinicForms []ReferenceForm   // existing clinic forms (style match)
	Marketplace []ReferenceForm   // marketplace examples for the vertical
	Override    *string           // per-request vertical override (rare)
}

// FormGenResult is what the service returns to the caller.
type FormGenResult struct {
	Form     *GeneratedForm
	Metadata AIMetadata
	Repairs  []RepairLogEntry // detail for the "auto-corrected" UI banner
}

// Generate runs the full pipeline and returns the validated GeneratedForm
// plus AIMetadata to persist on form_versions.generation_metadata.
//
// On user cancellation the function returns ctx.Err() wrapped, so callers
// can detect cancellation with errors.Is(err, context.Canceled).
func (s *FormGenService) Generate(ctx context.Context, req FormGenRequest) (*FormGenResult, error) {
	startedAt := time.Now()
	vertical := req.Clinic.Vertical
	if req.Override != nil && *req.Override != "" {
		vertical = *req.Override
	}
	if strings.TrimSpace(req.UserAsk) == "" {
		return nil, fmt.Errorf("aigen.forms.Generate: user description is required")
	}

	regulator := LookupRegulator(req.Clinic.Country, vertical)
	fewshot := req.FewShot
	if fewshot == nil {
		if raw := LoadFewShotForm(vertical); raw != nil {
			fewshot = []json.RawMessage{raw}
		}
	}
	budget := req.Budget
	if budget.Max <= 0 {
		budget = DefaultFieldBudget(req.Clinic.Tier)
	}

	prompt, err := RenderFormPrompt(PromptContext{
		Clinic:               req.Clinic,
		Vertical:             vertical,
		Regulator:            regulator,
		UserAsk:              req.UserAsk,
		FewShotExamples:      fewshot,
		ReferenceClinicForms: req.ClinicForms,
		ReferenceMarketplace: req.Marketplace,
		FieldBudget:          budget,
	})
	if err != nil {
		return nil, fmt.Errorf("aigen.forms.Generate.render: %w", err)
	}

	form, pipe, genErr := generateAndValidateForm(ctx, s.provider, prompt, budget)
	logEntry := s.buildLog(req, vertical, "form", pipe, startedAt, genErr)
	logEntry.Emit(s.logger)

	if genErr != nil {
		// Preserve cancellation semantics for the caller.
		if errors.Is(genErr, context.Canceled) || errors.Is(genErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("aigen.forms.Generate: %w", genErr)
		}
		return nil, fmt.Errorf("aigen.forms.Generate: %w", genErr)
	}

	return &FormGenResult{
		Form: form,
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

// buildLog constructs the structured log record for a single generation pass.
func (s *FormGenService) buildLog(req FormGenRequest, vertical, kind string, pipe *PipelineResult, startedAt time.Time, genErr error) GenerationLog {
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

// classifyValidationError extracts the leading error code(s) from a joined
// validation error message so observability can bucket failure modes without
// logging full content.
func classifyValidationError(err error) string {
	msg := err.Error()
	// JoinValidationErrors uses ": " between path and code.
	for _, code := range []string{
		CodeMissingName, CodeMissingFields, CodeUnknownFieldType, CodeInvalidFieldConfig,
		CodeFieldTitleEmpty, CodeFieldPositionInvalid, CodeFieldPositionDuplicate,
		CodeMissingPolicyName, CodeMissingPolicyContent, CodeMissingPolicyClauses,
		CodeContentNotArray, CodeBlockMissingID, CodeClauseInvalidParity,
		CodeClauseBlockNotInBody, CodeClauseDuplicateTitle,
	} {
		if strings.Contains(msg, code) {
			return code
		}
	}
	return "validation_failed"
}
