// Package extraction provides AI-powered form field extraction from audio transcripts.
// It defines the Extractor interface and concrete implementations for Gemini and OpenAI.
// The concrete implementation is chosen at startup based on EXTRACTION_PROVIDER config.
package extraction

import "context"

// FieldSpec describes a single form field to extract.
type FieldSpec struct {
	ID        string // UUID of the form_fields row
	Title     string // display name — included in prompt
	Type      string // widget type (text, slider, select, …) — helps AI understand expected format
	AIPrompt  string // per-field extraction hint from the form designer
	Required  bool   // AI must provide a value; absence flagged as low confidence
	Skippable bool   // excluded from AI extraction; leave value nil
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

// FormCoverageChecker assesses whether a form's field design covers the requirements
// of the policy clauses linked to that form. Used at form-design time (not runtime).
// Returns a qualitative text report the user can read and act on before publishing.
type FormCoverageChecker interface {
	// CheckFormCoverage takes the form's overall context prompt, its field definitions,
	// and the enforceable clauses from all linked policies. Returns a plain-text
	// compliance analysis with gaps and suggestions.
	CheckFormCoverage(ctx context.Context, overallPrompt string, fields []FieldSpec, clauses []PolicyClause) (string, error)
}
