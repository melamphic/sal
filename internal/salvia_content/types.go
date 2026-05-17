// Package salvia_content holds the canonical Salvia-authored forms and
// policies that ship into every clinic at signup. The YAML content under
// data/ is embedded at compile time; this package exposes a typed view of
// that content plus a loader that filters by (vertical, country) for
// materialisation into a tenant clinic.
//
// Disclaimer text and the badge string travel with the package — see
// data/_terms.md and the constant Badge below. Both surface in the UI and
// in PDF footers via the existing doc-theme system.
package salvia_content

import "time"

// Badge is the constant string shown on every Salvia-provided template in
// the UI and on the corresponding PDF footer. Captured here (not in a YAML
// file) so it cannot drift across templates.
const Badge = "Made by Salvia v1"

// Kind discriminates a template into form vs policy. Matches the folder
// path it was loaded from (data/<vertical>/<kind>/*.yaml).
type Kind string

const (
	KindForm   Kind = "form"
	KindPolicy Kind = "policy"
)

// State mirrors forms.salvia_template_state / policies.salvia_template_state
// — the per-clinic lifecycle of an installed template. The materialiser
// writes "default" on initial install; the forms/policy services flip to
// "forked" on first edit and "deleted" on explicit removal.
type State string

const (
	StateDefault State = "default"
	StateForked  State = "forked"
	StateDeleted State = "deleted"
)

// Vertical mirrors domain.Vertical. Re-declared here as a string so this
// package has no dependency on the domain package and can be loaded by
// tooling that doesn't pull in the full app.
type Vertical string

const (
	VerticalShared        Vertical = "shared"
	VerticalVeterinary    Vertical = "veterinary"
	VerticalDental        Vertical = "dental"
	VerticalGeneralClinic Vertical = "general_clinic"
	VerticalAgedCare      Vertical = "aged_care"
)

// Country is the ISO-3166 alpha-2 code (NZ, AU, UK, US). UK is the
// non-standard alias for GB used throughout the Salvia codebase — kept
// here for consistency with clinic.country.
type Country string

const (
	CountryNZ Country = "NZ"
	CountryAU Country = "AU"
	CountryUK Country = "UK"
	CountryUS Country = "US"
)

// Template is the loaded view of one YAML file. Forms have non-empty Fields;
// Policies have non-empty Clauses. The Kind discriminates which is populated.
//
// Path is the relative path inside the embedded fs (e.g.
// "shared/forms/consultation_note.yaml"). Useful for error reporting and
// for round-tripping back to git for audit/review.
type Template struct {
	ID                   string       `yaml:"id"`
	Name                 string       `yaml:"name"`
	Version              int          `yaml:"version"`
	CurrencyDateRaw      string       `yaml:"currency_date"`
	Vertical             Vertical     `yaml:"vertical"`
	Countries            []Country    `yaml:"countries"`
	Description          string       `yaml:"description,omitempty"`
	PurposePerRegulator  RegulatorMap `yaml:"purpose_per_regulator,omitempty"`
	LinkedPolicyDefaults []string     `yaml:"linked_policy_defaults,omitempty"`
	OverallPrompt        string       `yaml:"overall_prompt,omitempty"`
	Tags                 []string     `yaml:"tags,omitempty"`

	// Form-only.
	Fields []FieldSpec `yaml:"fields,omitempty"`
	// Policy-only.
	Clauses []ClauseSpec `yaml:"clauses,omitempty"`

	// Optional schema-injected fields. The loader populates Badge and
	// Disclaimer from constants if absent on the file. Files MAY include
	// them explicitly for clarity but it is not required.
	Badge      string `yaml:"badge,omitempty"`
	Disclaimer string `yaml:"disclaimer,omitempty"`
	Maintainer string `yaml:"maintainer,omitempty"`

	// Populated by the loader from the embedded path. Not in YAML.
	Path string `yaml:"-"`
	Kind Kind   `yaml:"-"`
}

// CurrencyDate parses CurrencyDateRaw into a time.Time. Returns the zero
// value if the raw string is empty or malformed.
func (t Template) CurrencyDate() time.Time {
	d, err := time.Parse("2006-01-02", t.CurrencyDateRaw)
	if err != nil {
		return time.Time{}
	}
	return d
}

