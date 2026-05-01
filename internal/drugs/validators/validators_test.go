package validators

import (
	"strings"
	"testing"
)

// Helpers — keep tests readable.

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }
func ptrInt(i int) *int       { return &i }
func ptrFloat(f float64) *float64 { return &f }

func hasCode(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func issueCodes(issues []Issue) string {
	codes := make([]string, len(issues))
	for i, x := range issues {
		codes[i] = x.Code
	}
	return strings.Join(codes, ",")
}

// ── Dispatch ─────────────────────────────────────────────────────────

func TestDispatch_KnownCountries(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"UK": "UK",
		"GB": "UK",
		"US": "US",
		"NZ": "NZ",
		"AU": "AU",
		"uk": "UK", // case-insensitive
	}
	for in, want := range cases {
		v := Dispatch(in)
		if v.Country() != want {
			t.Fatalf("Dispatch(%q).Country() = %q, want %q", in, v.Country(), want)
		}
	}
}

func TestDispatch_UnknownCountryReturnsNoop(t *testing.T) {
	t.Parallel()

	v := Dispatch("ZZ")
	issues := v.Validate(OperationContext{Operation: OpReceive, IsControlled: true})
	if len(issues) != 0 {
		t.Fatalf("noop validator should return no issues, got %s", issueCodes(issues))
	}
}

// ── UK ───────────────────────────────────────────────────────────────

func TestUK_NonControlled_NoIssues(t *testing.T) {
	t.Parallel()

	got := ukValidator{}.Validate(OperationContext{Operation: OpReceive, IsControlled: false})
	if len(got) != 0 {
		t.Fatalf("UK should not flag non-controlled drugs: %s", issueCodes(got))
	}
}

func TestUK_Receive_RequiresSupplierNameAndAddress(t *testing.T) {
	t.Parallel()

	got := ukValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		IsControlled: true,
	})

	if !hasCode(got, "uk_receive_supplier_name_required") {
		t.Errorf("missing uk_receive_supplier_name_required: %s", issueCodes(got))
	}
	if !hasCode(got, "uk_receive_supplier_address_required") {
		t.Errorf("missing uk_receive_supplier_address_required: %s", issueCodes(got))
	}
}

func TestUK_Receive_FullCompliancePasses(t *testing.T) {
	t.Parallel()

	got := ukValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		IsControlled: true,
		Compliance: ComplianceInput{
			CounterpartyName:    ptrStr("Boots Healthcare"),
			CounterpartyAddress: ptrStr("Nottingham NG90 1BS"),
		},
	})

	if len(got) != 0 {
		t.Fatalf("UK receive with full compliance should pass: %s", issueCodes(got))
	}
}

func TestUK_Dispense_RequiresCollectorIDFlags(t *testing.T) {
	t.Parallel()

	got := ukValidator{}.Validate(OperationContext{
		Operation:    OpDispense,
		IsControlled: true,
		Compliance: ComplianceInput{
			CounterpartyName:    ptrStr("Jane Doe"),
			CounterpartyAddress: ptrStr("123 High St"),
		},
	})

	if !hasCode(got, "uk_dispense_collector_name_required") {
		t.Errorf("missing collector name issue")
	}
	if !hasCode(got, "uk_dispense_collector_id_requested_required") {
		t.Errorf("missing collector ID requested issue")
	}
	if !hasCode(got, "uk_dispense_collector_id_provided_required") {
		t.Errorf("missing collector ID provided issue")
	}
}

func TestUK_Dispense_PrescriberRequiredWhenRxRef(t *testing.T) {
	t.Parallel()

	got := ukValidator{}.Validate(OperationContext{
		Operation:    OpDispense,
		IsControlled: true,
		Compliance: ComplianceInput{
			CounterpartyName:               ptrStr("Jane Doe"),
			CounterpartyAddress:            ptrStr("123 High St"),
			CollectorName:                  ptrStr("Jane Doe"),
			CollectorIDEvidenceRequested:   ptrBool(true),
			CollectorIDEvidenceProvided:    ptrBool(true),
			PrescriptionRef:                ptrStr("RX-12345"),
		},
	})

	if !hasCode(got, "uk_dispense_prescriber_name_required") {
		t.Errorf("missing prescriber name issue when Rx ref present: %s", issueCodes(got))
	}
}

