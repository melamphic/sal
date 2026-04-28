# aigen — AI form & policy generation

`aigen` generates draft clinical forms and clinical-compliance policies from
free-text user descriptions. The output is always a **draft** persisted via
the existing forms / policy services; this package never auto-publishes
anything. A clinic staff member reviews and publishes through the existing
manual editor.

This package is the AI-generation engine; it is **domain-agnostic**: vertical
and country are runtime inputs, never code paths. Adding a new vertical or
country = drop a fewshot data file + a row in `regulators.go`. No code
changes anywhere else in the engine.

## Why this exists

Manual form / policy authoring is a cold-start tax on every new clinic.
Marketplace packs cover the typical case but miss long tails. AI generation
fills the gap: the user describes the form in two sentences, picks up a 70%
draft, edits the last 30% in the existing editor, publishes.

For policies, the same loop applies — except output includes regulator
**source citations** which the UI renders as "AI-suggested, verify against
[regulator]". They are never auto-trusted.

## Design principles

1. **Vertical and country are RUNTIME context, not code paths.** No
   `if vertical == "vet"` branches anywhere in this package. Adding a new
   vertical/country is a data-file change.
2. **`clinic.country` is locked at registration.** This package never accepts
   country in a request body — it reads from the clinic record. Country is a
   `ClinicContext.Country` field that callers populate from clinic state.
3. **Schema safety in 8 layers** (see below). Broken AI output is normal.
   The pipeline validates / repairs / retries before persisting anything.
4. **Provider abstraction.** Dev runs Gemini (free tier), prod runs OpenAI.
   New AI features must implement both providers — never ship a
   Gemini-only feature.
5. **Human-in-loop publishes.** AI output enters the existing draft state and
   the existing editor. AI output is always badged "AI drafted" so clinicians
   know to review.
6. **Cancellable.** Every generation honours `ctx.Done()` so the user can
   cancel a running generation from the UI. Both providers forward the
   context to their network calls.

## Schema-safety: 8 layers of defense

```
User prompt
    ↓
[1] Provider strict JSON-schema mode      ← API-level rejection of non-conforming output
    ↓
[2] JSON parse                              ← bytes → typed Go struct
    ↓
[3] Schema-registry validation              ← types/parities against single source of truth
    ↓
[4] Type-aware config validation            ← select needs options[], slider needs min<max
    ↓
[5] Cross-field validation                  ← positions unique 1..N, every clause block_id in content
    ↓
[6] Auto-repair pass                        ← deterministic fixes (renumber positions, default parity, etc.)
    ↓
[7] One Provider retry on remaining errors  ← send error context back to model, ask to fix
    ↓
[8] DB constraints                          ← CHECK / UNIQUE / NOT NULL — final goalkeeper
    ↓
Draft persisted ✓
```

Failure at any layer surfaces a structured error to the caller; nothing is
ever silently corrupted. Every successful call returns a list of repairs
applied so the UI can banner "AI draft was auto-corrected on these fields"
to the reviewer.

### Single source of truth: schema registries

The forms and policy schema registries are the canonical enums:

- `sal/internal/forms/schema/registry.go` — `FieldType`, `AllFieldTypes`,
  `FieldTypeEnumValues`, `ValidateConfig`
- `sal/internal/policy/schema/registry.go` — `Parity`, `AllParities`,
  `ParityEnumValues`, `ParityWeight`, `ParseContent`

Every change here propagates to the provider response schemas (Gemini +
OpenAI), the validator pipeline, and any future DB CHECK constraints. The
registries' tests fail the build if a new value is added without a
`ValidateConfig` / `ParityWeight` branch.

## Module layout

