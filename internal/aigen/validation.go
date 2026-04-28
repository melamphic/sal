package aigen

import (
	"encoding/json"
	"fmt"
	"strings"

	formschema "github.com/melamphic/sal/internal/forms/schema"
	polschema "github.com/melamphic/sal/internal/policy/schema"
)

// ValidationError is one issue found by a validator. Fixable=true means the
// repair pass (repair.go) is expected to fix this error in place — the
// service may proceed if the repair pass cleared all errors.
type ValidationError struct {
	Field   string // dotted path, e.g. "fields[3].config"
	Code    string // machine-readable error code (see Code* constants)
	Message string // human-readable explanation
	Fixable bool   // can the repair pass deterministically fix this?
}

// Error implements the error interface so a slice of ValidationError can be
// joined into a single error.
func (v ValidationError) Error() string {
	return fmt.Sprintf("%s: %s — %s", v.Field, v.Code, v.Message)
}

// JoinValidationErrors combines a slice of ValidationError into a single
// error suitable for returning. Returns nil if errs is empty.
func JoinValidationErrors(errs []ValidationError) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return fmt.Errorf("aigen.validation: %d errors: %s", len(errs), strings.Join(parts, "; "))
}

// Validation error codes. Stable strings used by the repair pass to decide
// whether a given error can be fixed in place, and by observability to
// bucket failure modes.
const (
	CodeMissingName            = "missing_name"
	CodeMissingFields          = "missing_fields"
	CodeFieldCountOverBudget   = "field_count_over_budget"
	CodeUnknownFieldType       = "unknown_field_type"
	CodeInvalidFieldConfig     = "invalid_field_config"
	CodeFieldTitleEmpty        = "field_title_empty"
	CodeFieldTitleTooLong      = "field_title_too_long"
	CodeFieldAIPromptTooLong   = "field_ai_prompt_too_long"
	CodeFieldPositionInvalid   = "field_position_invalid"
	CodeFieldPositionDuplicate = "field_position_duplicate"
	CodeFieldPositionGap       = "field_position_gap"
	CodeTagEmpty               = "tag_empty"
	CodeTagTooLong             = "tag_too_long"

	CodeMissingPolicyName     = "missing_policy_name"
	CodeMissingPolicyContent  = "missing_policy_content"
	CodeMissingPolicyClauses  = "missing_policy_clauses"
	CodeContentNotArray       = "content_not_array"
	CodeBlockMissingID        = "block_missing_id"
	CodeBlockDuplicateID      = "block_duplicate_id"
	CodeClauseBlockNotInBody  = "clause_block_not_in_body"
	CodeClauseInvalidParity   = "clause_invalid_parity"
	CodeClauseTitleEmpty      = "clause_title_empty"
	CodeClauseBodyEmpty       = "clause_body_empty"
	CodeClauseDuplicateBlock  = "clause_duplicate_block_id"
	CodeClauseDuplicateTitle  = "clause_duplicate_title"
	CodeClauseTitleTooLong    = "clause_title_too_long"
	CodeClauseBodyTooLong     = "clause_body_too_long"
	CodeClauseCitationTooLong = "clause_citation_too_long"
)

// Length limits — match provider response-schema limits so the validator is
// the second line of defense, not just a different opinion.
const (
	MaxFieldTitle          = 120
	MaxFieldAIPrompt       = 500
	MaxTagLength           = 40
	MaxFieldsPerForm       = 20
	MaxTagsPerForm         = 10
	MaxClausesPerPolicy    = 20
	MaxClauseTitle         = 200
	MaxClauseBody          = 2000
	MaxClauseSourceCitation = 500
)

// ValidateForm runs all form validators against f. Returns a slice of
// validation errors; an empty slice means the form is acceptable as-is.
//
// Caller pipeline: Generate → Validate → if errors and any fixable, Repair →
// Validate → if errors, Retry once.
func ValidateForm(f *GeneratedForm, budget FieldBudget) []ValidationError {
	var errs []ValidationError
	errs = append(errs, validateFormBasics(f)...)
	errs = append(errs, validateFormFields(f, budget)...)
	errs = append(errs, validateFormTags(f)...)
	return errs
}

