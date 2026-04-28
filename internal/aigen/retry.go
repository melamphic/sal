package aigen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// generateAndValidateForm runs the full Generate → Validate → Repair → Retry
// pipeline for forms. Returns the (possibly repaired) GeneratedForm plus the
// audit trail (raw provider output, repairs applied, retries used) needed for
// observability and AIMetadata.
//
// Cancellation: at every stage the function checks ctx.Err() so callers can
// abort a long-running generation by cancelling the context. Both providers
// also forward ctx into their network calls.
func generateAndValidateForm(
	ctx context.Context,
	provider Provider,
	prompt string,
	budget FieldBudget,
) (*GeneratedForm, *PipelineResult, error) {
	res := &PipelineResult{}
	res.PromptHash = hashPrompt(prompt)

	if err := ctx.Err(); err != nil {
		return nil, res, fmt.Errorf("aigen.retry.form: %w", err)
	}

	// Layer 1+2: provider-enforced schema + JSON parse.
	raw, err := provider.GenerateJSON(ctx, prompt, SchemaForm)
	res.RawAttempts = append(res.RawAttempts, string(raw))
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.form.generate: %w", err)
	}
	form, err := parseForm(raw)
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.form.parse: %w", err)
	}

	// Layer 3-5: structural / type / cross-field validation.
	errs := ValidateForm(form, budget)
	if len(errs) == 0 {
		return form, res, nil
	}

	// Layer 6: repair pass.
	res.RepairsApplied = append(res.RepairsApplied, RepairForm(form, budget)...)
	errs = ValidateForm(form, budget)
	if len(errs) == 0 {
		return form, res, nil
	}

	// Layer 7: one retry to provider with explicit error feedback.
	if !canRetry(errs) {
		return nil, res, fmt.Errorf("aigen.retry.form.unrepairable: %w", JoinValidationErrors(errs))
	}
	if err := ctx.Err(); err != nil {
		return nil, res, fmt.Errorf("aigen.retry.form.before_retry: %w", err)
	}
	fixPrompt := buildFixupPrompt(prompt, MarshalForRetry(form), errs)
	rawRetry, err := provider.GenerateJSON(ctx, fixPrompt, SchemaForm)
	res.RawAttempts = append(res.RawAttempts, string(rawRetry))
	res.RetryCount = 1
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.form.regenerate: %w", err)
	}
	form2, err := parseForm(rawRetry)
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.form.reparse: %w", err)
	}
	res.RepairsApplied = append(res.RepairsApplied, RepairForm(form2, budget)...)
	errs2 := ValidateForm(form2, budget)
	if len(errs2) > 0 {
		return nil, res, fmt.Errorf("aigen.retry.form.still_invalid_after_retry: %w", JoinValidationErrors(errs2))
	}
	return form2, res, nil
}

// generateAndValidatePolicy mirrors generateAndValidateForm for policies.
func generateAndValidatePolicy(
	ctx context.Context,
	provider Provider,
	prompt string,
) (*GeneratedPolicy, *PipelineResult, error) {
	res := &PipelineResult{}
	res.PromptHash = hashPrompt(prompt)

	if err := ctx.Err(); err != nil {
		return nil, res, fmt.Errorf("aigen.retry.policy: %w", err)
	}

	raw, err := provider.GenerateJSON(ctx, prompt, SchemaPolicy)
	res.RawAttempts = append(res.RawAttempts, string(raw))
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.policy.generate: %w", err)
	}
	policy, err := parsePolicy(raw)
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.policy.parse: %w", err)
	}

	errs := ValidatePolicy(policy)
	if len(errs) == 0 {
		return policy, res, nil
	}

	res.RepairsApplied = append(res.RepairsApplied, RepairPolicy(policy)...)
	errs = ValidatePolicy(policy)
	if len(errs) == 0 {
		return policy, res, nil
	}

	if !canRetry(errs) {
		return nil, res, fmt.Errorf("aigen.retry.policy.unrepairable: %w", JoinValidationErrors(errs))
	}
	if err := ctx.Err(); err != nil {
		return nil, res, fmt.Errorf("aigen.retry.policy.before_retry: %w", err)
	}
	fixPrompt := buildFixupPrompt(prompt, MarshalForRetry(policy), errs)
	rawRetry, err := provider.GenerateJSON(ctx, fixPrompt, SchemaPolicy)
	res.RawAttempts = append(res.RawAttempts, string(rawRetry))
	res.RetryCount = 1
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.policy.regenerate: %w", err)
	}
	policy2, err := parsePolicy(rawRetry)
	if err != nil {
		return nil, res, fmt.Errorf("aigen.retry.policy.reparse: %w", err)
	}
	res.RepairsApplied = append(res.RepairsApplied, RepairPolicy(policy2)...)
	errs2 := ValidatePolicy(policy2)
	if len(errs2) > 0 {
		return nil, res, fmt.Errorf("aigen.retry.policy.still_invalid_after_retry: %w", JoinValidationErrors(errs2))
	}
	return policy2, res, nil
}

