package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestAllFieldTypes_HasValidatorBranch is the schema-evolution gate. It loops
// every canonical FieldType and confirms ValidateConfig has a switch arm for
// it (anything else returns ErrUnknownFieldType, which fails the test).
//
// This is what keeps the registry honest: add a new FieldType without a
// ValidateConfig branch and CI fails before merge.
func TestAllFieldTypes_HasValidatorBranch(t *testing.T) {
	t.Parallel()
	for _, ft := range AllFieldTypes() {
		ft := ft
		t.Run(string(ft), func(t *testing.T) {
			t.Parallel()
			err := ValidateConfig(ft, nil)
			// Domain-specific errors are fine (select needs options, slider needs range).
			// Unknown-type error is NOT fine — that means the type isn't handled.
			if err != nil && errors.Is(err, ErrUnknownFieldType) {
				t.Fatalf("ValidateConfig has no branch for FieldType %q", ft)
			}
		})
	}
}

func TestIsValidFieldType_Whitelist(t *testing.T) {
	t.Parallel()
	for _, ft := range AllFieldTypes() {
		if !IsValidFieldType(string(ft)) {
			t.Errorf("AllFieldTypes lists %q but IsValidFieldType rejects it", ft)
		}
	}
	if IsValidFieldType("checkbox") {
		t.Error("IsValidFieldType should reject unknown types")
	}
}

func TestFieldTypeEnumValues_MatchesAllFieldTypes(t *testing.T) {
	t.Parallel()
	if got, want := len(FieldTypeEnumValues()), len(AllFieldTypes()); got != want {
		t.Fatalf("enum drift: %d enum values vs %d FieldType constants", got, want)
	}
}

func TestValidateConfig_Select(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		config  string
		wantErr error
	}{
		{"valid_two", `{"options":[{"label":"A","value":"a"},{"label":"B","value":"b"}]}`, nil},
		{"too_few", `{"options":[{"label":"A","value":"a"}]}`, ErrSelectMissingOpts},
		{"missing_label", `{"options":[{"label":"","value":"a"},{"label":"B","value":"b"}]}`, ErrSelectMissingOpts},
		{"missing_value", `{"options":[{"label":"A","value":""},{"label":"B","value":"b"}]}`, ErrSelectMissingOpts},
		{"empty_object", `{}`, ErrSelectMissingOpts},
		{"nil", ``, ErrSelectMissingOpts},
		{"bad_json", `{"options":[`, ErrInvalidConfigJSON},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var raw json.RawMessage
			if tc.config != "" {
				raw = json.RawMessage(tc.config)
			}
			err := ValidateConfig(FieldTypeSelect, raw)
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("got err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got err=%v, want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConfig_Slider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		config  string
		wantErr error
	}{
		{"valid", `{"min":0,"max":10,"step":1}`, nil},
		{"min_eq_max", `{"min":5,"max":5}`, ErrSliderInvalidRange},
		{"min_gt_max", `{"min":10,"max":5}`, ErrSliderInvalidRange},
		{"empty", ``, ErrSliderInvalidRange},
		{"bad_json", `{"min":}`, ErrInvalidConfigJSON},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var raw json.RawMessage
			if tc.config != "" {
				raw = json.RawMessage(tc.config)
			}
			err := ValidateConfig(FieldTypeSlider, raw)
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("got err=%v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got err=%v, want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConfig_OptionalConfig(t *testing.T) {
	t.Parallel()
	for _, ft := range []FieldType{
		FieldTypeText, FieldTypeLongText, FieldTypeNumber, FieldTypeDecimal,
		FieldTypePercentage, FieldTypeBlocks, FieldTypeImage, FieldTypeDate,
	} {
		ft := ft
		t.Run(string(ft), func(t *testing.T) {
			t.Parallel()
			if err := ValidateConfig(ft, nil); err != nil {
				t.Errorf("nil config rejected: %v", err)
			}
			if err := ValidateConfig(ft, json.RawMessage(`{"placeholder":"foo"}`)); err != nil {
				t.Errorf("free-form config rejected: %v", err)
			}
			if err := ValidateConfig(ft, json.RawMessage(`{`)); err == nil {
				t.Error("malformed JSON should be rejected")
			} else if !errors.Is(err, ErrInvalidConfigJSON) {
				t.Errorf("got err=%v, want ErrInvalidConfigJSON", err)
			}
		})
	}
}

func TestValidateConfig_UnknownType(t *testing.T) {
	t.Parallel()
	err := ValidateConfig(FieldType("checkbox"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown field type") {
		t.Errorf("expected unknown-field-type error, got %v", err)
	}
}
