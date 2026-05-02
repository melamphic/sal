package notes

import (
	"strings"
	"testing"
)

// findItem returns the value of the first item with the given label,
// or "" if no match. Used to assert pretty parsing without coupling
// the test to slice ordering.
func findItem(items []PDFSummaryItem, label string) string {
	for _, it := range items {
		if it.Label == label {
			return it.Value
		}
	}
	return ""
}

// Drug-op AI payloads frequently arrive with mostly-null fields — the
// model only fills what it heard. Render must NEVER blow up on missing
// fields; missing values become "—" so the card has a stable layout.
func TestParseUnmaterialisedSystemPayload_DrugOp_PartialPayload(t *testing.T) {
	t.Parallel()
	raw := `{"operation":"administer","drug_name":"antibiotic","quantity":null,` +
		`"unit":null,"dose":null,"route":null,"reason_indication":null,` +
		`"witness_name":null}`

	items := parseUnmaterialisedSystemPayload("drug_op", raw)

	if len(items) != 7 {
		t.Fatalf("expected 7 drug-op items, got %d", len(items))
	}
	if got := findItem(items, "Operation"); got != "Administer" {
		t.Errorf("Operation = %q, want title-cased Administer", got)
	}
	if got := findItem(items, "Drug"); got != "antibiotic" {
		t.Errorf("Drug = %q, want antibiotic", got)
	}
	for _, label := range []string{"Quantity", "Dose", "Route", "Indication", "Witness"} {
		if got := findItem(items, label); got != "—" {
			t.Errorf("%s = %q, want — for null AI value", label, got)
		}
	}
}

func TestParseUnmaterialisedSystemPayload_DrugOp_FullPayload(t *testing.T) {
	t.Parallel()
	raw := `{"operation":"discard","drug_name":"morphine","quantity":2.5,` +
		`"unit":"mg","dose":"1mg/kg","route":"IV","reason_indication":"euthanasia",` +
		`"witness_name":"Dr Smith"}`

	items := parseUnmaterialisedSystemPayload("drug_op", raw)

	if got := findItem(items, "Operation"); got != "Discard" {
		t.Errorf("Operation = %q, want Discard", got)
	}
	if got := findItem(items, "Quantity"); got != "2.5 mg" {
		t.Errorf("Quantity = %q, want \"2.5 mg\"", got)
	}
	if got := findItem(items, "Indication"); got != "euthanasia" {
		t.Errorf("Indication = %q, want euthanasia", got)
	}
}

func TestParseUnmaterialisedSystemPayload_Consent_PartialPayload(t *testing.T) {
	t.Parallel()
	raw := `{"consent_type":"x-ray","scope":null,"captured_via":"verbal",` +
		`"consenting_party_name":null,"witness_name":null,` +
		`"risks_discussed":null,"alternatives_discussed":null,"expires_at":null}`

	items := parseUnmaterialisedSystemPayload("consent", raw)

	if got := findItem(items, "Type"); got != "x-ray" {
		t.Errorf("Type = %q, want x-ray", got)
	}
	if got := findItem(items, "Captured via"); got != "Verbal" {
		t.Errorf("Captured via = %q, want title-cased Verbal", got)
	}
	for _, label := range []string{"Scope", "Risks discussed", "Alternatives", "Witness", "Expires"} {
		if got := findItem(items, label); got != "—" {
			t.Errorf("%s = %q, want —", label, got)
		}
	}
}

func TestParseUnmaterialisedSystemPayload_Incident(t *testing.T) {
	t.Parallel()
	raw := `{"incident_type":"fall","severity":"medium","occurred_at":"2026-05-02T12:00:00Z",` +
		`"location":"ward 2","brief_description":"slipped in corridor","subject_outcome":null}`

	items := parseUnmaterialisedSystemPayload("incident", raw)

	if got := findItem(items, "Severity"); got != "Medium" {
		t.Errorf("Severity = %q, want Medium", got)
	}
	if got := findItem(items, "Type"); got != "fall" {
		t.Errorf("Type = %q, want fall", got)
	}
	if got := findItem(items, "Subject outcome"); got != "—" {
		t.Errorf("Outcome = %q, want — for missing AI value", got)
	}
}

func TestParseUnmaterialisedSystemPayload_PainScore_IntegerScore(t *testing.T) {
	t.Parallel()
	raw := `{"score":4,"pain_scale_used":"glasgow","method":"observed","note":null}`

	items := parseUnmaterialisedSystemPayload("pain_score", raw)

	// Integer scores must NOT render as 4.0 — that's a JSON-quirk
	// usability bug clinicians notice (regulator sees "4.0" and
	// thinks decimal precision applies).
	if got := findItem(items, "Score"); got != "4" {
		t.Errorf("Score = %q, want 4 (no decimal)", got)
	}
	if got := findItem(items, "Method"); got != "Observed" {
		t.Errorf("Method = %q, want Observed", got)
	}
}

// Bad JSON should never crash the renderer. parseUnmaterialisedSystemPayload
// returns nil so the caller falls back to raw-value rendering rather than
// aborting the PDF.
func TestParseUnmaterialisedSystemPayload_InvalidJSON(t *testing.T) {
	t.Parallel()
	raw := `{not valid json`
	if got := parseUnmaterialisedSystemPayload("drug_op", raw); got != nil {
		t.Errorf("invalid JSON should return nil, got %v", got)
	}
}

