// Package schema is the single source of truth for form field types and per-type
// config validation. Modify here and updates flow to:
//   - the Gemini / OpenAI ResponseSchema enum constraints (aigen package)
//   - the AI-output validator pipeline (aigen package)
//   - DB CHECK constraints if added in future migrations
//
// Adding a new field type without updating ValidateConfig fails the consistency
// test in registry_test.go — that's intentional, it is the schema-evolution gate.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
)

// FieldType is the canonical enum of allowed form field widget types.
// Add a new value here AND a switch arm in ValidateConfig — both are required.
type FieldType string

const (
	FieldTypeText        FieldType = "text"
	FieldTypeLongText    FieldType = "long_text"
	FieldTypeNumber      FieldType = "number"
	FieldTypeDecimal     FieldType = "decimal"
	FieldTypeSlider      FieldType = "slider"
	FieldTypeSelect      FieldType = "select"
	FieldTypeButtonGroup FieldType = "button_group"
	FieldTypePercentage  FieldType = "percentage"
	FieldTypeBlocks      FieldType = "blocks"
	FieldTypeImage       FieldType = "image"
	FieldTypeDate        FieldType = "date"

	// ── System widgets ──────────────────────────────────────────────────
	// Typed compliance fields — AI emits structured JSON for each, and the
	// note-submit pipeline materialises them as real ledger rows
	// (consent_records, drug_operations_log, incident_events,
	// pain_scores) instead of leaving them as free-text in
	// note_fields.value. See SYSTEM_WIDGETS.md for the rationale.
	FieldTypeSystemConsent   FieldType = "system.consent"
	FieldTypeSystemDrugOp    FieldType = "system.drug_op"
	FieldTypeSystemIncident  FieldType = "system.incident"
	FieldTypeSystemPainScore FieldType = "system.pain_score"
)

// AllFieldTypes returns the canonical whitelist. Order is stable so it can be
// fed to OpenAPI / Gemini schemas as enum values.
func AllFieldTypes() []FieldType {
	return []FieldType{
		FieldTypeText, FieldTypeLongText, FieldTypeNumber, FieldTypeDecimal,
		FieldTypeSlider, FieldTypeSelect, FieldTypeButtonGroup, FieldTypePercentage,
		FieldTypeBlocks, FieldTypeImage, FieldTypeDate,
		FieldTypeSystemConsent, FieldTypeSystemDrugOp,
		FieldTypeSystemIncident, FieldTypeSystemPainScore,
	}
}

// IsSystemFieldType reports whether the type is a typed compliance widget
// (consent / drug op / incident / pain score). Used by the note-submit
// pipeline to gate the entity-creation side-effect.
func IsSystemFieldType(t FieldType) bool {
	switch t {
	case FieldTypeSystemConsent, FieldTypeSystemDrugOp,
		FieldTypeSystemIncident, FieldTypeSystemPainScore:
		return true
	}
	return false
}

// IsValidFieldType reports whether s is one of the canonical field types.
func IsValidFieldType(s string) bool {
	for _, t := range AllFieldTypes() {
		if string(t) == s {
			return true
		}
	}
	return false
}

// FieldTypeEnumValues returns the canonical type list as []string for use in
// OpenAPI / provider response-schema enum constraints.
func FieldTypeEnumValues() []string {
	types := AllFieldTypes()
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = string(t)
	}
	return out
}

// SelectConfig is the canonical config shape for select/button_group fields.
type SelectConfig struct {
	Options []SelectOption `json:"options"`
}

// SelectOption is one entry in a SelectConfig.
type SelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SliderConfig is the canonical config shape for slider/percentage fields.
type SliderConfig struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Step float64 `json:"step,omitempty"`
}

