package aigen

import (
	"context"
	"errors"
)

// Provider is the AI provider abstraction used by the aigen services.
//
// Implementations MUST enforce JSON schema at the API boundary (Layer 1 of
// the schema-safety design — see README.md). They MUST honor ctx.Done() to
// support user-cancelled requests.
type Provider interface {
	// GenerateJSON sends a prompt and returns raw JSON bytes that conform to
	// the provider's representation of the named schema. Caller validates the
	// payload via the validation package before persisting anything.
	//
	// schemaName is one of SchemaForm | SchemaPolicy. Each provider keeps its
	// own representation (e.g. genai.Schema for Gemini, JSON Schema for OpenAI
	// structured output) keyed by these names.
	//
	// Cancellation: if ctx is cancelled, the provider MUST return promptly
	// with an error wrapping ctx.Err().
	GenerateJSON(ctx context.Context, prompt string, schemaName string) ([]byte, error)

	// Name returns "gemini" | "openai" — used for AIMetadata.Provider.
	Name() string

	// Model returns the specific model identifier — used for AIMetadata.Model.
	Model() string
}

// Schema names used by Provider.GenerateJSON. New schemas added here MUST be
// implemented by every Provider implementation.
const (
	// SchemaForm is the response schema for form generation. Output unmarshals
	// into GeneratedForm.
	SchemaForm = "form"

	// SchemaPolicy is the response schema for policy generation. Output
	// unmarshals into GeneratedPolicy.
	SchemaPolicy = "policy"
)

// Sentinel provider errors. Callers can branch with errors.Is.
var (
	// ErrProviderNotConfigured is returned when the requested provider has no
	// API key configured.
	ErrProviderNotConfigured = errors.New("aigen.provider: not configured (missing API key)")

	// ErrEmptyResponse is returned when the model returned no content.
	ErrEmptyResponse = errors.New("aigen.provider: empty response from model")

	// ErrUnknownSchema is returned when a Provider is asked for a schema name
	// it does not implement.
	ErrUnknownSchema = errors.New("aigen.provider: unknown schema name")
)
