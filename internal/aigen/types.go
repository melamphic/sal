// Package aigen provides AI-powered generation of clinical forms and policies.
//
// Input is a free-text user description; output is a draft form_version or
// policy_version that the user reviews and edits in the existing manual editors
// before publishing. This package never auto-publishes anything.
//
// Design principles:
//
//  1. Vertical and country are RUNTIME context, not code paths. They are read
//     from the clinic record and passed into prompt templates + regulator
//     lookup. There are no `if vertical == "vet"` branches anywhere here.
//     Adding a new vertical/country = drop a fewshot data file and a row in
//     regulators.go.
//
//  2. clinic.country is locked at registration. This package NEVER accepts
//     country in a request body — it's read from the clinic record.
//
//  3. Schema safety is enforced in 8 layers (see README.md). The whole module
//     assumes broken AI output is normal and validates / repairs / retries
//     before persisting anything.
//
//  4. Provider is pluggable: dev runs Gemini (free tier), prod runs OpenAI.
//     New AI features must implement both providers — never ship a
//     Gemini-only feature.
//
//  5. AI output is ALWAYS marked "AI drafted". Human-in-loop publishes.
package aigen

import (
	"encoding/json"
	"time"
)

// FormGenInput is the user-supplied input for a form-generation request.
//
// Country and primary vertical are NOT accepted here — they are read from the
// clinic record because clinic.country is locked at registration. A per-form
// vertical override is allowed for clinics that operate multiple verticals
// (rare; e.g. a mixed-practice that handles vet + general clinical).
type FormGenInput struct {
	// Description is the user's free-text request — what protocol/encounter
	// the form captures, what fields they want, etc.
	Description string

	// GroupID optionally places the new form into an existing folder.
	GroupID *string

	// UseMarketplace pulls 3 marketplace examples from the clinic's vertical
	// into the prompt as additional few-shot context. Defaults true.
	UseMarketplace bool

	// OverrideVertical overrides clinic.vertical for this generation only.
	// Allowed values are the same vertical strings used elsewhere
	// (vet, dental, general, aged_care). Use sparingly.
	OverrideVertical *string
}

// PolicyGenInput is the user-supplied input for a policy-generation request.
type PolicyGenInput struct {
	// Description is the user's free-text request — what compliance area
	// the policy covers, what clauses they need, etc.
	Description string

	// FolderID optionally places the new policy into an existing folder.
	FolderID *string

	// UseMarketplace pulls 3 marketplace examples (same vertical+country)
	// into the prompt as few-shot context. Defaults true.
	UseMarketplace bool

	// OverrideVertical — same semantics as FormGenInput.OverrideVertical.
	OverrideVertical *string
}

// GeneratedForm is the AI's structured output for form generation. Matches the
// shape consumed by forms.Service when persisting an AI-drafted form.
type GeneratedForm struct {
	Name          string           `json:"name"`
	Description   string           `json:"description,omitempty"`
	OverallPrompt string           `json:"overall_prompt,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	Fields        []GeneratedField `json:"fields"`
}

// GeneratedField mirrors the subset of forms.FieldResponse relevant to creation.
// Type must be one of forms/schema.AllFieldTypes; Config is validated per type.
type GeneratedField struct {
	Position       int             `json:"position"`
	Title          string          `json:"title"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config,omitempty"`
	AIPrompt       string          `json:"ai_prompt,omitempty"`
	Required       bool            `json:"required,omitempty"`
	Skippable      bool            `json:"skippable,omitempty"`
	AllowInference bool            `json:"allow_inference,omitempty"`
}

// GeneratedPolicy is the AI's structured output for policy generation.
type GeneratedPolicy struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Content     json.RawMessage   `json:"content"` // AppFlowy-compatible block array
	Clauses     []GeneratedClause `json:"clauses"`
}

