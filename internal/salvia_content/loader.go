package salvia_content

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// contentFS embeds the YAML library at compile time. The embed pattern
// excludes README, _terms.md, _schema.md, INDEX.md from being parsed as
// templates — those are documentation, kept beside the data so they ship
// alongside but the loader skips them in collectYAML.
//
//go:embed data
var contentFS embed.FS

// LoadAll walks the embedded filesystem and returns every Template it can
// parse. An error is returned only on parse / structural failure — a file
// that doesn't fit either form-shape or policy-shape is treated as a hard
// error so the build fails rather than shipping a silent half-loaded
// catalogue.
//
// The result is stable-ordered by Path for deterministic install / test
// output.
func LoadAll() ([]Template, error) {
	var out []Template
	err := fs.WalkDir(contentFS, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("salvia_content.loader.walk: %w", err)
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml") {
			// Markdown / README files are part of the embed but not
			// parsed as templates.
			return nil
		}
		t, perr := parseTemplate(p)
		if perr != nil {
			return fmt.Errorf("salvia_content.loader.parse: %s: %w", p, perr)
		}
		out = append(out, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MustLoadAll panics on load failure. Use only in tests / package-init
// helpers — production code paths should call LoadAll and surface the error.
func MustLoadAll() []Template {
	ts, err := LoadAll()
	if err != nil {
		panic(err)
	}
	return ts
}

// TemplatesFor returns the subset of all loaded templates that should be
// installed for a clinic of the given (vertical, country).
func TemplatesFor(all []Template, v Vertical, c Country) []Template {
	out := make([]Template, 0, len(all))
	for _, t := range all {
		if t.AppliesTo(v, c) {
			out = append(out, t)
		}
	}
	return out
}

// FormsAndPolicies splits a slice of templates by Kind, in path order.
// Convenience for callers that need the two slices separately (e.g. the
// materialiser invokes the forms service for one and the policy service
// for the other).
func FormsAndPolicies(ts []Template) (forms, policies []Template) {
	for _, t := range ts {
		switch t.Kind {
		case KindForm:
			forms = append(forms, t)
		case KindPolicy:
			policies = append(policies, t)
		}
	}
	return forms, policies
}

// parseTemplate reads one YAML file from the embedded fs and decorates it
// with Path / Kind / default Badge.
func parseTemplate(p string) (Template, error) {
	raw, err := contentFS.ReadFile(p)
	if err != nil {
		return Template{}, fmt.Errorf("salvia_content.loader.read: %w", err)
	}
	var t Template
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return Template{}, fmt.Errorf("salvia_content.loader.unmarshal: %w", err)
	}
	t.Path = strings.TrimPrefix(p, "data/")

	// Discriminate Kind by parent dir. data/<vertical>/<kind>/<file>.yaml
	parts := strings.Split(path.Dir(t.Path), "/")
	if len(parts) >= 2 {
		switch parts[1] {
		case "forms":
			t.Kind = KindForm
		case "policies":
			t.Kind = KindPolicy
		}
	}

	// Default-inject Badge / Disclaimer / Maintainer when the file omits them.
	if t.Badge == "" {
		t.Badge = Badge
	}
	if t.Disclaimer == "" {
		t.Disclaimer = "standard"
	}
	if t.Maintainer == "" {
		t.Maintainer = "Salvia"
	}

	if err := validate(t); err != nil {
		return Template{}, err
	}
	return t, nil
}

// validate enforces the structural rules from data/_schema.md. Run at load
// time so a bad file fails the build, not the runtime.
func validate(t Template) error {
	if t.ID == "" {
		return fmt.Errorf("salvia_content.loader.validate: missing id")
	}
	if !strings.HasPrefix(t.ID, "salvia.") {
		return fmt.Errorf("salvia_content.loader.validate: id %q must start with 'salvia.'", t.ID)
	}
	if t.Name == "" {
		return fmt.Errorf("salvia_content.loader.validate: %s: missing name", t.ID)
	}
	if t.Version <= 0 {
		return fmt.Errorf("salvia_content.loader.validate: %s: version must be >= 1", t.ID)
	}
	if t.CurrencyDateRaw == "" {
		return fmt.Errorf("salvia_content.loader.validate: %s: missing currency_date", t.ID)
	}
	if t.CurrencyDate().IsZero() {
		return fmt.Errorf("salvia_content.loader.validate: %s: currency_date %q is not YYYY-MM-DD",
			t.ID, t.CurrencyDateRaw)
	}
	switch t.Vertical {
	case VerticalShared, VerticalVeterinary, VerticalDental, VerticalGeneralClinic, VerticalAgedCare:
	default:
		return fmt.Errorf("salvia_content.loader.validate: %s: unknown vertical %q", t.ID, t.Vertical)
	}
	if len(t.Countries) == 0 {
		return fmt.Errorf("salvia_content.loader.validate: %s: countries must be non-empty", t.ID)
	}
	for _, c := range t.Countries {
		switch c {
		case CountryNZ, CountryAU, CountryUK, CountryUS:
		default:
			return fmt.Errorf("salvia_content.loader.validate: %s: unknown country %q", t.ID, c)
		}
	}

	// Vertical declared in YAML must match the folder it was loaded from.
	parts := strings.Split(t.Path, "/")
	if len(parts) >= 1 && Vertical(parts[0]) != t.Vertical {
		return fmt.Errorf(
			"salvia_content.loader.validate: %s: vertical=%q but file is under %q",
			t.ID, t.Vertical, parts[0])
	}

	switch t.Kind {
	case KindForm:
		if len(t.Fields) == 0 {
			return fmt.Errorf("salvia_content.loader.validate: %s: form has no fields", t.ID)
		}
		for i, f := range t.Fields {
			if f.Key == "" {
				return fmt.Errorf("salvia_content.loader.validate: %s: field[%d] missing key", t.ID, i)
			}
			if f.Type == "" {
				return fmt.Errorf("salvia_content.loader.validate: %s: field %q missing type", t.ID, f.Key)
			}
		}
	case KindPolicy:
		if len(t.Clauses) == 0 {
			return fmt.Errorf("salvia_content.loader.validate: %s: policy has no clauses", t.ID)
		}
		for i, c := range t.Clauses {
			if c.ID == "" {
				return fmt.Errorf("salvia_content.loader.validate: %s: clause[%d] missing id", t.ID, i)
			}
			if c.Title == "" {
				return fmt.Errorf("salvia_content.loader.validate: %s: clause %q missing title", t.ID, c.ID)
			}
			// body_per_country keys must be subset of Countries
			for k := range c.BodyPerCountry {
				if !containsCountry(t.Countries, k) {
					return fmt.Errorf(
						"salvia_content.loader.validate: %s: clause %q has body_per_country[%q] but %q is not in declared countries",
						t.ID, c.ID, k, k)
				}
			}
		}
	default:
		return fmt.Errorf("salvia_content.loader.validate: %s: kind not determined from path %q", t.ID, t.Path)
	}

	return nil
}

func containsCountry(cs []Country, c Country) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}
