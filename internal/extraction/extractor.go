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
	FieldID     string   // matches FieldSpec.ID
	Value       *string  // JSON-encoded value; nil = not found / skipped
	Confidence  *float64 // 0.0–1.0; nil when skippable or no value found
	SourceQuote *string  // supporting snippet from transcript; nil when not applicable
}

// Extractor extracts structured form field values from a clinical transcript.
type Extractor interface {
	// Extract takes the full transcript text, an overall context prompt for the
	// form, and a slice of field specs. It returns one FieldResult per spec.
	// The caller must pass exactly the non-skippable fields; skippable fields
	// should be excluded from specs before calling.
	Extract(ctx context.Context, transcript, overallPrompt string, fields []FieldSpec) ([]FieldResult, error)
}