// ValidatePolicy runs all policy validators against p.
func ValidatePolicy(p *GeneratedPolicy) []ValidationError {
	var errs []ValidationError
	errs = append(errs, validatePolicyBasics(p)...)
	blocks := validatePolicyContent(p, &errs)
	errs = append(errs, validatePolicyClauses(p, blocks)...)
	return errs
}

// ── Form validators ──────────────────────────────────────────────────────────

func validateFormBasics(f *GeneratedForm) []ValidationError {
	var errs []ValidationError
	if strings.TrimSpace(f.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Code: CodeMissingName, Message: "form name is required"})
	}
	if len(f.Fields) == 0 {
		errs = append(errs, ValidationError{Field: "fields", Code: CodeMissingFields, Message: "at least one field is required"})
	}
	return errs
}

func validateFormFields(f *GeneratedForm, budget FieldBudget) []ValidationError {
	var errs []ValidationError

	if budget.Max > 0 && len(f.Fields) > budget.Max {
		errs = append(errs, ValidationError{
			Field:   "fields",
			Code:    CodeFieldCountOverBudget,
			Message: fmt.Sprintf("%d fields exceeds budget max %d", len(f.Fields), budget.Max),
			Fixable: true, // repair can trim trailing fields
		})
	}
	if len(f.Fields) > MaxFieldsPerForm {
		errs = append(errs, ValidationError{
			Field:   "fields",
			Code:    CodeFieldCountOverBudget,
			Message: fmt.Sprintf("%d fields exceeds hard cap %d", len(f.Fields), MaxFieldsPerForm),
			Fixable: true,
		})
	}

	positions := make(map[int]int) // position -> count
	for i, fld := range f.Fields {
		path := fmt.Sprintf("fields[%d]", i)

		if !formschema.IsValidFieldType(fld.Type) {
			errs = append(errs, ValidationError{
				Field:   path + ".type",
				Code:    CodeUnknownFieldType,
				Message: fmt.Sprintf("type %q is not in the canonical whitelist", fld.Type),
			})
			continue // skip further checks on a field with no valid type
		}
		if err := formschema.ValidateConfig(formschema.FieldType(fld.Type), fld.Config); err != nil {
			errs = append(errs, ValidationError{
				Field:   path + ".config",
				Code:    CodeInvalidFieldConfig,
				Message: err.Error(),
			})
		}
		if strings.TrimSpace(fld.Title) == "" {
			errs = append(errs, ValidationError{
				Field:   path + ".title",
				Code:    CodeFieldTitleEmpty,
				Message: "field title is required",
			})
		}
		if utf8Len(fld.Title) > MaxFieldTitle {
			errs = append(errs, ValidationError{
				Field:   path + ".title",
				Code:    CodeFieldTitleTooLong,
				Message: fmt.Sprintf("title length %d exceeds max %d", utf8Len(fld.Title), MaxFieldTitle),
				Fixable: true,
			})
		}
		if utf8Len(fld.AIPrompt) > MaxFieldAIPrompt {
			errs = append(errs, ValidationError{
				Field:   path + ".ai_prompt",
				Code:    CodeFieldAIPromptTooLong,
				Message: fmt.Sprintf("ai_prompt length %d exceeds max %d", utf8Len(fld.AIPrompt), MaxFieldAIPrompt),
				Fixable: true,
			})
		}
		if fld.Position < 1 {
			errs = append(errs, ValidationError{
				Field:   path + ".position",
				Code:    CodeFieldPositionInvalid,
				Message: fmt.Sprintf("position %d must be ≥ 1", fld.Position),
				Fixable: true,
			})
		}
		positions[fld.Position]++
	}

	// Position uniqueness + contiguity (1..N).
	for pos, count := range positions {
		if count > 1 {
			errs = append(errs, ValidationError{
				Field:   "fields",
				Code:    CodeFieldPositionDuplicate,
				Message: fmt.Sprintf("position %d appears %d times", pos, count),
				Fixable: true,
			})
		}
	}
	if len(positions) == len(f.Fields) {
		// no duplicates — check for gaps if we have n fields with positions
		for i := 1; i <= len(f.Fields); i++ {
			if _, ok := positions[i]; !ok {
				errs = append(errs, ValidationError{
					Field:   "fields",
					Code:    CodeFieldPositionGap,
					Message: fmt.Sprintf("position %d missing — positions must be 1..N contiguous", i),
					Fixable: true,
				})
				break
			}
		}
	}

	return errs
}

