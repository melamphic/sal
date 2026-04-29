package catalog

import (
	"strings"
	"testing"
)

// TestNewLoader_ParsesAllShippedCatalogs guards against any regressions
// in the embedded JSON files (malformed, duplicate IDs, missing required
// fields). Runs at every CI build — if any catalog drift breaks the
// schema, this fails before deploy.
func TestNewLoader_ParsesAllShippedCatalogs(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	manifest := l.Manifest()
	if len(manifest) == 0 {
		t.Fatalf("expected at least one manifest entry; got 0")
	}

	expectedCombos := []string{
		"vet:NZ", "vet:AU", "vet:UK", "vet:US",
		"dental:NZ", "dental:AU", "dental:UK", "dental:US",
		"general:NZ", "general:AU", "general:UK", "general:US",
		"aged_care:NZ", "aged_care:AU", "aged_care:UK", "aged_care:US",
	}
	got := make(map[string]int, len(manifest))
	for _, m := range manifest {
		got[m.Vertical+":"+m.Country] = m.Count
	}

	for _, combo := range expectedCombos {
		count, ok := got[combo]
		if !ok {
			t.Errorf("manifest missing combo %s", combo)
			continue
		}
		if count == 0 {
			t.Errorf("combo %s has 0 entries — should have at least 1", combo)
		}
	}
}

// TestEntries_ReturnsByCombo checks that the per-(vertical, country)
// lookup returns entries from the right file and not a different one.
func TestEntries_ReturnsByCombo(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	vetNZ := l.Entries("vet", "NZ")
	if len(vetNZ) == 0 {
		t.Fatalf("vet:NZ returned 0 entries")
	}
	for _, e := range vetNZ {
		if !strings.HasPrefix(e.ID, "vet.NZ.") {
			t.Errorf("vet:NZ entry has wrong ID prefix: %s", e.ID)
		}
	}

	dentalUK := l.Entries("dental", "UK")
	if len(dentalUK) == 0 {
		t.Fatalf("dental:UK returned 0 entries")
	}
	for _, e := range dentalUK {
		if !strings.HasPrefix(e.ID, "dental.UK.") {
			t.Errorf("dental:UK entry has wrong ID prefix: %s", e.ID)
		}
	}
}

// TestLookup_FindsKnownEntry confirms the byID lookup works.
func TestLookup_FindsKnownEntry(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	got := l.Lookup("vet", "NZ", "vet.NZ.ketamine.injectable.100mgml")
	if got == nil {
		t.Fatalf("expected to find ketamine entry; got nil")
	}
	assertKetamineEntry(t, got)
}

// assertKetamineEntry is extracted so staticcheck SA5011 can see the
// non-nil precondition (t.Helper + the t.Fatalf above the call site
// terminates the test on a nil; analyzers accept this in a helper).
func assertKetamineEntry(t *testing.T, got *Entry) {
	t.Helper()
	if got.Name != "Ketamine" {
		t.Errorf("name = %q, want %q", got.Name, "Ketamine")
	}
	if !got.IsControlled() {
		t.Error("ketamine should be controlled")
	}
	if !got.RequiresWitness() {
		t.Error("ketamine should require witness")
	}
}

// TestLookup_MissingEntryReturnsNil — lookup miss isn't an error.
func TestLookup_MissingEntryReturnsNil(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if got := l.Lookup("vet", "NZ", "nonexistent.id.foo"); got != nil {
		t.Errorf("expected nil for unknown id; got %+v", got)
	}
}

// TestEntries_AcceptsCanonicalVerticalAliases verifies the loader accepts
// both the catalog short form ("vet", "general") and the canonical clinic
// strings ("veterinary", "general_clinic") used everywhere else in the
// codebase. Prevents the regression where the drugs catalog endpoint
// returned an empty system list because the clinic stored "veterinary" and
// the catalog files were keyed under "vet".
func TestEntries_AcceptsCanonicalVerticalAliases(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	cases := []struct{ alias, normalized string }{
		{"veterinary", "vet"},
		{"general_clinic", "general"},
		{"dental", "dental"},
		{"aged_care", "aged_care"},
	}
	for _, c := range cases {
		long := l.Entries(c.alias, "NZ")
		short := l.Entries(c.normalized, "NZ")
		if len(long) == 0 {
			t.Errorf("alias %q returned 0 entries; expected same as %q", c.alias, c.normalized)
		}
		if len(long) != len(short) {
			t.Errorf("alias %q -> %d entries; %q -> %d entries; should be equal",
				c.alias, len(long), c.normalized, len(short))
		}
	}
}

// TestEntries_UnknownComboReturnsNil — same: missing catalog isn't an error.
func TestEntries_UnknownComboReturnsNil(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if got := l.Entries("vet", "ZZ"); got != nil {
		t.Errorf("expected nil for unknown country; got %d entries", len(got))
	}
}

// TestEntries_ControlledFlagsConsistent validates that any entry whose
// schedule label is the COUNTRY-SPECIFIC controlled-substance code ALSO
// has Controls.RegisterRequired = true. Schedule labels overlap across
// countries (e.g. "S2" means Pharmacy Medicine in AU but Class B
// controlled drug in NZ), so the matching is country-keyed.
//
// This catches data-entry mistakes where a curator picked the right
// schedule label but forgot to mark the operational rules.
func TestEntries_ControlledFlagsConsistent(t *testing.T) {
	t.Parallel()
	l, err := NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	// Per-country controlled-schedule sets.
	controlledByCountry := map[string]map[string]bool{
		"NZ": {"S1": true, "S2": true, "S3": true},      // Misuse of Drugs Act 1975
		"AU": {"S8": true},                              // Schedule 8 only is "Controlled Drug" in Poisons Standard
		"UK": {"CD2": true, "CD3": true},                // MDR 2001 schedules requiring register
		"US": {"CII": true, "CIII": true, "CIV": true},  // DEA scheduled (CII strictest)
	}

	for _, m := range l.Manifest() {
		controlled := controlledByCountry[m.Country]
		for _, e := range l.Entries(m.Vertical, m.Country) {
			if !controlled[e.Schedule] {
				continue
			}
			if !e.Controls.RegisterRequired {
				t.Errorf("%s: schedule %s in %s requires register but RegisterRequired=false",
					e.ID, e.Schedule, m.Country)
			}
		}
	}
}
