package aigen

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	formschema "github.com/melamphic/sal/internal/forms/schema"
	polschema "github.com/melamphic/sal/internal/policy/schema"
)

// defaultOpenAIModel mirrors the existing extraction package selection so
// dev/prod parity is easy to reason about.
const defaultOpenAIModel = openai.ChatModelGPT4_1Mini

// openaiProvider is the prod implementation of Provider. Uses OpenAI's
// strict JSON-schema mode (response_format = json_schema with strict=true) so
// non-conforming JSON is rejected at the API boundary — Layer 1 schema safety.
//
// Pattern matches sal/internal/extraction/openai.go which is in production.
type openaiProvider struct {
	client *openai.Client
	model  string
}

func newOpenAIProvider(apiKey, modelOverride string) *openaiProvider {
	c := openai.NewClient(option.WithAPIKey(apiKey))
	model := defaultOpenAIModel
	if modelOverride != "" {
		model = modelOverride
	}
	return &openaiProvider{client: &c, model: model}
}

func (p *openaiProvider) Name() string  { return "openai" }
func (p *openaiProvider) Model() string { return p.model }

func (p *openaiProvider) GenerateJSON(ctx context.Context, prompt string, schemaName string) ([]byte, error) {
	schema, schemaTitle, err := openaiSchemaFor(schemaName)
	if err != nil {
		return nil, fmt.Errorf("aigen.provider.openai.GenerateJSON: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("aigen.provider.openai.GenerateJSON: %w", err)
	}

	resp, err := p.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: p.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a clinical-documentation expert generating clinical forms or compliance policies. Output only valid JSON conforming to the provided schema. Never include prose, code fences, or commentary outside the JSON."),
			openai.UserMessage(prompt),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				Type: "json_schema",
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   schemaTitle,
					Schema: schema,
					Strict: openai.Bool(true),
				},
			},
		},
		Temperature: openai.Float(0.2),
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("aigen.provider.openai.GenerateJSON: %w", ctxErr)
		}
		return nil, fmt.Errorf("aigen.provider.openai.GenerateJSON: %w", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("aigen.provider.openai.GenerateJSON: %w", ErrEmptyResponse)
	}
	return []byte(resp.Choices[0].Message.Content), nil
}

// openaiSchemaFor returns the JSON Schema (as a generic map) for the named
// contract, plus a short title used as OpenAI's strict-output schema name.
//
// The schema is built from the forms / policy schema registries — single
// source of truth. Adding a new FieldType / Parity propagates here
// automatically.
func openaiSchemaFor(name string) (any, string, error) {
	switch name {
	case SchemaForm:
		return openaiFormSchema(), "GeneratedForm", nil
	case SchemaPolicy:
		return openaiPolicySchema(), "GeneratedPolicy", nil
	default:
		return nil, "", fmt.Errorf("%w: %s", ErrUnknownSchema, name)
	}
}

// jsonSchema is a thin alias to make the literal builders below readable.
type jsonSchema = map[string]any

func openaiFormSchema() jsonSchema {
	return jsonSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "description", "overall_prompt", "tags", "fields"},
		"properties": jsonSchema{
			"name":           jsonSchema{"type": "string", "maxLength": 200},
			"description":    jsonSchema{"type": []string{"string", "null"}, "maxLength": 1000},
			"overall_prompt": jsonSchema{"type": []string{"string", "null"}, "maxLength": 2000},
			"tags": jsonSchema{
				"type":     []string{"array", "null"},
				"maxItems": 10,
				"items":    jsonSchema{"type": "string", "maxLength": 40},
			},
			"fields": jsonSchema{
				"type":     "array",
				"minItems": 1,
				"maxItems": 20,
				"items":    openaiFieldSchema(),
			},
		},
	}
}

func openaiFieldSchema() jsonSchema {
	return jsonSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"position", "title", "type", "config",
			"ai_prompt", "required", "skippable", "allow_inference",
		},
		"properties": jsonSchema{
			"position": jsonSchema{"type": "integer", "minimum": 1},
			"title":    jsonSchema{"type": "string", "maxLength": 120},
			"type":     jsonSchema{"type": "string", "enum": stringsToAny(formschema.FieldTypeEnumValues())},
			"config": jsonSchema{
				"type":                 []string{"object", "null"},
				"additionalProperties": true,
			},
			"ai_prompt":       jsonSchema{"type": []string{"string", "null"}, "maxLength": 500},
			"required":        jsonSchema{"type": []string{"boolean", "null"}},
			"skippable":       jsonSchema{"type": []string{"boolean", "null"}},
			"allow_inference": jsonSchema{"type": []string{"boolean", "null"}},
		},
	}
}

func openaiPolicySchema() jsonSchema {
	return jsonSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "description", "content", "clauses"},
		"properties": jsonSchema{
			"name":        jsonSchema{"type": "string", "maxLength": 200},
			"description": jsonSchema{"type": []string{"string", "null"}, "maxLength": 1000},
			"content": jsonSchema{
				"type":     "array",
				"minItems": 1,
				"items": jsonSchema{
					"type":                 "object",
					"additionalProperties": true,
					"required":             []string{"id"},
					"properties": jsonSchema{
						"id":   jsonSchema{"type": "string", "maxLength": 120},
						"type": jsonSchema{"type": "string", "maxLength": 40},
						"data": jsonSchema{"type": []string{"object", "null"}, "additionalProperties": true},
					},
				},
			},
			"clauses": jsonSchema{
				"type":     "array",
				"minItems": 1,
				"maxItems": 20,
				"items":    openaiClauseSchema(),
			},
		},
	}
}

func openaiClauseSchema() jsonSchema {
	return jsonSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"block_id", "title", "body", "parity", "source_citation"},
		"properties": jsonSchema{
			"block_id":        jsonSchema{"type": "string", "maxLength": 120},
			"title":           jsonSchema{"type": "string", "maxLength": 200},
			"body":            jsonSchema{"type": "string", "maxLength": 2000},
			"parity":          jsonSchema{"type": "string", "enum": stringsToAny(polschema.ParityEnumValues())},
			"source_citation": jsonSchema{"type": []string{"string", "null"}, "maxLength": 500},
		},
	}
}

// stringsToAny converts []string enum values into []any for embedding into
// JSON Schema literals.
func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
