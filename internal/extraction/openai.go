package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

const openAIModel = openai.ChatModelGPT4_1Mini

// OpenAIExtractor implements Extractor, PolicyAligner, and FormCoverageChecker
// using OpenAI GPT-4.1-mini with strict JSON schema mode.
// Strict mode guarantees 100% schema conformance — no markdown-fence stripping needed.
type OpenAIExtractor struct {
	client *openai.Client
}

// NewOpenAIExtractor constructs an OpenAIExtractor.
func NewOpenAIExtractor(apiKey string) *OpenAIExtractor {
	c := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIExtractor{client: &c}
}

// ── Schema types ──────────────────────────────────────────────────────────────
// Do NOT add omitempty — the schema reflector treats omitempty fields as optional,
// which breaks strict mode enforcement.

type openAIExtractionField struct {
	FieldID            string   `json:"field_id"`
	Value              *string  `json:"value"`
	Confidence         *float64 `json:"confidence"`
	SourceQuote        *string  `json:"source_quote"`
	TransformationType *string  `json:"transformation_type"`
}

type openAIClauseResult struct {
	BlockID   string `json:"block_id"`
	Satisfied bool   `json:"satisfied"`
}

// ── Extractor ─────────────────────────────────────────────────────────────────

// Extract calls GPT-4.1-mini to fill form fields from a clinical transcript.
// Uses strict JSON schema mode — response is guaranteed to match the schema.
func (e *OpenAIExtractor) Extract(ctx context.Context, vertical, transcript, overallPrompt string, fields []FieldSpec) ([]FieldResult, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	prompt := buildPrompt(vertical, transcript, overallPrompt, fields)

	schema := buildArraySchema(openAIExtractionField{})

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openAIModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a clinical documentation AI. Extract structured data from clinical consultation transcripts. " + verticalContextLine(vertical)),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				Type: "json_schema",
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "extraction_result",
					Schema: schema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0),
	})
	if err != nil {
		return nil, fmt.Errorf("extraction.openai.Extract: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("extraction.openai.Extract: empty response from model")
	}

	raw := resp.Choices[0].Message.Content
	var parsed []openAIExtractionField
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("extraction.openai.Extract: parse response: %w", err)
	}

	byID := make(map[string]openAIExtractionField, len(parsed))
	for _, p := range parsed {
		byID[p.FieldID] = p
	}

	results := make([]FieldResult, len(fields))
	for i, f := range fields {
		p, ok := byID[f.ID]
		if !ok {
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

// ── PolicyAligner ─────────────────────────────────────────────────────────────

// AlignPolicy calls GPT-4.1-mini to assess how well the note content satisfies
// each policy clause. Returns a weighted alignment percentage (0.0–100.0).
// Weights: high=3, medium=2, low=1.
func (e *OpenAIExtractor) AlignPolicy(ctx context.Context, vertical, noteContent string, clauses []PolicyClause) (float64, error) {
	if len(clauses) == 0 {
		return 100.0, nil
	}

	var sb strings.Builder
	sb.WriteString("Assess whether the following clinical note satisfies each policy clause.\n")
	sb.WriteString(verticalContextLine(vertical))
	sb.WriteString("\n\n")
	sb.WriteString("## Note content\n")
	sb.WriteString(noteContent)
	sb.WriteString("\n\n## Policy clauses\n")
	for _, c := range clauses {
		sb.WriteString(fmt.Sprintf("- block_id: %q, title: %q, parity: %q\n", c.BlockID, c.Title, c.Parity))
	}
	sb.WriteString("\nReturn one object per clause in order. satisfied=true if the note clearly addresses the clause.")

	schema := buildArraySchema(openAIClauseResult{})

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openAIModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a clinical compliance AI evaluating policy adherence."),
			openai.UserMessage(sb.String()),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				Type: "json_schema",
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "alignment_result",
					Schema: schema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0),
	})
	if err != nil {
		return 0, fmt.Errorf("extraction.openai.AlignPolicy: %w", err)
	}
	if len(resp.Choices) == 0 {
		return 0, fmt.Errorf("extraction.openai.AlignPolicy: empty response from model")
	}

	var results []openAIClauseResult
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &results); err != nil {
		return 0, fmt.Errorf("extraction.openai.AlignPolicy: parse response: %w", err)
	}

	satisfied := make(map[string]bool, len(results))
	for _, r := range results {
		satisfied[r.BlockID] = r.Satisfied
	}

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

// ── FormCoverageChecker ───────────────────────────────────────────────────────

// openAIFormCoverageClause is the per-clause entry of the structured form-coverage response.
type openAIFormCoverageClause struct {
	BlockID   string `json:"block_id"`
	Status    string `json:"status"`
	Reasoning string `json:"reasoning"`
}

// openAIFormCoverageResponse is the strict-JSON shape returned by CheckFormCoverage.
type openAIFormCoverageResponse struct {
	Narrative string                     `json:"narrative"`
	Clauses   []openAIFormCoverageClause `json:"clauses"`
}

