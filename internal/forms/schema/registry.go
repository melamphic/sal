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
)

// AllFieldTypes returns the canonical whitelist. Order is stable so it can be
// fed to OpenAPI / Gemini schemas as enum values.
func AllFieldTypes() []FieldType {
	return []FieldType{
		FieldTypeText, FieldTypeLongText, FieldTypeNumber, FieldTypeDecimal,
		FieldTypeSlider, FieldTypeSelect, FieldTypeButtonGroup, FieldTypePercentage,
		FieldTypeBlocks, FieldTypeImage, FieldTypeDate,
	}
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

// Sentinel validation errors. Callers can use errors.Is() to branch.
var (
	ErrUnknownFieldType   = errors.New("schema: unknown field type")
	ErrSelectMissingOpts  = errors.New("schema: select/button_group requires at least 2 well-formed options")
	ErrSliderInvalidRange = errors.New("schema: slider requires min < max")
	ErrInvalidConfigJSON  = errors.New("schema: config is not valid JSON")
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
	}
	return fmt.Errorf("%w: %s (no validator branch)", ErrUnknownFieldType, t)
}
