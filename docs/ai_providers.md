# AI Providers — Extraction, Policy Alignment, Form Coverage

*Last updated: 2026-04-15. Reflects actual code in `internal/extraction/` and current provider APIs.*

---

## Overview

Salvia uses LLMs for three distinct tasks inside the extraction pipeline:

| Task | Interface | When it runs |
|---|---|---|
| **Form field extraction** | `Extractor` | Per note, after Deepgram transcription completes (River job `ExtractNoteWorker`) |
| **Policy alignment scoring** | `PolicyAligner` | Per note, after extraction; also re-run on submit (River job `ComputePolicyAlignmentWorker`) |
| **Form coverage check** | `FormCoverageChecker` | On demand, at form-design time via `POST /api/v1/forms/{form_id}/policy-check` |

Provider is selected at startup via `EXTRACTION_PROVIDER` env var (`gemini` or `openai`). Both share the factory in `internal/extraction/factory.go`.

---

## Current State (April 2026)

| Provider | Extraction | Policy Alignment | Form Coverage | Status |
|---|---|---|---|---|
| **Gemini** (`gemini-2.5-flash`) | ✅ Implemented | ✅ Implemented | ✅ Implemented | Production-ready; used in dev (free tier) |
| **OpenAI** (`gpt-4.1-mini`) | ✅ Implemented | ✅ Implemented | ✅ Implemented | Fully implemented with strict schema mode |

---

## Provider 1 — Google Gemini

### SDK

```go
// go.mod
google.golang.org/genai v1.53.0
```

Client construction:

```go
client, err := genai.NewClient(ctx, &genai.ClientConfig{
    APIKey:  apiKey,
    Backend: genai.BackendGeminiAPI,
})
```

`genai.BackendGeminiAPI` routes to Google AI Studio. Switch to `genai.BackendVertexAI` for GCP-hosted deployments.

### Model

```go
const geminiModel = "gemini-2.5-flash"
```

`gemini-2.5-flash` (GA, early 2026):
- 1M token context window
- Native structured output via `ResponseMIMEType` + `ResponseSchema`
- Thinking tokens — **must be disabled** for extraction (add latency and cost with no benefit)

```go
&genai.GenerateContentConfig{
    ResponseMIMEType: "application/json",
    Temperature:      genai.Ptr[float32](0),
    ThinkingConfig:   &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)},
}
```

### Structured Output Approach

Use `ResponseSchema` for API-level enforcement (stricter than prompt-only JSON mode):

```go
schema := &genai.Schema{
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
config := &genai.GenerateContentConfig{
    ResponseMIMEType: "application/json",
    ResponseSchema:   schema,
    Temperature:      genai.Ptr[float32](0),
    ThinkingConfig:   &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)},
}
```

### Temperature Settings

| Task | Setting | Rationale |
|---|---|---|
| Field extraction | `0` | Deterministic; same transcript must produce same values for audit consistency |
| Policy alignment | `0` | Binary satisfied/not — no creativity needed |
| Form coverage check | `0.2` | Qualitative prose report; slight variation acceptable for readability |

### Pricing (April 2026)

| Tier | Input | Output |
|---|---|---|
| Free (Google AI Studio) | $0 | $0 |
| Paid | $0.30 / 1M tokens | $2.50 / 1M tokens |

Free tier: 15 RPM, 1,500 RPD — sufficient for dev and low-volume staging.

**Thinking token billing**: at `ThinkingBudget: 0` no thinking tokens are billed. Without this flag, thinking tokens count as output tokens at $2.50/M.

**Cost per note** (~600 input, ~200 output tokens): < $0.001

---

## Provider 2 — OpenAI

### SDK

```go
// go.mod
github.com/openai/openai-go v1.x   // official OpenAI Go SDK
```

Client construction:

```go
import (
    "github.com/openai/openai-go"
    "github.com/openai/openai-go/option"
)

client := openai.NewClient(option.WithAPIKey(apiKey))
```

### Model

```go
const openAIModel = openai.ChatModelGPT4_1Mini
```

**GPT-4.1 family** (released April 2025, GA):

| Model | Context | Input | Output | Use case |
|---|---|---|---|---|
| `gpt-4.1` | 1M tokens | $2.00 / 1M | $8.00 / 1M | Highest accuracy; premium tier |
| `gpt-4.1-mini` | 1M tokens | $0.40 / 1M | $1.60 / 1M | **Recommended default** — best cost/accuracy |
| `gpt-4.1-nano` | 1M tokens | $0.10 / 1M | $0.40 / 1M | Simple classification only |

`gpt-4.1-mini` is the recommended default: handles structured JSON reliably at 5× lower cost than `gpt-4.1`.