// ── US ───────────────────────────────────────────────────────────────

func TestUS_SchII_Receive_RequiresContainerCount(t *testing.T) {
	t.Parallel()

	got := usValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		Schedule:     "CII",
		IsControlled: true,
		Compliance: ComplianceInput{
			CounterpartyName:      ptrStr("Cardinal Health"),
			CounterpartyAddress:   ptrStr("Dublin OH"),
			CounterpartyDEANumber: ptrStr("AB1234567"),
			OrderFormSerial:       ptrStr("222-12345"),
			DEARegistrationID:     ptrStr("00000000-0000-0000-0000-000000000000"),
		},
	})

	if !hasCode(got, "us_sch2_container_count_required") {
		t.Errorf("missing container count issue: %s", issueCodes(got))
	}
	if !hasCode(got, "us_sch2_units_per_container_required") {
		t.Errorf("missing units per container issue: %s", issueCodes(got))
	}
}

func TestUS_SchII_Receive_FullCompliancePasses(t *testing.T) {
	t.Parallel()

	got := usValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		Schedule:     "CII",
		IsControlled: true,
		Compliance: ComplianceInput{
			CounterpartyName:         ptrStr("Cardinal Health"),
			CounterpartyAddress:      ptrStr("Dublin OH"),
			CounterpartyDEANumber:    ptrStr("AB1234567"),
			OrderFormSerial:          ptrStr("222-12345"),
			DEARegistrationID:        ptrStr("00000000-0000-0000-0000-000000000000"),
			CommercialContainerCount: ptrInt(2),
			UnitsPerContainer:        ptrFloat(100),
		},
	})

	if len(got) != 0 {
		t.Fatalf("US Sch II receive with full compliance should pass: %s", issueCodes(got))
	}
}

func TestUS_NonSchII_NoContainerCheck(t *testing.T) {
	t.Parallel()

	got := usValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		Schedule:     "CIV",
		IsControlled: true,
		Compliance: ComplianceInput{
			CounterpartyName:      ptrStr("Cardinal"),
			CounterpartyAddress:   ptrStr("Dublin OH"),
			CounterpartyDEANumber: ptrStr("AB1234567"),
			DEARegistrationID:     ptrStr("00000000-0000-0000-0000-000000000000"),
		},
	})

	if hasCode(got, "us_sch2_container_count_required") {
		t.Errorf("non-Sch II should not require container count: %s", issueCodes(got))
	}
}

// ── NZ ───────────────────────────────────────────────────────────────

func TestNZ_ClassA_Receive_RequiresSupplier(t *testing.T) {
	t.Parallel()

	got := nzValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		Schedule:     "S1",
		IsControlled: true,
	})

	if !hasCode(got, "nz_receive_supplier_name_required") {
		t.Errorf("missing supplier name issue: %s", issueCodes(got))
	}
	if !hasCode(got, "nz_receive_supplier_address_required") {
		t.Errorf("missing supplier address issue: %s", issueCodes(got))
	}
}

func TestNZ_ClassC_NoStrictReceive(t *testing.T) {
	t.Parallel()

	// Class C controlled drugs (Reg 31) have looser register format.
	// Today we don't enforce supplier on Class C — flagged only Class A/B.
	got := nzValidator{}.Validate(OperationContext{
		Operation:    OpReceive,
		Schedule:     "S3",
		IsControlled: true,
	})

	if hasCode(got, "nz_receive_supplier_name_required") {
		t.Errorf("Class C should not require formal supplier (Reg 31): %s", issueCodes(got))
	}
}

func TestNZ_Discard_RequiresWitness(t *testing.T) {
	t.Parallel()

	got := nzValidator{}.Validate(OperationContext{
		Operation:    OpDiscard,
		Schedule:     "S1",
		IsControlled: true,
	})

	if !hasCode(got, "nz_discard_witness_required") {
		t.Errorf("missing witness-required issue on NZ discard: %s", issueCodes(got))
	}
}

// ── AU ───────────────────────────────────────────────────────────────

