package extraction

import (
	"context"
	"fmt"
)

// OpenAIExtractor implements Extractor using OpenAI GPT-4o.
// Recommended for production — uses structured JSON output mode for
// reliable field extraction. Requires OPENAI_API_KEY env var.
//
// TODO: implement when OpenAI Go SDK is added as a dependency.
// Until then this returns ErrNotImplemented so the job can skip extraction
// gracefully and mark the note as failed with a clear message.
type OpenAIExtractor struct {
	apiKey string
}

// NewOpenAIExtractor constructs an OpenAIExtractor.
func NewOpenAIExtractor(apiKey string) *OpenAIExtractor {
	return &OpenAIExtractor{apiKey: apiKey}
}

// Extract is not yet implemented. Returns an error that the River job
// handles by marking the note as failed with a descriptive message.
func (e *OpenAIExtractor) Extract(_ context.Context, _, _ string, _ []FieldSpec) ([]FieldResult, error) {
	return nil, fmt.Errorf("extraction.openai: not yet implemented — set EXTRACTION_PROVIDER=gemini for dev")
}
