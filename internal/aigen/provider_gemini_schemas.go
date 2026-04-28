package aigen

import (
	"google.golang.org/genai"
)

// geminiFormSchema returns the Gemini ResponseSchema for SchemaForm.
//
// Constraint budget — important: Gemini compiles ResponseSchema into a
// state machine internally and rejects schemas whose state count exceeds
// an internal cap, surfacing as
// "The specified schema produces a constraint that has too many states
// for serving" (HTTP 400 INVALID_ARGUMENT). The combination most likely
// to trigger that cap is an enum-bearing string nested inside an array of
// objects (state count grows as enum-cardinality × array-length × number
// of sibling constraints). Stripping numeric/length/array bounds is not
// enough on its own — the field-type enum (11 values) crosses the line.
//
// So this schema deliberately does NOT include:
//   - MinItems / MaxItems on arrays
//   - MaxLength on strings
//   - Minimum / Maximum on numbers
//   - the field-type enum on `fields[].type`
//   - the parity enum on `clauses[].parity`
//
// All of those constraints are enforced (and any violations auto-repaired
// where possible) by the downstream pipeline:
//   - Layer 3 schema-registry validation — `forms/schema.ValidateConfig`
//     and `policy/schema.IsValidParity` reject unknown enum values
//   - Layer 4 type-aware config validation
//   - Layer 5 cross-field validation (positions, block IDs)
//   - Layer 6 auto-repair (renumber, default parity to medium, etc.)
//   - Layer 7 one Gemini retry with explicit error feedback
//   - Layer 8 DB CHECK / UNIQUE constraints
//
// Net safety: unchanged. Net Gemini state-count: comfortably under the
// cap. The prompt template restates the allowed enum values in natural
// language so the model still receives the constraint signal.
//
// OpenAI's strict-JSON-schema engine has no such state-cap problem and
// keeps its tighter schema (see provider_openai.go).
func geminiFormSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"name":           {Type: genai.TypeString},
			"description":    {Type: genai.TypeString, Nullable: genai.Ptr(true)},
			"overall_prompt": {Type: genai.TypeString, Nullable: genai.Ptr(true)},
			"tags": {
				Type:     genai.TypeArray,
				Items:    &genai.Schema{Type: genai.TypeString},
				Nullable: genai.Ptr(true),
			},
			"fields": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"position":        {Type: genai.TypeInteger},
						"title":           {Type: genai.TypeString},
						"type":            {Type: genai.TypeString},
						"config":          {Type: genai.TypeObject, Nullable: genai.Ptr(true)},
						"ai_prompt":       {Type: genai.TypeString, Nullable: genai.Ptr(true)},
						"required":        {Type: genai.TypeBoolean, Nullable: genai.Ptr(true)},
						"skippable":       {Type: genai.TypeBoolean, Nullable: genai.Ptr(true)},
						"allow_inference": {Type: genai.TypeBoolean, Nullable: genai.Ptr(true)},
					},
					Required: []string{"position", "title", "type"},
				},
			},
		},
		Required: []string{"name", "fields"},
	}
}

// geminiPolicySchema returns the Gemini ResponseSchema for SchemaPolicy.
// Same constraint-budget reasoning as geminiFormSchema — see that doc.
// Parity enum is enforced by the validator (policy/schema.IsValidParity),
// not by the provider schema, so Gemini stays under its state cap.
func geminiPolicySchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"name":        {Type: genai.TypeString},
			"description": {Type: genai.TypeString, Nullable: genai.Ptr(true)},
			"content": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"id":   {Type: genai.TypeString},
						"type": {Type: genai.TypeString},
						"data": {Type: genai.TypeObject, Nullable: genai.Ptr(true)},
					},
					Required: []string{"id"},
				},
			},
			"clauses": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"block_id":        {Type: genai.TypeString},
						"title":           {Type: genai.TypeString},
						"body":            {Type: genai.TypeString},
						"parity":          {Type: genai.TypeString},
						"source_citation": {Type: genai.TypeString, Nullable: genai.Ptr(true)},
					},
					Required: []string{"block_id", "title", "body", "parity"},
				},
			},
		},
		Required: []string{"name", "content", "clauses"},
	}
}
