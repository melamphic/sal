package extraction

import (
	"context"
	"fmt"

	"github.com/melamphic/sal/internal/platform/config"
)

// newGemini constructs a GeminiExtractor or returns nil when the API key is absent.
func newGemini(ctx context.Context, cfg *config.Config) (*GeminiExtractor, error) {
	if cfg.GeminiAPIKey == "" {
		return nil, nil
	}
	e, err := NewGeminiExtractor(ctx, cfg.GeminiAPIKey)
	if err != nil {
		return nil, fmt.Errorf("extraction: init gemini: %w", err)
	}
	return e, nil
}

// newOpenAI constructs an OpenAIExtractor or returns nil when the API key is absent.
func newOpenAI(cfg *config.Config) *OpenAIExtractor {
	if cfg.OpenAIAPIKey == "" {
		return nil
	}
	return NewOpenAIExtractor(cfg.OpenAIAPIKey)
}

// NewFromConfig creates the Extractor configured by EXTRACTION_PROVIDER.
// Returns nil (no error) when the provider's API key is not set.
func NewFromConfig(ctx context.Context, cfg *config.Config) (Extractor, error) {
	switch cfg.ExtractionProvider {
	case "openai":
		return newOpenAI(cfg), nil
	case "gemini":
		return newGemini(ctx, cfg)
	default:
		return nil, fmt.Errorf("extraction.NewFromConfig: unknown provider %q (use gemini or openai)", cfg.ExtractionProvider)
	}
}

// NewPolicyAlignerFromConfig creates a PolicyAligner from config.
// Both Gemini and OpenAI support policy alignment.
// Returns nil (no error) when the provider's API key is not set.
func NewPolicyAlignerFromConfig(ctx context.Context, cfg *config.Config) (PolicyAligner, error) {
	switch cfg.ExtractionProvider {
	case "openai":
		return newOpenAI(cfg), nil
	case "gemini":
		return newGemini(ctx, cfg)
	default:
		return nil, nil
	}
}

// NewPolicyDetailedCheckerFromConfig creates a PolicyDetailedChecker from config.
// Returns nil (no error) when the provider's API key is not set.
func NewPolicyDetailedCheckerFromConfig(ctx context.Context, cfg *config.Config) (PolicyDetailedChecker, error) {
	switch cfg.ExtractionProvider {
	case "gemini":
		return newGemini(ctx, cfg)
	default:
		return nil, nil
	}
}

// NewFormCheckerFromConfig creates a FormCoverageChecker from config.
// Both Gemini and OpenAI support form coverage checking.
// Returns nil (no error) when the provider's API key is not set.
func NewFormCheckerFromConfig(ctx context.Context, cfg *config.Config) (FormCoverageChecker, error) {
	switch cfg.ExtractionProvider {
	case "openai":
		return newOpenAI(cfg), nil
	case "gemini":
		return newGemini(ctx, cfg)
	default:
		return nil, nil
	}
}