// SystemConsentConfig — the form builder pins the consent type so the AI
// extractor can fill the right template. RequireWitness can override the
// per-type default from the consent module.
type SystemConsentConfig struct {
	// One of consent.validConsentType (audio_recording, ai_processing,
	// telemedicine, sedation, euthanasia, invasive_procedure, mhr_write,
	// photography, data_sharing, controlled_drug_administration,
	// treatment_plan, other). Empty = AI infers from context.
	ConsentType string `json:"consent_type,omitempty"`

	// Forces the witness picker to be filled regardless of the per-type
	// default. Use for jurisdictions where any consent capture must be
	// witnessed (e.g. some aged-care facility policies).
	RequireWitness bool `json:"require_witness,omitempty"`
}

// SystemDrugOpConfig — pins the operation kind + whether the widget is
// only for controlled drugs. ConfirmRequired (default true) gates submit
// behind an explicit clinician confirm even when AI pre-fills the row.
type SystemDrugOpConfig struct {
	// One of drugs.validOperation (administer, dispense, discard,
	// receive, transfer, adjust). Empty = AI infers.
	Operation string `json:"operation,omitempty"`

	// When true, only catalog entries with controls=true are pickable.
	ControlledOnly bool `json:"controlled_only,omitempty"`

	// When true (default), the row stays in pending_confirm until a
	// clinician taps Confirm; backend rejects note submission while any
	// drug op for that note is unconfirmed. Set false ONLY for
	// non-regulator-binding ops (e.g. internal stock-take) — never for
	// administer / dispense.
	ConfirmRequired *bool `json:"confirm_required,omitempty"`
}

// ConfirmRequiredOrDefault — true unless explicitly disabled.
func (c SystemDrugOpConfig) ConfirmRequiredOrDefault() bool {
	if c.ConfirmRequired == nil {
		return true
	}
	return *c.ConfirmRequired
}

// SystemIncidentConfig — pins the incident type so the classifier
// pre-routes (SIRS / CQC / VCNZ / HDC) consistently. AutoClassify lets
// AI choose; pinning is for forms that ONLY capture one kind (e.g. a
// fall-specific form on an aged-care daily round).
type SystemIncidentConfig struct {
	// One of incidents.validIncidentType. Empty = AI infers.
	IncidentType string `json:"incident_type,omitempty"`

	// Minimum severity floor — AI extracts severity from transcript but
	// the captured row is bumped to this if AI's value is lower. Use for
	// incident-type forms where any logged event has at least N severity.
	MinSeverity string `json:"min_severity,omitempty"`
}

// SystemPainScoreConfig — pins the scale so the picker is locked. Empty
// means AI picks per subject (vet → CMPS-SF, aged-care → PainAD, …).
type SystemPainScoreConfig struct {
	// One of pain.validScale (nrs, flacc, painad, wong_baker, vrs, vas).
	ScaleID string `json:"scale_id,omitempty"`
}

// Sentinel validation errors. Callers can use errors.Is() to branch.
var (
	ErrUnknownFieldType    = errors.New("schema: unknown field type")
	ErrSelectMissingOpts   = errors.New("schema: select/button_group requires at least 2 well-formed options")
	ErrSliderInvalidRange  = errors.New("schema: slider requires min < max")
	ErrInvalidConfigJSON   = errors.New("schema: config is not valid JSON")
	ErrInvalidSystemConfig = errors.New("schema: invalid system widget config")
)