func validateFormTags(f *GeneratedForm) []ValidationError {
	var errs []ValidationError
	if len(f.Tags) > MaxTagsPerForm {
		// Tag overflow is fixable by trimming — emit one error.
		errs = append(errs, ValidationError{
			Field:   "tags",
			Code:    CodeTagTooLong,
			Message: fmt.Sprintf("%d tags exceeds max %d", len(f.Tags), MaxTagsPerForm),
			Fixable: true,
		})
	}
	for i, t := range f.Tags {
		if strings.TrimSpace(t) == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("tags[%d]", i),
				Code:    CodeTagEmpty,
				Message: "tag is empty",
				Fixable: true,
			})
		}
		if utf8Len(t) > MaxTagLength {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("tags[%d]", i),
				Code:    CodeTagTooLong,
				Message: fmt.Sprintf("tag length %d exceeds max %d", utf8Len(t), MaxTagLength),
				Fixable: true,
			})
		}
	}
	return errs
}

// ── Policy validators ────────────────────────────────────────────────────────

func validatePolicyBasics(p *GeneratedPolicy) []ValidationError {
	var errs []ValidationError
	if strings.TrimSpace(p.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Code: CodeMissingPolicyName, Message: "policy name is required"})
	}
	if len(p.Content) == 0 {
		errs = append(errs, ValidationError{Field: "content", Code: CodeMissingPolicyContent, Message: "policy content is required"})
	}
	if len(p.Clauses) == 0 {
		errs = append(errs, ValidationError{Field: "clauses", Code: CodeMissingPolicyClauses, Message: "at least one clause is required"})
	}
	if len(p.Clauses) > MaxClausesPerPolicy {
		errs = append(errs, ValidationError{
			Field:   "clauses",
			Code:    CodeMissingPolicyClauses,
			Message: fmt.Sprintf("%d clauses exceeds max %d", len(p.Clauses), MaxClausesPerPolicy),
			Fixable: true,
		})
	}
	return errs
}

// validatePolicyContent appends content-shape errors and returns the parsed
// blocks (or nil if content is invalid). Sharing the parse with the clause
// validator avoids parsing the JSONB twice.
func validatePolicyContent(p *GeneratedPolicy, errs *[]ValidationError) []polschema.Block {
	if len(p.Content) == 0 {
		return nil
	}
	blocks, err := polschema.ParseContent(p.Content)
	if err != nil {
		*errs = append(*errs, ValidationError{
			Field:   "content",
			Code:    classifyPolicyContentError(err),
			Message: err.Error(),
		})
		return nil
	}
	return blocks
}

func classifyPolicyContentError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "duplicate"):
		return CodeBlockDuplicateID
	case strings.Contains(msg, "missing id"):
		return CodeBlockMissingID
	default:
		return CodeContentNotArray
	}
}

