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

// extractionSchema is the ResponseSchema for form field extraction.
// Using ResponseSchema gives API-level enforcement — stronger than prompt-only JSON mode.
var extractionSchema = &genai.Schema{
	Type: genai.TypeArray,
	Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"field_id":            {Type: genai.TypeString},
			"value":               {Type: genai.TypeString, Nullable: genai.Ptr(true)},
			"confidence":          {Type: genai.TypeNumber, Nullable: genai.Ptr(true)},
			"source_quote":        {Type: genai.TypeString, Nullable: genai.Ptr(true)},
			"transformation_type": {Type: genai.TypeString, Nullable: genai.Ptr(true)},
		},
		Required: []string{"field_id"},
	},
}

// clauseSchema is the ResponseSchema for policy alignment clause results.
var clauseSchema = &genai.Schema{
	Type: genai.TypeArray,
	Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"block_id":  {Type: genai.TypeString},
			"satisfied": {Type: genai.TypeBoolean},
		},
		Required: []string{"block_id", "satisfied"},
	},
}

// noThinking disables thinking tokens on gemini-2.5-flash.
// Without this, thinking tokens are billed at output token rates ($2.50/M).
var noThinking = &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)}

// Extract calls Gemini to fill form fields from a transcript.
// Returns one FieldResult per non-skippable field spec in order.
func (e *GeminiExtractor) Extract(ctx context.Context, vertical, transcript, overallPrompt string, fields []FieldSpec) ([]FieldResult, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	prompt := buildPrompt(vertical, transcript, overallPrompt, fields)

	resp, err := e.client.Models.GenerateContent(
		ctx,
		geminiModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			ResponseSchema:   extractionSchema,
			Temperature:      genai.Ptr[float32](0),
			ThinkingConfig:   noThinking,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("extraction.gemini: generate: %w", err)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("extraction.gemini: empty response from model")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text
	results, err := parseExtractionResponse(raw, fields)
	if err != nil {
		return nil, err
	}
	if verr := ValidateExtractionResponse(fields, results); verr != nil {
		return nil, fmt.Errorf("extraction.gemini.Extract: %w", verr)
	}
	return results, nil
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

func buildPrompt(vertical, transcript, overallPrompt string, fields []FieldSpec) string {
	var sb strings.Builder

	sb.WriteString("You are a clinical documentation AI. Extract structured data from the clinical consultation transcript below.\n")
	sb.WriteString(verticalContextLine(vertical))
	sb.WriteString("\n\n")

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
		if !f.AllowInference {
			sb.WriteString(", direct_only: true")
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
- direct_only: true means the field must only be set when the value appears verbatim or near-verbatim. Set transformation_type to "direct" only. If no verbatim match found, set value to null.
- Do not add fields not in the list. Do not omit any field from the list.
- For required fields with no evidence, set value to null and confidence to 0.0.

## System widget fields — typed compliance entities
Some fields have a type starting with "system." — these are typed
compliance widgets. The note-submit pipeline will materialise each as a
real ledger row (consent / drug op / incident / pain). Emit value as a
JSON-encoded OBJECT (not a free-text string) with the keys below. If the
transcript doesn't mention the relevant event, value = null.

system.consent → emit object with keys:
    consent_type    one of: audio_recording, ai_processing, telemedicine,
                    sedation, euthanasia, invasive_procedure, mhr_write,
                    photography, data_sharing,
                    controlled_drug_administration, treatment_plan, other
    scope           1-2 sentence plain-English summary of what was consented to
    captured_via    one of: verbal_clinic, verbal_telehealth,
                    written_signature, electronic_signature, guardian
    consenting_party_name   name of the signer if not the patient (vet =
                    owner, aged-care = SDM/EPOA, etc.) — null if self
    consenting_party_relationship  one of: self, owner, guardian, epoa,
                    nok, authorised_representative, other
    risks_discussed_summary       short — what was explained
    alternatives_discussed_summary short — what alternatives were named
    witness_name    free-text name if a witness is mentioned (will be
                    matched against the staff list later) — null otherwise

system.drug_op → emit object with keys:
    operation       one of: administer, dispense, discard, receive,
                    transfer, adjust
    drug_name       drug as stated in transcript (will be matched
                    against the shelf catalog)
    quantity        numeric (no unit string)
    unit            mg / mL / g / tab / cap / unit
    dose            free-text dose phrase (e.g. "5 mg/kg")
    route           IV / IM / PO / SC / IN / topical / etc.
    reason_indication  free-text indication
    witness_name    free-text — null if not mentioned
    NOTE: AI emits this only as a SUGGESTION. The clinician must
    explicitly confirm before the row is logged to the ledger.

system.incident → emit object with keys:
    incident_type   short kind (fall, medication_error, pressure_injury,
                    near_miss, abuse_allegation, infection_outbreak,
                    elopement, other)
    severity        one of: low, medium, high, critical
    brief_description  one-sentence summary
    immediate_actions  short list / sentence of what was done in response
    location        room / area if mentioned, null otherwise
    witnesses_text  free-text mention of any witnesses, null otherwise
    subject_outcome one of: no_harm, minor_injury, moderate_injury,
                    hospitalised, deceased, complaint_resolved, unknown

system.pain_score → emit object with keys:
    score           integer 0-10
    pain_scale_used one of: nrs, flacc, painad, wong_baker, vrs, vas
                    (infer from the population mentioned in transcript:
                    cat/dog/animal → flacc; dementia resident → painad;
                    young child → wong_baker; otherwise nrs)
    note            free-text supporting context

For all system fields: still set source_quote to the verbatim transcript
text that backs the values. Set transformation_type to "direct" if every
key is verbatim, "inference" if you derived any of them from context.

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
func (e *GeminiExtractor) AlignPolicy(ctx context.Context, vertical, noteContent string, clauses []PolicyClause) (float64, error) {
	if len(clauses) == 0 {
		return 100.0, nil
	}

	var sb strings.Builder
	sb.WriteString("You are a clinical compliance AI. Assess whether the following note satisfies each policy clause.\n")
	sb.WriteString(verticalContextLine(vertical))
	sb.WriteString("\n\n")
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
			ResponseSchema:   clauseSchema,
			Temperature:      genai.Ptr[float32](0),
			ThinkingConfig:   noThinking,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("extraction.gemini.AlignPolicy: generate: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return 0, fmt.Errorf("extraction.gemini.AlignPolicy: empty response from model")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text

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

// ── Per-clause policy check ───────────────────────────────────────────────────

// detailedClauseSchema is the ResponseSchema for per-clause compliance results.
var detailedClauseSchema = &genai.Schema{
	Type: genai.TypeArray,
	Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"block_id":  {Type: genai.TypeString},
			"status":    {Type: genai.TypeString, Enum: []string{"satisfied", "violated"}},
			"reasoning": {Type: genai.TypeString},
		},
		Required: []string{"block_id", "status", "reasoning"},
	},
}

// geminiDetailedClauseResult is the JSON structure returned by the policy check prompt.
type geminiDetailedClauseResult struct {
	BlockID   string `json:"block_id"`
	Status    string `json:"status"`    // "satisfied" | "violated"
	Reasoning string `json:"reasoning"` // one-sentence explanation
}

// CheckPolicyClauses calls Gemini to assess each policy clause individually against note content.
// Returns per-clause results with reasoning. Parity is copied from the input clause.
func (e *GeminiExtractor) CheckPolicyClauses(ctx context.Context, vertical, noteContent string, clauses []PolicyClause) ([]ClauseCheckResult, error) {
	if len(clauses) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	sb.WriteString("You are a clinical compliance AI. Assess whether the following note satisfies each policy clause individually.\n")
	sb.WriteString(verticalContextLine(vertical))
	sb.WriteString("\n\n")
	sb.WriteString("## Note content\n")
	sb.WriteString(noteContent)
	sb.WriteString("\n\n## Policy clauses\n")
	for _, c := range clauses {
		sb.WriteString(fmt.Sprintf("- block_id: %q, title: %q, parity: %q\n", c.BlockID, c.Title, c.Parity))
	}
	sb.WriteString(`
## Instructions
Return a JSON array with one object per clause in the same order.
Each object: { "block_id": "<id>", "status": "satisfied"|"violated", "reasoning": "<one sentence>" }
status=satisfied if the note content clearly addresses the clause requirement.
status=violated if the clause is not addressed or cannot be determined from the note.
reasoning: a brief one-sentence explanation of why the clause is satisfied or violated.
Respond with ONLY the JSON array. No markdown fences.
`)

	resp, err := e.client.Models.GenerateContent(
		ctx,
		geminiModel,
		genai.Text(sb.String()),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			ResponseSchema:   detailedClauseSchema,
			Temperature:      genai.Ptr[float32](0),
			ThinkingConfig:   noThinking,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("extraction.gemini.CheckPolicyClauses: generate: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("extraction.gemini.CheckPolicyClauses: empty response from model")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text
	var rawResults []geminiDetailedClauseResult
	if err := json.Unmarshal([]byte(raw), &rawResults); err != nil {
		return nil, fmt.Errorf("extraction.gemini.CheckPolicyClauses: parse response: %w (raw: %.200s)", err, raw)
	}

	// Index raw results by block_id.
	byBlockID := make(map[string]geminiDetailedClauseResult, len(rawResults))
	for _, r := range rawResults {
		byBlockID[r.BlockID] = r
	}

	// Build result slice matching input clause order, enriched with parity.
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

	if verr := ValidatePolicyCheckResponse(clauses, results); verr != nil {
		return nil, fmt.Errorf("extraction.gemini.CheckPolicyClauses: %w", verr)
	}
	return results, nil
}

// ── Form coverage check ───────────────────────────────────────────────────────

// formCoverageSchema is the ResponseSchema for the structured form-coverage check:
// a narrative string plus a per-clause array so the service can persist evidence.
var formCoverageSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"narrative": {Type: genai.TypeString},
		"clauses": {
			Type: genai.TypeArray,
			Items: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"block_id":  {Type: genai.TypeString},
					"status":    {Type: genai.TypeString, Enum: []string{"satisfied", "violated"}},
					"reasoning": {Type: genai.TypeString},
				},
				Required: []string{"block_id", "status", "reasoning"},
			},
		},
	},
	Required: []string{"narrative", "clauses"},
}

// geminiFormCoverageResult mirrors the schema above for JSON unmarshalling.
type geminiFormCoverageResult struct {
	Narrative string                       `json:"narrative"`
	Clauses   []geminiDetailedClauseResult `json:"clauses"`
}

// CheckFormCoverage calls Gemini to assess whether the form's fields cover the
// requirements of the linked policy clauses. Returns a narrative + per-clause
// structured results in a single call.
func (e *GeminiExtractor) CheckFormCoverage(ctx context.Context, vertical, overallPrompt string, fields []FieldSpec, clauses []PolicyClause) (*FormCoverageResult, error) {
	if len(clauses) == 0 {
		return &FormCoverageResult{
			Narrative: "No policy clauses found on linked policies. Add clauses to your policies to enable compliance analysis.",
		}, nil
	}

	var sb strings.Builder
	sb.WriteString("You are a clinical compliance analyst. Assess whether the following form design adequately captures the data required by the linked policy clauses.\n")
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

	sb.WriteString(formCoveragePromptInstructions)
	sb.WriteString("\nRespond with ONLY the JSON object. No markdown fences.\n")

	resp, err := e.client.Models.GenerateContent(
		ctx,
		geminiModel,
		genai.Text(sb.String()),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			ResponseSchema:   formCoverageSchema,
			Temperature:      genai.Ptr[float32](0.2),
			ThinkingConfig:   noThinking,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("extraction.gemini.CheckFormCoverage: generate: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("extraction.gemini.CheckFormCoverage: empty response from model")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text
	var parsed geminiFormCoverageResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("extraction.gemini.CheckFormCoverage: parse response: %w (raw: %.200s)", err, raw)
	}

	byBlockID := make(map[string]geminiDetailedClauseResult, len(parsed.Clauses))
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

func parseExtractionResponse(raw string, fields []FieldSpec) ([]FieldResult, error) {
	// Strip markdown fences if model adds them despite instructions.
	// Find the first '[' and last ']' to handle preamble text or fences.
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "["); start != -1 {
		if end := strings.LastIndex(raw, "]"); end > start {
			raw = raw[start : end+1]
		}
	}

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
