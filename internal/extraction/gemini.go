package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

const geminiModel = "gemini-2.5-flash"

// GeminiExtractor implements Extractor using Google Gemini.
// Suitable for development (free tier) and cost-sensitive deployments.
type GeminiExtractor struct {
	client *genai.Client
}

// NewGeminiExtractor constructs a GeminiExtractor from an API key.
func NewGeminiExtractor(ctx context.Context, apiKey string) (*GeminiExtractor, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("extraction.gemini: new client: %w", err)
	}
	return &GeminiExtractor{client: client}, nil
}

// Extract calls Gemini to fill form fields from a transcript.
// Returns one FieldResult per non-skippable field spec in order.
func (e *GeminiExtractor) Extract(ctx context.Context, transcript, overallPrompt string, fields []FieldSpec) ([]FieldResult, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	prompt := buildPrompt(transcript, overallPrompt, fields)

	resp, err := e.client.Models.GenerateContent(
		ctx,
		geminiModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			// Temperature 0 = deterministic, maximally consistent extraction.
			Temperature: genai.Ptr[float32](0),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("extraction.gemini: generate: %w", err)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("extraction.gemini: empty response from model")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text
	return parseExtractionResponse(raw, fields)
}

// ── Prompt building ───────────────────────────────────────────────────────────

// geminiResponseField matches the JSON schema the model is asked to produce.
type geminiResponseField struct {
	FieldID            string   `json:"field_id"`
	Value              *string  `json:"value"`               // JSON-encoded; null if not found
	Confidence         *float64 `json:"confidence"`          // 0.0–1.0
	SourceQuote        *string  `json:"source_quote"`        // verbatim quote from transcript
	TransformationType *string  `json:"transformation_type"` // "direct" or "inference"
}

func buildPrompt(transcript, overallPrompt string, fields []FieldSpec) string {
	var sb strings.Builder

	sb.WriteString("You are a clinical documentation AI. Extract structured data from the veterinary consultation transcript below.\n\n")

	if overallPrompt != "" {
		sb.WriteString("## Form context\n")
		sb.WriteString(overallPrompt)
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Transcript\n")
	sb.WriteString(transcript)
	sb.WriteString("\n\n")

	sb.WriteString("## Fields to extract\n")
	for _, f := range fields {
		sb.WriteString(fmt.Sprintf("- field_id: %q, title: %q, type: %q", f.ID, f.Title, f.Type))
		if f.AIPrompt != "" {
			sb.WriteString(fmt.Sprintf(", hint: %q", f.AIPrompt))
		}
		if f.Required {
			sb.WriteString(", required: true")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`
## Instructions
- Return a JSON array with one object per field.
- Each object must have: field_id (string), value (string or null), confidence (0.0–1.0 float or null), source_quote (string or null), transformation_type (string or null).
- value: JSON-encoded string representing the field value (e.g. "42", "\"mild\"", "[\"option1\"]"). Use null if not mentioned.
- confidence: how certain you are (0.0 = guess, 1.0 = verbatim). Use null only when value is null.
- source_quote: verbatim text from the transcript supporting the value. Use null when value is null.
- transformation_type: "direct" if the value appears verbatim or near-verbatim in the transcript; "inference" if derived or computed from surrounding context. Use null when value is null.
- Do not add fields not in the list. Do not omit any field from the list.
- For required fields with no evidence, set value to null and confidence to 0.0.

Respond with ONLY the JSON array. No markdown fences.
`)

	return sb.String()
}

// ── Policy alignment ──────────────────────────────────────────────────────────

// geminiClauseResult is the per-clause JSON response from the alignment prompt.
type geminiClauseResult struct {
	BlockID   string `json:"block_id"`
	Satisfied bool   `json:"satisfied"`
}

// AlignPolicy calls Gemini to assess how well the note content satisfies each policy clause.
// Returns a weighted alignment percentage (0.0–100.0).
// Weights: high=3, medium=2, low=1.
func (e *GeminiExtractor) AlignPolicy(ctx context.Context, noteContent string, clauses []PolicyClause) (float64, error) {
	if len(clauses) == 0 {
		return 100.0, nil
	}

	var sb strings.Builder
	sb.WriteString("You are a clinical compliance AI. Assess whether the following note satisfies each policy clause.\n\n")
	sb.WriteString("## Note content\n")
	sb.WriteString(noteContent)
	sb.WriteString("\n\n## Policy clauses\n")
	for _, c := range clauses {
		sb.WriteString(fmt.Sprintf("- block_id: %q, title: %q, parity: %q\n", c.BlockID, c.Title, c.Parity))
	}
	sb.WriteString(`
## Instructions
Return a JSON array with one object per clause in the same order.
Each object: { "block_id": "<id>", "satisfied": true/false }
satisfied=true if the note content clearly addresses the clause requirement.
satisfied=false if the clause is not addressed or cannot be determined from the note.
Respond with ONLY the JSON array. No markdown fences.
`)

	resp, err := e.client.Models.GenerateContent(
		ctx,
		geminiModel,
		genai.Text(sb.String()),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			Temperature:      genai.Ptr[float32](0),
		},
	)
	if err != nil {
		return 0, fmt.Errorf("extraction.gemini.AlignPolicy: generate: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return 0, fmt.Errorf("extraction.gemini.AlignPolicy: empty response from model")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var results []geminiClauseResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return 0, fmt.Errorf("extraction.gemini.AlignPolicy: parse response: %w (raw: %.200s)", err, raw)
	}

	// Index results by block_id for lookup.
	satisfied := make(map[string]bool, len(results))
	for _, r := range results {
		satisfied[r.BlockID] = r.Satisfied
	}

	// Compute weighted score.
	weights := map[string]float64{"high": 3, "medium": 2, "low": 1}
	var total, earned float64
	for _, c := range clauses {
		w := weights[c.Parity]
		if w == 0 {
			w = 1
		}
		total += w
		if satisfied[c.BlockID] {
			earned += w
		}
	}
	if total == 0 {
		return 100.0, nil
	}
	return (earned / total) * 100.0, nil
}

func parseExtractionResponse(raw string, fields []FieldSpec) ([]FieldResult, error) {
	// Strip markdown fences if model adds them despite instructions.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var parsed []geminiResponseField
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("extraction.gemini: parse response: %w (raw: %.200s)", err, raw)
	}

	// Index by field_id for O(1) lookup.
	byID := make(map[string]geminiResponseField, len(parsed))
	for _, p := range parsed {
		byID[p.FieldID] = p
	}

	results := make([]FieldResult, len(fields))
	for i, f := range fields {
		p, ok := byID[f.ID]
		if !ok {
			// Model omitted this field — treat as not found.
			results[i] = FieldResult{FieldID: f.ID}
			continue
		}
		results[i] = FieldResult{
			FieldID:            f.ID,
			Value:              p.Value,
			Confidence:         p.Confidence,
			SourceQuote:        p.SourceQuote,
			TransformationType: p.TransformationType,
		}
	}
	return results, nil
}