```
sal/internal/aigen/
├── README.md                        ← this file
├── types.go                         ← public types (FormGenInput, GeneratedForm, etc.)
├── regulators.go                    ← (country, vertical) → regulator metadata
├── provider.go                      ← Provider interface + sentinel errors
├── factory.go                       ← provider selection by env (auto: openai > gemini)
├── provider_gemini.go               ← Gemini implementation (dev)
├── provider_gemini_schemas.go       ← Gemini ResponseSchema literals
├── provider_openai.go               ← OpenAI implementation (prod)
├── provider_openai_enums.go         ← (placeholder; reserved)
├── validation.go                    ← validator pipeline + error codes
├── repair.go                        ← deterministic auto-repair pass
├── retry.go                         ← Generate→Validate→Repair→Retry orchestration
├── observability.go                 ← GenerationLog (slog-friendly, PII-free)
├── prompts.go                       ← embed-based template + fewshot loader
├── forms.go                         ← FormGenService (high-level orchestrator)
├── policies.go                      ← PolicyGenService (mirror)
├── prompts/
│   ├── form_system.tmpl             ← form generation prompt template
│   └── policy_system.tmpl           ← policy generation prompt template
└── fewshot/
    ├── form/                        ← 1 pack per vertical (3 examples each)
    │   ├── vet.json
    │   ├── dental.json
    │   ├── general.json
    │   └── aged_care.json
    └── policy/                      ← 1 pack per (vertical, country) combo
        ├── vet_NZ.json    │  vet_AU.json    │  vet_UK.json    │  vet_US.json
        ├── dental_NZ.json │  dental_AU.json │  dental_UK.json │  dental_US.json
        ├── general_NZ.json│  general_AU.json│  general_UK.json│  general_US.json
        └── aged_care_NZ.json│aged_care_AU.json│aged_care_UK.json│aged_care_US.json
```

## Wiring (caller responsibility)

This package returns `*GeneratedForm` / `*GeneratedPolicy` plus an
`AIMetadata` struct. It does NOT touch the forms / policy repositories
directly — the caller (forms / policy handler) integrates by:

1. Adding a new HTTP route, e.g. `POST /api/v1/forms/generate`.
2. Resolving `ClinicContext` from existing clinic services.
3. Optionally gathering reference forms (existing clinic forms +
   marketplace samples) for the prompt.
4. Calling `aigen.FormGenService.Generate(ctx, req)`.
5. Persisting the returned `*GeneratedForm` via the existing
   `forms.Service` — typically with a new `CreateFromAIPayload` method that
   atomically creates form + draft version + fields and stores the
   `AIMetadata` on `form_versions.generation_metadata`.
6. Emitting a timeline event for audit (`form.generated` / `policy.generated`).
7. Returning the resulting `FormResponse` to the client (so the UI can
   open the editor at the new draft).

The caller-side method (`CreateFromAIPayload`) is intentionally NOT in this
package because cross-domain repository access is forbidden by `CLAUDE.md`.

## Dependencies

This package depends on:

- `github.com/melamphic/sal/internal/forms/schema` — single source of truth
  for field types
- `github.com/melamphic/sal/internal/policy/schema` — single source of truth
  for parity + content blocks
- `github.com/openai/openai-go` — prod provider
- `google.golang.org/genai` — dev provider
- standard library only otherwise

It is **NOT** allowed to depend on:

- `sal/internal/forms` (the high-level forms service) — would create a cycle
- `sal/internal/policy` — same
- `sal/internal/clinic`, `sal/internal/marketplace`, etc. — caller injects
  context, never queries from inside aigen

## Configuration

Environment variables read by callers and passed to `FactoryConfig`:

- `AIGEN_PROVIDER` — `openai` | `gemini` | empty (auto: openai if key set, else gemini)
- `OPENAI_API_KEY` — required when provider is `openai`
- `OPENAI_AIGEN_MODEL` — optional override (default `gpt-4.1-mini`)
- `GEMINI_API_KEY` — required when provider is `gemini`
- `GEMINI_AIGEN_MODEL` — optional override (default `gemini-2.5-flash`)

Suggested binding env-var names; the actual env wiring lives in the app
config layer.

## Migration

`sal/migrations/00049_add_generation_metadata.sql`:

