package aigen

import (
	"fmt"
	"strings"
)

// FactoryConfig drives provider selection at startup. Read from env / config
// struct in app wiring (typically sal/internal/app/config or similar).
//
// Selection rules:
//   - If Provider is "openai" or "gemini", use that exact provider (errors if
//     its API key is missing).
//   - If Provider is empty, auto-select: prefer OpenAI when OPENAI_API_KEY is
//     set (prod), fall back to Gemini when GEMINI_API_KEY is set (dev).
//   - If neither key is set, returns ErrProviderNotConfigured.
type FactoryConfig struct {
	Provider     string
	GeminiAPIKey string
	OpenAIAPIKey string
	GeminiModel  string // optional override (default "gemini-2.5-flash")
	OpenAIModel  string // optional override (default "gpt-4o-mini")
}

// NewProvider constructs the configured Provider implementation.
// See FactoryConfig for selection rules.
func NewProvider(cfg FactoryConfig) (Provider, error) {
	pick := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if pick == "" {
		switch {
		case cfg.OpenAIAPIKey != "":
			pick = "openai"
		case cfg.GeminiAPIKey != "":
			pick = "gemini"
		default:
			return nil, ErrProviderNotConfigured
		}
	}
	switch pick {
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("aigen.factory: openai selected but OPENAI_API_KEY not set: %w", ErrProviderNotConfigured)
		}
		return newOpenAIProvider(cfg.OpenAIAPIKey, cfg.OpenAIModel), nil
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			return nil, fmt.Errorf("aigen.factory: gemini selected but GEMINI_API_KEY not set: %w", ErrProviderNotConfigured)
		}
		return newGeminiProvider(cfg.GeminiAPIKey, cfg.GeminiModel)
	default:
		return nil, fmt.Errorf("aigen.factory: unknown provider %q (want gemini|openai)", pick)
	}
}
