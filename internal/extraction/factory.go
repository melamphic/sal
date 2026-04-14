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

// NewPolicyAlignerFromConfig creates a PolicyAligner from config.
// Only Gemini supports policy alignment; OpenAI returns nil (skipped).
// Returns nil (no error) when no API key is set.
func NewPolicyAlignerFromConfig(ctx context.Context, cfg *config.Config) (PolicyAligner, error) {
	if cfg.ExtractionProvider != "gemini" || cfg.GeminiAPIKey == "" {
		return nil, nil
	}
	e, err := NewGeminiExtractor(ctx, cfg.GeminiAPIKey)
	if err != nil {
		return nil, fmt.Errorf("extraction.NewPolicyAlignerFromConfig: %w", err)
	}
	return e, nil
}