// CheckFormCoverage calls GPT-4.1-mini to assess whether the form's fields cover
// the requirements of the linked policy clauses. Returns a narrative plus
// per-clause pass/fail so the service can compute a result percentage.
func (e *OpenAIExtractor) CheckFormCoverage(ctx context.Context, vertical, overallPrompt string, fields []FieldSpec, clauses []PolicyClause) (*FormCoverageResult, error) {
	if len(clauses) == 0 {
		return &FormCoverageResult{
			Narrative: "No policy clauses found on linked policies. Add clauses to your policies to enable compliance analysis.",
		}, nil
	}

	var sb strings.Builder
	sb.WriteString("Assess whether the following form design adequately captures the data required by the linked policy clauses.\n")
	sb.WriteString(verticalContextLineForm(vertical))
	sb.WriteString("\n\n")

	if overallPrompt != "" {
		sb.WriteString("## Form purpose\n")
		sb.WriteString(overallPrompt)
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Form fields\n")
	for _, f := range fields {
		if f.Skippable {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %q (type: %s", f.Title, f.Type))
		if f.AIPrompt != "" {
			sb.WriteString(fmt.Sprintf(", hint: %q", f.AIPrompt))
		}
		if f.Required {
			sb.WriteString(", required")
		}
		sb.WriteString(")\n")
	}

	sb.WriteString("\n## Policy clauses to satisfy\n")
	for _, c := range clauses {
		sb.WriteString(fmt.Sprintf("- block_id: %q, title: %q, parity: %q\n", c.BlockID, c.Title, c.Parity))
	}

	sb.WriteString(`
Return a JSON object with two keys:

- "narrative": a markdown-formatted analysis with these four sections in order:

    ## Overall
    One sentence summary.

    ## Covered
    - One bullet per clause that is addressed by one or more fields. Reference the clause title and the field(s) covering it.

    ## Gaps
    - One bullet per clause with no capturing field, plus a concrete suggestion for what field to add.

    ## Suggestions
    - Up to 3 actionable bullets, if any.

    Use **bold** (with double asterisks) for field and clause names. Always separate sections with blank lines. Always end every list bullet with a newline.

- "clauses": an array with one object per input clause (same block_id). Each object:
    { "block_id": "<id>", "status": "satisfied"|"violated", "reasoning": "<one sentence>" }
    status=satisfied if at least one form field captures the data needed to satisfy the clause;
    status=violated otherwise.`)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"narrative": map[string]any{"type": "string"},
			"clauses": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"block_id":  map[string]any{"type": "string"},
						"status":    map[string]any{"type": "string", "enum": []string{"satisfied", "violated"}},
						"reasoning": map[string]any{"type": "string"},
					},
					"required":             []string{"block_id", "status", "reasoning"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"narrative", "clauses"},
		"additionalProperties": false,
	}

	resp, err := e.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openAIModel,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a clinical compliance analyst reviewing form designs against policy requirements."),
			openai.UserMessage(sb.String()),
		},
		Temperature: openai.Float(0.2),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				Type: "json_schema",
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "form_coverage",
					Schema: schema,
					Strict: openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("extraction.openai.CheckFormCoverage: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("extraction.openai.CheckFormCoverage: empty response from model")
	}

	var parsed openAIFormCoverageResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &parsed); err != nil {
		return nil, fmt.Errorf("extraction.openai.CheckFormCoverage: parse: %w", err)
	}

	byBlockID := make(map[string]openAIFormCoverageClause, len(parsed.Clauses))
	for _, r := range parsed.Clauses {
		byBlockID[r.BlockID] = r
	}
	results := make([]ClauseCheckResult, len(clauses))
	for i, c := range clauses {
		if r, ok := byBlockID[c.BlockID]; ok {
			results[i] = ClauseCheckResult{
				BlockID:   c.BlockID,
				Status:    r.Status,
				Reasoning: r.Reasoning,
				Parity:    c.Parity,
			}
		} else {
			results[i] = ClauseCheckResult{
				BlockID:   c.BlockID,
				Status:    "violated",
				Reasoning: "clause not assessed by model",
				Parity:    c.Parity,
			}
		}
	}

	return &FormCoverageResult{
		Narrative: strings.TrimSpace(parsed.Narrative),
		Clauses:   results,
	}, nil
}

// ── Schema helper ─────────────────────────────────────────────────────────────

// buildArraySchema builds a minimal JSON Schema for a slice of T based on struct
// field tags. Avoids adding the invopop/jsonschema dependency for a simple schema.
func buildArraySchema(item any) map[string]any {
	itemSchema := structToSchema(item)
	return map[string]any{
		"type":  "array",
		"items": itemSchema,
	}
}

func structToSchema(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)

	props := make(map[string]any, len(m))
	required := make([]string, 0, len(m))
	for k := range m {
		props[k] = map[string]any{"type": "string"}
		required = append(required, k)
	}

	// Override known numeric and boolean fields.
	if _, ok := props["confidence"]; ok {
		props["confidence"] = map[string]any{"type": "number"}
	}
	if _, ok := props["satisfied"]; ok {
		props["satisfied"] = map[string]any{"type": "boolean"}
		props["block_id"] = map[string]any{"type": "string"}
	}

	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}