// ValidateConfig checks that the per-type config JSON is well-formed for the
// given field type. Empty/null config is allowed for types where config is
// optional (text, long_text, number, decimal, percentage, blocks, image, date).
//
// This is the second of the schema-safety layers — provider-enforced JSON
// schema is the first; DB constraints are the last.
//
// IMPORTANT: every value of FieldType must be handled here. The consistency
// test in registry_test.go fails the build otherwise.
func ValidateConfig(t FieldType, raw json.RawMessage) error {
	if !IsValidFieldType(string(t)) {
		return fmt.Errorf("%w: %s", ErrUnknownFieldType, t)
	}
	switch t {
	case FieldTypeSelect, FieldTypeButtonGroup:
		if len(raw) == 0 {
			return ErrSelectMissingOpts
		}
		var c SelectConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		if len(c.Options) < 2 {
			return ErrSelectMissingOpts
		}
		for i, o := range c.Options {
			if o.Label == "" || o.Value == "" {
				return fmt.Errorf("%w: option[%d] missing label or value", ErrSelectMissingOpts, i)
			}
		}
		return nil

	case FieldTypeSlider:
		if len(raw) == 0 {
			return ErrSliderInvalidRange
		}
		var c SliderConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		if c.Min >= c.Max {
			return ErrSliderInvalidRange
		}
		return nil

	case FieldTypeText, FieldTypeLongText, FieldTypeNumber, FieldTypeDecimal,
		FieldTypePercentage, FieldTypeBlocks, FieldTypeImage, FieldTypeDate:
		if len(raw) == 0 {
			return nil
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		return nil

	case FieldTypeSystemConsent:
		if len(raw) == 0 {
			return nil
		}
		var c SystemConsentConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		if c.ConsentType != "" && !validSystemConsentType(c.ConsentType) {
			return fmt.Errorf("%w: invalid consent_type %q", ErrInvalidSystemConfig, c.ConsentType)
		}
		return nil

	case FieldTypeSystemDrugOp:
		if len(raw) == 0 {
			return nil
		}
		var c SystemDrugOpConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		if c.Operation != "" && !validSystemDrugOperation(c.Operation) {
			return fmt.Errorf("%w: invalid operation %q", ErrInvalidSystemConfig, c.Operation)
		}
		// confirm_required defaults to true. Refuse explicit-false when
		// the operation is administer/dispense — those are
		// regulator-binding and the gate is mandatory.
		if c.ConfirmRequired != nil && !*c.ConfirmRequired {
			if c.Operation == "administer" || c.Operation == "dispense" {
				return fmt.Errorf("%w: confirm_required cannot be disabled for administer/dispense", ErrInvalidSystemConfig)
			}
		}
		return nil

	case FieldTypeSystemIncident:
		if len(raw) == 0 {
			return nil
		}
		var c SystemIncidentConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		if c.MinSeverity != "" && !validSystemIncidentSeverity(c.MinSeverity) {
			return fmt.Errorf("%w: invalid min_severity %q", ErrInvalidSystemConfig, c.MinSeverity)
		}
		return nil

	case FieldTypeSystemPainScore:
		if len(raw) == 0 {
			return nil
		}
		var c SystemPainScoreConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfigJSON, err)
		}
		if c.ScaleID != "" && !validSystemPainScale(c.ScaleID) {
			return fmt.Errorf("%w: invalid scale_id %q", ErrInvalidSystemConfig, c.ScaleID)
		}
		return nil
	}
	return fmt.Errorf("%w: %s (no validator branch)", ErrUnknownFieldType, t)
}

// ── System widget enum mirrors ───────────────────────────────────────────
// These mirror the enums in the consent / drugs / incidents / pain modules.
// We can't import those packages from here (would create a cycle), so the
// allowlist is duplicated. registry_test.go cross-checks the values match
// the source-of-truth enums in the respective modules.

func validSystemConsentType(t string) bool {
	switch t {
	case "audio_recording", "ai_processing", "telemedicine",
		"sedation", "euthanasia", "invasive_procedure",
		"mhr_write", "photography", "data_sharing",
		"controlled_drug_administration", "treatment_plan", "other":
		return true
	}
	return false
}

func validSystemDrugOperation(op string) bool {
	switch op {
	case "administer", "dispense", "discard", "receive", "transfer", "adjust":
		return true
	}
	return false
}

func validSystemIncidentSeverity(s string) bool {
	switch s {
	case "low", "medium", "high", "critical":
		return true
	}
	return false
}

func validSystemPainScale(s string) bool {
	switch s {
	case "nrs", "flacc", "painad", "wong_baker", "vrs", "vas":
		return true
	}
	return false
}
