package salvia_content

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAll_ParsesEntireCatalogue(t *testing.T) {
	t.Parallel()
	ts, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(ts) < 90 {
		t.Fatalf("expected ≥90 templates, got %d", len(ts))
	}

	// Every loaded template carries the constant badge.
	for _, x := range ts {
		if x.Badge != Badge {
			t.Errorf("%s: badge = %q, want %q", x.ID, x.Badge, Badge)
		}
	}
}

func TestLoadAll_AllIDsUnique(t *testing.T) {
	t.Parallel()
	ts, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	seen := map[string]string{}
	for _, x := range ts {
		if prev, ok := seen[x.ID]; ok {
			t.Fatalf("duplicate id %s in %s and %s", x.ID, x.Path, prev)
		}
		seen[x.ID] = x.Path
	}
}

func TestLoadAll_FormsHaveFields_PoliciesHaveClauses(t *testing.T) {
	t.Parallel()
	ts, _ := LoadAll()
	for _, x := range ts {
		switch x.Kind {
		case KindForm:
			if len(x.Fields) == 0 {
				t.Errorf("%s: form has zero fields", x.ID)
			}
		case KindPolicy:
			if len(x.Clauses) == 0 {
				t.Errorf("%s: policy has zero clauses", x.ID)
			}
		default:
			t.Errorf("%s: kind not determined", x.ID)
		}
	}
}

func TestLoadAll_CurrencyDatesParse(t *testing.T) {
	t.Parallel()
	ts, _ := LoadAll()
	for _, x := range ts {
		if x.CurrencyDate().IsZero() {
			t.Errorf("%s: currency_date %q failed to parse", x.ID, x.CurrencyDateRaw)
		}
		// Sanity: not in the future.
		if x.CurrencyDate().After(time.Now().AddDate(0, 0, 1)) {
			t.Errorf("%s: currency_date %s is in the future", x.ID, x.CurrencyDateRaw)
		}
	}
}

func TestLoadAll_VerticalMatchesFolder(t *testing.T) {
	t.Parallel()
	ts, _ := LoadAll()
	for _, x := range ts {
		parts := strings.Split(x.Path, "/")
		if len(parts) > 0 && Vertical(parts[0]) != x.Vertical {
			t.Errorf("%s: vertical %q but file under %q", x.ID, x.Vertical, parts[0])
		}
	}
}

func TestTemplatesFor_VetNZ_IncludesSharedAndVet(t *testing.T) {
	t.Parallel()
	all, _ := LoadAll()
	got := TemplatesFor(all, VerticalVeterinary, CountryNZ)

	hasShared, hasVet := false, false
	for _, x := range got {
		if x.Vertical == VerticalShared {
			hasShared = true
		}
		if x.Vertical == VerticalVeterinary {
			hasVet = true
		}
		if x.Vertical != VerticalShared && x.Vertical != VerticalVeterinary {
			t.Errorf("vet+NZ filter returned other-vertical template %s (vertical=%s)",
				x.ID, x.Vertical)
		}
	}
	if !hasShared {
		t.Error("expected shared templates in vet+NZ result, got none")
	}
	if !hasVet {
		t.Error("expected vet templates in vet+NZ result, got none")
	}
}

func TestTemplatesFor_FiltersByCountry(t *testing.T) {
	t.Parallel()
	all, _ := LoadAll()
	for _, c := range []Country{CountryNZ, CountryAU, CountryUK, CountryUS} {
		got := TemplatesFor(all, VerticalDental, c)
		if len(got) == 0 {
			t.Errorf("dental+%s returned 0 templates", c)
		}
		for _, x := range got {
			if !containsCountry(x.Countries, c) {
				t.Errorf("%s: returned for %s but not in countries %v", x.ID, c, x.Countries)
			}
		}
	}
}

func TestFormsAndPolicies_Splits(t *testing.T) {
	t.Parallel()
	all, _ := LoadAll()
	got := TemplatesFor(all, VerticalAgedCare, CountryAU)
	forms, policies := FormsAndPolicies(got)

	if len(forms) == 0 {
		t.Error("expected aged-care forms, got 0")
	}
	if len(policies) == 0 {
		t.Error("expected aged-care policies, got 0")
	}

	// Every form should be Kind=form and every policy Kind=policy.
	for _, f := range forms {
		if f.Kind != KindForm {
			t.Errorf("forms slice contains non-form: %s (kind=%s)", f.ID, f.Kind)
		}
	}
	for _, p := range policies {
		if p.Kind != KindPolicy {
			t.Errorf("policies slice contains non-policy: %s (kind=%s)", p.ID, p.Kind)
		}
	}
}

func TestRegulatorMap_For(t *testing.T) {
	t.Parallel()
	r := RegulatorMap{NZ: "n", AU: "a", UK: "u", US: "s"}
	if r.For(CountryNZ) != "n" {
		t.Error("NZ")
	}
	if r.For(CountryAU) != "a" {
		t.Error("AU")
	}
	if r.For(CountryUK) != "u" {
		t.Error("UK")
	}
	if r.For(CountryUS) != "s" {
		t.Error("US")
	}
}

func TestClauseSpec_BodyFor_PrefersPerCountry(t *testing.T) {
	t.Parallel()
	c := ClauseSpec{
		ID:    "x",
		Title: "x",
		Body:  "shared",
		BodyPerCountry: map[Country]string{
			CountryNZ: "kiwi",
		},
	}
	if got := c.BodyFor(CountryNZ); got != "kiwi" {
		t.Errorf("NZ: got %q, want kiwi", got)
	}
	if got := c.BodyFor(CountryUK); got != "shared" {
		t.Errorf("UK fallback: got %q, want shared", got)
	}
}

func TestFieldSpec_LabelFor_HonoursOverride(t *testing.T) {
	t.Parallel()
	f := FieldSpec{
		Key:             "k",
		Label:           "default",
		Type:            "text",
		LabelPerCountry: map[Country]string{CountryUS: "us-label"},
	}
	if f.LabelFor(CountryUS) != "us-label" {
		t.Error("US override")
	}
	if f.LabelFor(CountryNZ) != "default" {
		t.Error("NZ fallback")
	}
}