- adds `generation_metadata JSONB` to `form_versions` and `policy_versions`
- hardens `form_fields.position` (positive + unique per version) — defends
  in depth against any caller that bypasses validation

## Observability

Every generation emits a structured `aigen.generation` log via `slog`:

- `clinic_id`, `staff_id`, `vertical`, `country`, `kind`, `provider`, `model`
- `prompt_hash`, `latency_ms`, `retry_count`
- `validation_errors` — codes only (never AI/user content)
- `repairs` — action names only (never content)
- `outcome` — `success | validation_failed | provider_error | cancelled`
- `outcome_reason` — short tag for bucketing

Suggested dashboards (build outside this package):

- success-rate by `(vertical, country)` — catches prompt regressions per
  combo
- validation-error frequency by `code` — catches prompt drift
- retry rate — rising = prompt is degrading
- p95 latency — UX + cost signal

## What's shipped end-to-end

The feature is wired all the way through:

1. **`forms.Service.CreateFromAIGen` + `policy.Service.CreateFromAIGen`** —
   atomic create form/policy + draft version + fields/clauses and persist
   `AIMetadata` on `*_versions.generation_metadata`.
2. **HTTP routes** — `POST /api/v1/forms/generate` (`manage_forms`) and
   `POST /api/v1/policies/generate` (`manage_policies`).
3. **DI in `app.go`** — `aigen.NewProvider(cfg)` + per-IP `RateLimiterStore`
   (1 req per 10s, burst 3) + injected `AIGenHandler`s. Routes self-disable
   if no provider key is configured.
4. **Flutter** — `FormsListPage` / `PoliciesListPage` show a primary
   `✨ AI draft` button alongside the existing `+ Blank`; the modal opens
   the cubit + repository chain; on success the editor opens at the new
   draft. The editor renders an "AI drafted — review before publishing"
   pill (`AIDraftedPill`, exported from the design package) when the active
   version's `generation_metadata` is non-null. Policy clauses with a
   `source_citation` render an `AISuggestedCitation` badge with a "verify
   against [regulator]" hint.
5. **Fewshot packs** — 4 form packs (vet, dental, general, aged_care) and
   16 policy packs (4 verticals × NZ/AU/UK/US). Adding more is a data-only
   change — `LoadFewShotForm(vertical)` and `LoadFewShotPolicy(vertical,
   country)` look up by filename.
6. **Round-trip metadata** — `form_versions.generation_metadata` and
   `policy_versions.generation_metadata` flow back through the API as
   `generation_metadata` in version responses; the Flutter editor reads
   them via `FormVersion.isAIGenerated` / `PolicyVersion.isAIGenerated`.
7. **`policy_clauses.source_citation`** — added in migration 00050;
   accepted on the manual upsert-clauses API and persisted by AI gen.
8. **Per-IP rate limit** — both `/generate` endpoints share a tight
   limiter store. Tune in `app.go` if needed.

## What's optional for follow-up

- **Per-clinic rate limiting** (instead of per-IP) — reuse the same
  middleware pattern but key on `mw.ClinicIDFromContext`. Differentiated
  daily caps by tier (e.g. 50/day Practice, 200/day Pro) would slot in
  here.
- **CI consistency tests across (vertical, country) combos** — the schema
  registries already self-test; a new integration test could round-trip
  one small generation per combo against a fake provider.
- **Voice input** — Deepgram pipeline is ready; the modal would gain a mic
  button that streams to the same `/generate` body via Deepgram → text.
- **Per-field "regenerate this field"** — small refinement on the modal;
  reuses the same provider but with a narrower prompt.

## Adding a new vertical or country

1. Append a row to `regulators.go` keyed `<COUNTRY>:<vertical>`.
2. Drop a `fewshot/form/<vertical>.json` and / or
   `fewshot/policy/<vertical>_<country>.json`.
3. (Optional) Update marketing copy / Flutter UI strings if a new vertical
   needs first-class onboarding support.

That's it. No code changes in this package.