func validatePolicyClauses(p *GeneratedPolicy, blocks []polschema.Block) []ValidationError {
	var errs []ValidationError

	blockIDs := polschema.CollectBlockIDs(blocks)
	seenBlock := make(map[string]int)
	seenTitle := make(map[string]int)

	for i, c := range p.Clauses {
		path := fmt.Sprintf("clauses[%d]", i)

		if !polschema.IsValidParity(c.Parity) {
			errs = append(errs, ValidationError{
				Field:   path + ".parity",
				Code:    CodeClauseInvalidParity,
				Message: fmt.Sprintf("parity %q must be one of %v", c.Parity, polschema.ParityEnumValues()),
				Fixable: true, // repair defaults to medium
			})
		}
		if strings.TrimSpace(c.Title) == "" {
			errs = append(errs, ValidationError{
				Field:   path + ".title",
				Code:    CodeClauseTitleEmpty,
				Message: "clause title is required",
			})
		}
		if utf8Len(c.Title) > MaxClauseTitle {
			errs = append(errs, ValidationError{
				Field:   path + ".title",
				Code:    CodeClauseTitleTooLong,
				Message: fmt.Sprintf("title length %d exceeds max %d", utf8Len(c.Title), MaxClauseTitle),
				Fixable: true,
			})
		}
		if strings.TrimSpace(c.Body) == "" {
			errs = append(errs, ValidationError{
				Field:   path + ".body",
				Code:    CodeClauseBodyEmpty,
				Message: "clause body is required",
			})
		}
		if utf8Len(c.Body) > MaxClauseBody {
			errs = append(errs, ValidationError{
				Field:   path + ".body",
				Code:    CodeClauseBodyTooLong,
				Message: fmt.Sprintf("body length %d exceeds max %d", utf8Len(c.Body), MaxClauseBody),
				Fixable: true,
			})
		}
		if c.SourceCitation != nil && utf8Len(*c.SourceCitation) > MaxClauseSourceCitation {
			errs = append(errs, ValidationError{
				Field:   path + ".source_citation",
				Code:    CodeClauseCitationTooLong,
				Message: fmt.Sprintf("source_citation length %d exceeds max %d", utf8Len(*c.SourceCitation), MaxClauseSourceCitation),
				Fixable: true,
			})
		}

		// Block-id checks (skip if content was malformed and blockIDs is empty).
		if blocks != nil {
			if _, ok := blockIDs[c.BlockID]; !ok {
				errs = append(errs, ValidationError{
					Field:   path + ".block_id",
					Code:    CodeClauseBlockNotInBody,
					Message: fmt.Sprintf("clause references block_id %q not found in content", c.BlockID),
					Fixable: true, // repair appends a placeholder block
				})
			}
		}
		seenBlock[c.BlockID]++
		if seenBlock[c.BlockID] > 1 {
			errs = append(errs, ValidationError{
				Field:   path + ".block_id",
				Code:    CodeClauseDuplicateBlock,
				Message: fmt.Sprintf("block_id %q is referenced by multiple clauses", c.BlockID),
				Fixable: true, // repair renames duplicates
			})
		}
		seenTitle[strings.ToLower(strings.TrimSpace(c.Title))]++
		if seenTitle[strings.ToLower(strings.TrimSpace(c.Title))] > 1 {
			errs = append(errs, ValidationError{
				Field:   path + ".title",
				Code:    CodeClauseDuplicateTitle,
				Message: fmt.Sprintf("clause title %q is duplicated", c.Title),
				Fixable: false,
			})
		}
	}
	return errs
}

// ── helpers ──────────────────────────────────────────────────────────────────

// utf8Len returns the rune count of s — a more meaningful "length" for
// UI-facing limits than byte length.
func utf8Len(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// AnyFixable reports whether any of the supplied errors are repairable.
func AnyFixable(errs []ValidationError) bool {
	for _, e := range errs {
		if e.Fixable {
			return true
		}
	}
	return false
}

// AllFixable reports whether every error is repairable.
func AllFixable(errs []ValidationError) bool {
	for _, e := range errs {
		if !e.Fixable {
			return false
		}
	}
	return true
}

// SummariseForLLM returns a compact bullet list of errors suitable for feeding
// back to the model on the retry prompt — telling it what to fix.
func SummariseForLLM(errs []ValidationError) string {
	var sb strings.Builder
	for _, e := range errs {
		sb.WriteString("- ")
		sb.WriteString(e.Field)
		sb.WriteString(": ")
		sb.WriteString(e.Message)
		sb.WriteString("\n")
	}
	return sb.String()
}

// MarshalForRetry helps build the retry prompt by re-serialising the offending
// payload alongside the error bullets. Returns "" if marshalling fails — we
// degrade gracefully rather than crash the retry path.
func MarshalForRetry(payload any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}