// PipelineResult carries the per-request audit trail of the
// generate→validate→repair→retry pipeline. Populated regardless of outcome
// so callers (observability + AIMetadata) can record what happened.
type PipelineResult struct {
	PromptHash     string           // sha256 hex of the original prompt
	RawAttempts    []string         // raw provider output per call (1 or 2 entries)
	RepairsApplied []RepairLogEntry // deterministic corrections applied
	RetryCount     int              // 0 if first attempt was good, 1 otherwise
}

// canRetry reports whether the validation failures are worth re-prompting the
// model about. We retry on fixable + recoverable errors. A few error codes
// indicate prompt-level issues the model can correct (missing required field,
// referencing non-existent block, etc.). Everything else is "garbage in,
// garbage out" — the user should just edit the failed draft.
func canRetry(errs []ValidationError) bool {
	for _, e := range errs {
		switch e.Code {
		case CodeMissingName, CodeMissingFields, CodeMissingPolicyName,
			CodeMissingPolicyContent, CodeMissingPolicyClauses,
			CodeUnknownFieldType, CodeInvalidFieldConfig,
			CodeFieldTitleEmpty, CodeClauseTitleEmpty, CodeClauseBodyEmpty,
			CodeContentNotArray, CodeBlockMissingID, CodeBlockDuplicateID,
			CodeClauseBlockNotInBody, CodeClauseInvalidParity,
			CodeClauseDuplicateTitle:
			return true
		}
	}
	return false
}

// buildFixupPrompt formats the corrective prompt sent to the model on retry.
// Includes the original ask, the offending payload, and the explicit list of
// validation errors with paths.
func buildFixupPrompt(originalPrompt, brokenPayload string, errs []ValidationError) string {
	var sb strings.Builder
	sb.WriteString(originalPrompt)
	sb.WriteString("\n\n")
	sb.WriteString("## Previous attempt — DO NOT REPEAT THESE ISSUES\n")
	sb.WriteString("Your previous response had the following problems:\n\n")
	sb.WriteString(SummariseForLLM(errs))
	sb.WriteString("\n")
	if brokenPayload != "" {
		sb.WriteString("## Previous (invalid) output\n")
		sb.WriteString("```json\n")
		sb.WriteString(brokenPayload)
		sb.WriteString("\n```\n\n")
	}
	sb.WriteString("Produce a NEW response that fixes every issue above. Conform strictly to the response schema.")
	return sb.String()
}

// parseForm unmarshals raw provider JSON into GeneratedForm.
func parseForm(raw []byte) (*GeneratedForm, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty payload")
	}
	var f GeneratedForm
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &f, nil
}

// parsePolicy unmarshals raw provider JSON into GeneratedPolicy.
func parsePolicy(raw []byte) (*GeneratedPolicy, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty payload")
	}
	var p GeneratedPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &p, nil
}

// hashPrompt returns the sha256 hex of a prompt for AIMetadata.PromptHash.
func hashPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}