// AppliesTo reports whether this template should be installed for a clinic
// of the given (vertical, country). A shared template installs everywhere
// its country list matches.
func (t Template) AppliesTo(v Vertical, c Country) bool {
	if t.Vertical != VerticalShared && t.Vertical != v {
		return false
	}
	for _, x := range t.Countries {
		if x == c {
			return true
		}
	}
	return false
}

// RegulatorMap is a per-country block. Generic enough to back
// PurposePerRegulator and any future per-country textual map.
type RegulatorMap struct {
	NZ string `yaml:"NZ,omitempty"`
	AU string `yaml:"AU,omitempty"`
	UK string `yaml:"UK,omitempty"`
	US string `yaml:"US,omitempty"`
}

// For returns the value for the supplied country, or "" if absent.
func (r RegulatorMap) For(c Country) string {
	switch c {
	case CountryNZ:
		return r.NZ
	case CountryAU:
		return r.AU
	case CountryUK:
		return r.UK
	case CountryUS:
		return r.US
	}
	return ""
}

// FieldSpec mirrors a single entry under `fields:` in a form YAML. Type is
// the schema enum from forms/schema/registry.go (text, long_text, system.consent,
// etc.) plus the special "system_field" variant for patient-record-pulled
// fields.
type FieldSpec struct {
	Key              string                  `yaml:"key"`
	Label            string                  `yaml:"label"`
	Type             string                  `yaml:"type"`
	Required         bool                    `yaml:"required,omitempty"`
	AIExtract        bool                    `yaml:"ai_extract,omitempty"`
	PII              bool                    `yaml:"pii,omitempty"`
	PHI              bool                    `yaml:"phi,omitempty"`
	HelpText         string                  `yaml:"help_text,omitempty"`
	Source           string                  `yaml:"source,omitempty"` // for system_field
	Config           map[string]any          `yaml:"config,omitempty"`
	Countries        []Country               `yaml:"countries,omitempty"`
	LabelPerCountry  map[Country]string      `yaml:"label_per_country,omitempty"`
	RequiredCountry  map[Country]bool        `yaml:"required_per_country,omitempty"`
}

// AppliesToCountry mirrors Template.AppliesTo for an individual field that
// is country-scoped. Empty Countries = applies to every country the form
// applies to.
func (f FieldSpec) AppliesToCountry(c Country) bool {
	if len(f.Countries) == 0 {
		return true
	}
	for _, x := range f.Countries {
		if x == c {
			return true
		}
	}
	return false
}

// LabelFor returns the country-specific label override if present,
// otherwise the default Label.
func (f FieldSpec) LabelFor(c Country) string {
	if v, ok := f.LabelPerCountry[c]; ok {
		return v
	}
	return f.Label
}

// IsRequiredFor honours required_per_country overrides.
func (f FieldSpec) IsRequiredFor(c Country) bool {
	if v, ok := f.RequiredCountry[c]; ok {
		return v
	}
	return f.Required
}

// ClauseSpec mirrors a single entry under `clauses:` in a policy YAML.
// Body and BodyPerCountry are mutually exclusive at use-time: BodyFor()
// prefers per-country, falling back to Body.
type ClauseSpec struct {
	ID             string             `yaml:"id"`
	Title          string             `yaml:"title"`
	Body           string             `yaml:"body,omitempty"`
	BodyPerCountry map[Country]string `yaml:"body_per_country,omitempty"`
	// Parity drives policy-check scoring and the overlay parity badge.
	// Accepted values: "high" (must-comply), "medium" (should), "low" (guidance).
	// Omit to default to "high".
	Parity string `yaml:"parity,omitempty"`
}

// BodyFor returns the country-specific body if present, falling back to
// the shared Body. Empty result is allowed (some clauses are intentionally
// title-only as a header).
func (c ClauseSpec) BodyFor(country Country) string {
	if v, ok := c.BodyPerCountry[country]; ok {
		return v
	}
	return c.Body
}
