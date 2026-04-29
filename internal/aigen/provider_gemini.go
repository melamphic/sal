package aigen

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

const defaultGeminiModel = "gemini-2.5-flash"

// geminiProvider is the dev/staging implementation of Provider. Uses the same
// SDK as the existing extraction package (google.golang.org/genai).
type geminiProvider struct {
	client *genai.Client
	model  string
}

func newGeminiProvider(apiKey, modelOverride string) (*geminiProvider, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("aigen.provider.gemini.new: %w", err)
	}
	model := modelOverride
	if model == "" {
		model = defaultGeminiModel
	}
	return &geminiProvider{client: client, model: model}, nil
}

func (p *geminiProvider) Name() string  { return "gemini" }
func (p *geminiProvider) Model() string { return p.model }

func (p *geminiProvider) GenerateJSON(ctx context.Context, prompt string, schemaName string) ([]byte, error) {
	schema, err := geminiSchemaFor(schemaName)
	if err != nil {
		return nil, fmt.Errorf("aigen.provider.gemini.GenerateJSON: %w", err)
	}

	// Honor cancellation eagerly — if ctx is already done we should not even
	// hit the network.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("aigen.provider.gemini.GenerateJSON: %w", err)
	}

	resp, err := p.client.Models.GenerateContent(
		ctx,
		p.model,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			ResponseMIMEType: "application/json",
			ResponseSchema:   schema,
			Temperature:      genai.Ptr[float32](0.2),
			ThinkingConfig:   &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)},
		},
	)
	if err != nil {
		// Distinguish ctx cancellation from upstream errors so callers can
		// branch on errors.Is(err, context.Canceled).
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("aigen.provider.gemini.GenerateJSON: %w", ctxErr)
		}
		return nil, fmt.Errorf("aigen.provider.gemini.GenerateJSON: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("aigen.provider.gemini.GenerateJSON: %w", ErrEmptyResponse)
	}
	return []byte(resp.Candidates[0].Content.Parts[0].Text), nil
}

// geminiSchemaFor returns the genai.Schema for the named JSON contract.
// Built from the forms/policy schema registries — single source of truth.
func geminiSchemaFor(name string) (*genai.Schema, error) {
	switch name {
	case SchemaForm:
		return geminiFormSchema(), nil
	case SchemaPolicy:
		return geminiPolicySchema(), nil
	case SchemaConsentDraft:
		return geminiConsentDraftSchema(), nil
	case SchemaIncidentDraft:
		return geminiIncidentDraftSchema(), nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownSchema, name)
	}
}