func TestAU_Receive_RequiresBatchAndExpiry(t *testing.T) {
	t.Parallel()

	got := auValidator{}.Validate(OperationContext{
		Operation:     OpReceive,
		Schedule:      "S8",
		IsControlled:  true,
		ClinicCountry: "AU",
		ClinicState:   "NSW",
	})

	if !hasCode(got, "au_receive_batch_required") {
		t.Errorf("missing batch issue: %s", issueCodes(got))
	}
	if !hasCode(got, "au_receive_expiry_required") {
		t.Errorf("missing expiry issue: %s", issueCodes(got))
	}
}

func TestAU_WA_Dispense_RequiresWitness(t *testing.T) {
	t.Parallel()

	// WA is the strictest state — RequireDispenseWitness == true.
	got := auValidator{}.Validate(OperationContext{
		Operation:     OpDispense,
		Schedule:      "S8",
		IsControlled:  true,
		ClinicCountry: "AU",
		ClinicState:   "WA",
		Compliance: ComplianceInput{
			CounterpartyName:    ptrStr("J Doe"),
			CounterpartyAddress: ptrStr("Perth"),
			Witnessed:           false,
		},
	})

	if !hasCode(got, "au_dispense_witness_required") {
		t.Errorf("WA dispense should require witness: %s", issueCodes(got))
	}
}

func TestAU_NSW_Dispense_NoWitnessRequired(t *testing.T) {
	t.Parallel()

	got := auValidator{}.Validate(OperationContext{
		Operation:     OpDispense,
		Schedule:      "S8",
		IsControlled:  true,
		ClinicCountry: "AU",
		ClinicState:   "NSW",
		Compliance: ComplianceInput{
			CounterpartyName:    ptrStr("J Doe"),
			CounterpartyAddress: ptrStr("Sydney"),
			Witnessed:           false,
		},
	})

	if hasCode(got, "au_dispense_witness_required") {
		t.Errorf("NSW dispense should NOT require witness in our rules: %s", issueCodes(got))
	}
}

func TestAU_UnknownStateFallsBackToWA(t *testing.T) {
	t.Parallel()

	r := ruleForState("XX")
	if r.State != "WA" {
		t.Fatalf("unknown state should fall back to WA-strict, got %s", r.State)
	}
	if !r.RequireDispenseWitness {
		t.Fatalf("WA-strict should require dispense witness")
	}
}

func TestAU_QLD_HasPurposeColumnFlag(t *testing.T) {
	t.Parallel()

	r := ruleForState("QLD")
	if !r.RequirePurposeColumn {
		t.Fatalf("QLD should set RequirePurposeColumn for animal Sch 8 admin")
	}
}

func TestAU_Discard_RequiresWitnessAndReason(t *testing.T) {
	t.Parallel()

	got := auValidator{}.Validate(OperationContext{
		Operation:     OpDiscard,
		Schedule:      "S8",
		IsControlled:  true,
		ClinicCountry: "AU",
		ClinicState:   "NSW",
	})

	if !hasCode(got, "au_discard_witness_required") {
		t.Errorf("missing witness issue: %s", issueCodes(got))
	}
	if !hasCode(got, "au_discard_reason_required") {
		t.Errorf("missing reason issue: %s", issueCodes(got))
	}
}

// ── Schedule helpers ─────────────────────────────────────────────────

func TestIsScheduleII(t *testing.T) {
	t.Parallel()

	yes := []string{"CII", "C-II", "II", "Schedule II", "schedule ii"}
	for _, s := range yes {
		if !isScheduleII(s) {
			t.Errorf("isScheduleII(%q) = false, want true", s)
		}
	}
	no := []string{"CIII", "CIV", "S8", "CD2", ""}
	for _, s := range no {
		if isScheduleII(s) {
			t.Errorf("isScheduleII(%q) = true, want false", s)
		}
	}
}

func TestIsClassAOrB(t *testing.T) {
	t.Parallel()

	yes := []string{"S1", "S2", "Class A", "class b", "B", "a"}
	for _, s := range yes {
		if !isClassAOrB(s) {
			t.Errorf("isClassAOrB(%q) = false, want true", s)
		}
	}
	no := []string{"S3", "Class C", "C", ""}
	for _, s := range no {
		if isClassAOrB(s) {
			t.Errorf("isClassAOrB(%q) = true, want false", s)
		}
	}
}