### Structured Output Approach

OpenAI **strict JSON schema mode** — API-enforced, 100% schema conformance guaranteed. No markdown-fence stripping needed.

```go
// FieldResult struct — do NOT use omitempty (treats fields as optional in schema)
type openAIExtractionField struct {
    FieldID            string   `json:"field_id"`
    Value              *string  `json:"value"`
    Confidence         *float64 `json:"confidence"`
    SourceQuote        *string  `json:"source_quote"`
    TransformationType *string  `json:"transformation_type"`
}

// Generate schema via GenerateSchema helper
schemaParam := openai.ResponseFormatJSONSchemaJSONSchemaParam{
    Name:        "extraction_result",
    Schema:      GenerateSchema[[]openAIExtractionField](),
    Strict:      openai.Bool(true),
}

resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model: openai.F(openAIModel),
    Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
        openai.SystemMessage("You are a clinical documentation AI..."),
        openai.UserMessage(prompt),
    }),
    ResponseFormat: openai.F[openai.ChatCompletionNewParamsResponseFormatUnion](
        openai.ResponseFormatJSONSchemaParam{
            Type:       openai.F(openai.ResponseFormatJSONSchemaTypeJSONSchema),
            JSONSchema: openai.F(schemaParam),
        },
    ),
    Temperature: openai.Float(0),
})
```

**Important**: avoid `omitempty` in JSON struct tags — the `jsonschema` reflector treats it as "optional", which can allow the model to skip fields.

### Pricing (April 2026)

| Model | Input | Output |
|---|---|---|
| `gpt-4.1` | $2.00 / 1M | $8.00 / 1M |
| `gpt-4.1-mini` | $0.40 / 1M | $1.60 / 1M |
| `gpt-4.1-nano` | $0.10 / 1M | $0.40 / 1M |

No free tier. A $5–10 top-up covers development. **Cost per note** with `gpt-4.1-mini`: < $0.001.

---

## Head-to-Head Comparison

| Dimension | Gemini 2.5 Flash | GPT-4.1 Mini |
|---|---|---|
| **SDK in go.mod** | ✅ Yes | ✅ Yes (added) |
| **Implementation status** | ✅ Complete | ✅ Complete |
| **Structured output mode** | `ResponseSchema` (API-enforced) | Strict schema (API-enforced) |
| **Markdown fence risk** | None (ResponseSchema) | None (strict mode) |
| **Temperature 0 reliability** | Reliable | Reliable |
| **Input cost** | $0.30 / 1M | $0.40 / 1M |
| **Output cost** | $2.50 / 1M | $1.60 / 1M |
| **Free tier** | ✅ Yes (1,500 RPD) | ❌ No |
| **Context window** | 1M tokens | 1M tokens |
| **Thinking tokens** | Must disable via `ThinkingBudget: 0` | N/A |
| **Clinical accuracy** | ~80% (med benchmarks) | ~70% |
| **Data routing** | Google | OpenAI |

**Output cost asymmetry**: Gemini output tokens cost more ($2.50 vs $1.60/M). For short extraction outputs both cost < $0.001/note so this rarely matters in practice.

---

## Environment Variables

```bash
# Choose provider
EXTRACTION_PROVIDER=gemini   # or: openai

# Gemini (Google AI Studio)
GEMINI_API_KEY=AIza...

# OpenAI
OPENAI_API_KEY=sk-...
```

Both keys can coexist. The factory reads only the key for the configured provider. If the configured provider's key is empty, extraction is disabled — notes go straight to `draft` for manual review.

---

## Recommendation

| Environment | Provider | Reason |
|---|---|---|
| Development | Gemini | Free tier, no billing setup |
| Staging | Gemini | Same as dev; validates pipeline at near-zero cost |
| Production (default) | Gemini | Cheaper input, stronger clinical accuracy |
| Production (strict schema compliance) | OpenAI `gpt-4.1-mini` | API-enforced schema, lower output cost |

Run both providers against real vet transcripts in staging before committing to a production provider.

---

## Sources

- [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing)
- [Gemini Structured Output](https://ai.google.dev/gemini-api/docs/structured-output)
- [Gemini Thinking](https://ai.google.dev/gemini-api/docs/thinking)
- [OpenAI Models](https://platform.openai.com/docs/models)
- [OpenAI Structured Outputs](https://platform.openai.com/docs/guides/structured-outputs)
- [openai-go SDK](https://github.com/openai/openai-go)
- [GPT-4.1 Introduction](https://openai.com/index/gpt-4-1/)
- [OpenAI API Pricing](https://openai.com/api/pricing/)
