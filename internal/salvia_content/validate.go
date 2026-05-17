package salvia_content

import (
	"fmt"
	"strings"
)

// ValidationError is a collected list of problems found in a single template.
type ValidationError struct {
	Path   string
	Errors []string
}

func (v *ValidationError) Error() string {
	return fmt.Sprintf("%s: %d error(s): %s", v.Path, len(v.Errors), strings.Join(v.Errors, "; "))
}

func (v *ValidationError) add(format string, a ...any) {
	v.Errors = append(v.Errors, fmt.Sprintf(format, a...))
}

// ValidateForm checks a form Template for structural and content rule violations.
// Returns nil when the form is clean.
func ValidateForm(t Template) error {
	ve := &ValidationError{Path: t.Path}

	if t.ID == "" {
		ve.add("id is empty")
	}
	if t.Name == "" {
		ve.add("name is empty")
	}
	if strings.TrimSpace(t.OverallPrompt) == "" {
		ve.add("overall_prompt is missing — AI scribe has no extraction context")
	}

	// Duplicate field key detection.
	seen := make(map[string]bool, len(t.Fields))
	for _, f := range t.Fields {
		if f.Key == "" {
			ve.add("field with label %q has empty key", f.Label)
			continue
		}
		if seen[f.Key] {
			ve.add("duplicate field key %q", f.Key)
		}
		seen[f.Key] = true

		// Banned fields — Salvia covers these via digital auth or patient record.
		if isBannedField(f.Key) {
			ve.add("field %q is banned (Salvia handles this via digital signature or system fields)", f.Key)
		}

		// Type must be known.
		if !isKnownFieldType(f.Type) {
			ve.add("field %q has unknown type %q", f.Key, f.Type)
		}

		// select / button_group must carry options.
		if (f.Type == "select" || f.Type == "button_group") && len(optionsFromConfig(f.Config)) == 0 {
			ve.add("field %q (type %s) has no options in config", f.Key, f.Type)
		}
	}

	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

// ValidatePolicy checks a policy Template for structural and content rule violations.
// Returns nil when the policy is clean.
func ValidatePolicy(t Template) error {
	ve := &ValidationError{Path: t.Path}

	if t.ID == "" {
		ve.add("id is empty")
	}
	if t.Name == "" {
		ve.add("name is empty")
	}
	if len(t.Clauses) == 0 {
		ve.add("policy has no clauses")
	}

	seen := make(map[string]bool, len(t.Clauses))
	for _, c := range t.Clauses {
		if c.ID == "" {
			ve.add("clause with title %q has empty id", c.Title)
			continue
		}
		if seen[c.ID] {
			ve.add("duplicate clause id %q", c.ID)
		}
		seen[c.ID] = true

		// Each clause must have content — either a shared body or per-country bodies.
		if strings.TrimSpace(c.Body) == "" && len(c.BodyPerCountry) == 0 {
			ve.add("clause %q has no body and no body_per_country", c.ID)
		}

		// Parity must be a known value when set.
		if c.Parity != "" && c.Parity != "high" && c.Parity != "medium" && c.Parity != "low" {
			ve.add("clause %q has invalid parity %q (must be high, medium, or low)", c.ID, c.Parity)
		}
	}

	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

// ValidateAll loads every embedded template and validates each one.
// Returns a slice of ValidationErrors — one per broken template.
func ValidateAll() ([]*ValidationError, error) {
	templates, err := LoadAll()
	if err != nil {
		return nil, fmt.Errorf("salvia_content.ValidateAll: load: %w", err)
	}
	var errs []*ValidationError
	for _, t := range templates {
		var err error
		switch t.Kind {
		case KindForm:
			err = ValidateForm(t)
		case KindPolicy:
			err = ValidatePolicy(t)
		}
		if err != nil {
			if ve, ok := err.(*ValidationError); ok {
				errs = append(errs, ve)
			}
		}
	}
	return errs, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

var bannedFieldKeys = map[string]bool{
	"signature_clinician":  true,
	"signature_date":       true,
	"signature_prescriber": true,
	"signature_patient":    true,
	"clinician_name":       true,
	"vet_name":             true,
	"veterinarian_name":    true,
	"entry_datetime":       true, // contemporaneous forms use Salvia submit timestamp
	"documentation_date":   true,
}

func isBannedField(key string) bool {
	return bannedFieldKeys[strings.ToLower(key)]
}

var knownFieldTypes = map[string]bool{
	"text":               true,
	"long_text":          true,
	"number":             true,
	"decimal":            true,
	"boolean":            true,
	"date":               true,
	"datetime":           true,
	"select":             true,
	"multiselect":        true,
	"button_group":       true,
	"slider":             true,
	"blocks":             true,
	"system_field":       true,
	"system.consent":     true,
	"system.drug_op":     true,
	"system.incident":    true,
	"system.pain_score":  true,
}

func isKnownFieldType(t string) bool {
	return knownFieldTypes[t]
}

func optionsFromConfig(cfg map[string]any) []any {
	if cfg == nil {
		return nil
	}
	v, ok := cfg["options"]
	if !ok {
		return nil
	}
	opts, _ := v.([]any)
	return opts
}
