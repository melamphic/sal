package extraction

import (
	"context"
	"fmt"

	"github.com/melamphic/sal/internal/platform/config"
)

// NewFromConfig creates the Extractor configured by the environment.
// Returns nil (no error) when no API keys are set — callers skip extraction.
func NewFromConfig(ctx context.Context, cfg *config.Config) (Extractor, error) {
	switch cfg.ExtractionProvider {
	case "openai":

		if cfg.OpenAIAPIKey == "" {
			return nil, nil
		}
		return NewOpenAIExtractor(cfg.OpenAIAPIKey), nil
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			return nil, nil
		}
		e, err := NewGeminiExtractor(ctx, cfg.GeminiAPIKey)
		if err != nil {
			return nil, fmt.Errorf("extraction.NewFromConfig: %w", err)
		}
		return e, nil
	default:
		return nil, fmt.Errorf("extraction.NewFromConfig: unknown provider %q (use gemini or openai)", cfg.ExtractionProvider)
	}
}