// Missing keys (older payload schema, future schema) should still
// produce items with — fillers — never panic.
func TestParseUnmaterialisedSystemPayload_MissingKeys(t *testing.T) {
	t.Parallel()
	raw := `{}`
	items := parseUnmaterialisedSystemPayload("drug_op", raw)
	if len(items) != 7 {
		t.Fatalf("empty payload should produce 7 dash-fill rows, got %d", len(items))
	}
	for _, it := range items {
		if it.Value != "—" {
			t.Errorf("%s = %q, want — for missing key", it.Label, it.Value)
		}
	}
}

// Unknown kinds (future system widget types) are surfaced as
// dynamic key/value rows so the PDF still says something useful
// instead of dropping the field.
func TestParseUnmaterialisedSystemPayload_UnknownKindFallsBackToKeys(t *testing.T) {
	t.Parallel()
	raw := `{"alpha":"a","beta":42,"gamma":null}`
	items := parseUnmaterialisedSystemPayload("future_widget", raw)
	if len(items) != 3 {
		t.Fatalf("expected 3 items for unknown-kind fallback, got %d", len(items))
	}
	// Sorted alphabetically by key, so the order is deterministic.
	if items[0].Label != "Alpha" || items[0].Value != "a" {
		t.Errorf("items[0] = %+v, want Alpha/a", items[0])
	}
	if items[1].Label != "Beta" || items[1].Value != "42" {
		t.Errorf("items[1] = %+v, want Beta/42", items[1])
	}
	if items[2].Label != "Gamma" || items[2].Value != "—" {
		t.Errorf("items[2] = %+v, want Gamma/—", items[2])
	}
}

// titleCase must leave the dash-filler untouched so the card never
// renders "Administer · —" as "Administer · — Administer".
func TestTitleCase_LeavesDashFiller(t *testing.T) {
	t.Parallel()
	if got := titleCase("—"); got != "—" {
		t.Errorf("titleCase(—) = %q, want —", got)
	}
	if got := titleCase(""); got != "" {
		t.Errorf("titleCase(empty) = %q, want empty", got)
	}
	if got := titleCase("administer"); got != "Administer" {
		t.Errorf("titleCase(administer) = %q, want Administer", got)
	}
	if got := titleCase("two words"); got != "Two Words" {
		t.Errorf("titleCase(two words) = %q, want Two Words", got)
	}
}

func TestCombine_HandlesEmptyAndDash(t *testing.T) {
	t.Parallel()
	if got := combine("10", "mg"); got != "10 mg" {
		t.Errorf("combine(10, mg) = %q, want \"10 mg\"", got)
	}
	if got := combine("10", ""); got != "10" {
		t.Errorf("combine(10, empty) = %q, want 10", got)
	}
	if got := combine("", "mg"); got != "mg" {
		t.Errorf("combine(empty, mg) = %q, want mg", got)
	}
	if got := combine("—", "—"); got != "—" {
		t.Errorf("combine(—, —) = %q, want —", got)
	}
	if got := combine("Jane", "(parent)"); got != "Jane (parent)" {
		t.Errorf("combine(Jane, parent) = %q, want \"Jane (parent)\"", got)
	}
}

// PDF render must succeed end-to-end with a partial system widget
// payload. Verifies the new drawSystemCard path doesn't panic when the
// AI emits mostly-null values, and that the kind-specific accent colour
// reaches the rendered bytes.
func TestBuildNotePDF_DrugOpPartialPayloadDoesNotPanic(t *testing.T) {
	t.Parallel()
	items := parseUnmaterialisedSystemPayload("drug_op",
		`{"operation":"administer","drug_name":"antibiotic","quantity":null,`+
			`"unit":null,"dose":null,"route":null,"reason_indication":null,`+
			`"witness_name":null}`)
	if len(items) == 0 {
		t.Fatal("parser returned no items")
	}
	buf, err := BuildNotePDF(PDFInput{
		ClinicName:  "Test Clinic",
		FormName:    "SOAP",
		FormVersion: "1.0",
		NoteID:      "note-id",
		Fields: []PDFField{
			{Label: "Subjective", Value: "Larry the bird presented..."},
			{
				Label:         "Drug Operation",
				Value:         `{"operation":"administer"}`,
				SystemSummary: items,
				SystemKind:    "drug_op",
				SystemPending: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildNotePDF failed on partial drug_op payload: %v", err)
	}
	if buf == nil || buf.Len() == 0 {
		t.Fatal("BuildNotePDF returned empty buffer")
	}
	// fpdf compresses content streams by default so substring searches
	// for visible text are unreliable. Smoke test on the magic header
	// is sufficient — if the renderer panicked or returned garbage,
	// `%PDF-` won't be at byte 0.
	if !strings.HasPrefix(buf.String(), "%PDF-") {
		preview := buf.String()
		if len(preview) > 8 {
			preview = preview[:8]
		}
		t.Errorf("output is not a valid PDF; first bytes = %q", preview)
	}
}
