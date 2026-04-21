// Package extraction provides AI-powered form field extraction from audio transcripts.
// It defines the Extractor interface and concrete implementations for Gemini and OpenAI.
// The concrete implementation is chosen at startup based on EXTRACTION_PROVIDER config.
package extraction

import "context"

// FieldSpec describes a single form field to extract.
type FieldSpec struct {
	ID             string // UUID of the form_fields row
	Title          string // display name — included in prompt
	Type           string // widget type (text, slider, select, …) — helps AI understand expected format
	AIPrompt       string // per-field extraction hint from the form designer
	Required       bool   // AI must provide a value; absence flagged as low confidence
	Skippable      bool   // excluded from AI extraction; leave value nil
	AllowInference bool   // when false, only direct (verbatim) quotes accepted; hint to AI
}

// FieldResult holds the AI output for a single field.
type FieldResult struct {
	FieldID            string   // matches FieldSpec.ID
	Value              *string  // JSON-encoded value; nil = not found / skipped
	Confidence         *float64 // 0.0–1.0; nil when skippable or no value found
	SourceQuote        *string  // supporting snippet from transcript; nil when not applicable
	TransformationType *string  // "direct" | "inference"; nil when value is nil
}

// Extractor extracts structured form field values from a clinical transcript.
type Extractor interface {
	// Extract takes the full transcript text, an overall context prompt for the
	// form, and a slice of field specs. It returns one FieldResult per spec.
	// The caller must pass exactly the non-skippable fields; skippable fields
	// should be excluded from specs before calling.
	Extract(ctx context.Context, transcript, overallPrompt string, fields []FieldSpec) ([]FieldResult, error)
}

// PolicyClause describes a single enforceable policy clause for alignment checking.
type PolicyClause struct {
	BlockID string
	Title   string
	Parity  string // "high" | "medium" | "low"
}

// PolicyAligner scores how well a note's field values align with a set of policy clauses.
// The result is a percentage 0.0–100.0 weighted by clause parity.
type PolicyAligner interface {
	// AlignPolicy takes a plain-text summary of note field values and a list of
	// enforceable policy clauses. Returns an alignment percentage 0.0–100.0.
	AlignPolicy(ctx context.Context, noteContent string, clauses []PolicyClause) (float64, error)
}

// ClauseCheckResult is a per-clause compliance result from a detailed policy check.
type ClauseCheckResult struct {
	BlockID   string `json:"block_id"`
	Status    string `json:"status"`    // "satisfied" | "violated"
	Reasoning string `json:"reasoning"` // one-sentence explanation
	Parity    string `json:"parity"`    // "high" | "medium" | "low" — copied from clause
}

// PolicyDetailedChecker assesses each policy clause individually against a note's content.
// Unlike PolicyAligner (which returns a single %) this returns per-clause pass/fail with reasoning.
// Used for the user-triggered check-policy endpoint and submit-time blocking.
type PolicyDetailedChecker interface {
	CheckPolicyClauses(ctx context.Context, noteContent string, clauses []PolicyClause) ([]ClauseCheckResult, error)
}

// FormCoverageResult is the combined output of a form-level coverage check:
// a plain-text narrative the user reads, plus per-clause structured results
// the service uses to compute a result percentage and persist evidence.
type FormCoverageResult struct {
	Narrative string              // free-form analysis (OVERALL / COVERED / GAPS / SUGGESTIONS)
	Clauses   []ClauseCheckResult // per-clause status; BlockID matches input clause IDs
}

// FormCoverageChecker assesses whether a form's field design covers the requirements
// of the policy clauses linked to that form. Used at form-design time (not runtime).
type FormCoverageChecker interface {
	// CheckFormCoverage takes the form's overall context prompt, its field definitions,
	// and the enforceable clauses from all linked policies. Returns a narrative
	// plus per-clause pass/fail so the service can compute a result percentage
	// and persist structured evidence on the form version.
	CheckFormCoverage(ctx context.Context, overallPrompt string, fields []FieldSpec, clauses []PolicyClause) (*FormCoverageResult, error)
}