// GeneratedClause mirrors the subset of policy.PolicyClauseResponse relevant
// to creation. BlockID must reference a block that appears in Content.
type GeneratedClause struct {
	BlockID string `json:"block_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Parity  string `json:"parity"` // policy/schema.Parity*

	// SourceCitation is an AI-suggested verbatim regulator quote backing the
	// clause. The UI MUST display this as "AI-suggested citation, verify
	// against [regulator]" — it is NEVER auto-trusted as authoritative.
	SourceCitation *string `json:"source_citation,omitempty"`
}

// GeneratedConsentDraft is the AI's structured output for consent-draft
// generation: a risks-discussed paragraph and an alternatives-discussed
// paragraph. The clinician reviews and edits before saving — Salvia never
// auto-publishes consent text. Mirrors the consent_records.risks_discussed
// and alternatives_discussed columns one-for-one.
type GeneratedConsentDraft struct {
	RisksDiscussed        string `json:"risks_discussed"`
	AlternativesDiscussed string `json:"alternatives_discussed"`
}

// GeneratedIncidentDraft is the AI's structured output for incident-draft
// generation: typed fields harvested from a free-text account or audio
// transcript. The reporting clinician reviews + edits before submitting —
// SIRS / CQC classification still happens server-side based on the final
// values, so AI suggestions never bypass the regulator-decision logic.
type GeneratedIncidentDraft struct {
	IncidentType     string  `json:"incident_type"`     // CHECK constraint values
	Severity         string  `json:"severity"`          // low/medium/high/critical
	BriefDescription string  `json:"brief_description"`
	ImmediateActions string  `json:"immediate_actions,omitempty"`
	WitnessesText    string  `json:"witnesses_text,omitempty"`
	SubjectOutcome   string  `json:"subject_outcome,omitempty"`
	Location         string  `json:"location,omitempty"`
}

// AIMetadata is persisted on form_versions.generation_metadata /
// policy_versions.generation_metadata (JSONB columns). The Flutter editor
// reads it to render the "AI drafted" badge; the audit log captures it for
// compliance traceability.
type AIMetadata struct {
	Source             string    `json:"source"` // "ai_generated"
	Provider           string    `json:"provider"`
	Model              string    `json:"model"`
	PromptHash         string    `json:"prompt_hash"`
	GeneratedByStaffID string    `json:"generated_by_staff_id"`
	GeneratedAt        time.Time `json:"generated_at"`
	RepairsApplied     int       `json:"repairs_applied,omitempty"`
	RetryCount         int       `json:"retry_count,omitempty"`
}

// ClinicContext is the slice of clinic state needed to render prompts.
// The aigen package does not query the DB directly — callers populate this
// from existing services (clinic.Service, etc.).
type ClinicContext struct {
	ClinicID string
	Vertical string // primary vertical (vet | dental | general | aged_care)
	Country  string // ISO-3166-1 alpha-2 (NZ | AU | UK | US | …)
	Tier     string // billing tier — drives field budget
}

// PromptContext is the data passed to the prompt template engine.
type PromptContext struct {
	Clinic               ClinicContext
	Vertical             string // effective vertical (override or clinic default)
	Regulator            Regulator
	UserAsk              string
	FewShotExamples      []json.RawMessage
	ReferenceClinicForms []ReferenceForm
	ReferenceMarketplace []ReferenceForm
	FieldBudget          FieldBudget
}

// ReferenceForm is a lightweight summary used in prompt context to anchor
// stylistic / tonal choices without leaking entire form definitions.
type ReferenceForm struct {
	Name       string
	FieldCount int
	Tags       []string
}

// FieldBudget caps form size by tier so a generated form is appropriately
// scoped to what the clinic is likely to want / pay for.
type FieldBudget struct {
	Min int
	Max int
}

// DefaultFieldBudget returns a sensible budget keyed off tier name. Unknown
// tiers fall through to a conservative default. Adjust as pricing evolves.
func DefaultFieldBudget(tier string) FieldBudget {
	switch tier {
	case "pro", "Pro", "PRO":
		return FieldBudget{Min: 10, Max: 18}
	case "practice", "Practice", "PRACTICE":
		return FieldBudget{Min: 8, Max: 14}
	default:
		return FieldBudget{Min: 6, Max: 12}
	}
}
