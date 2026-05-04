package v2

import (
	"strings"
	"testing"
	"time"
)

func sampleCDRegisterInput() CDRegisterInput {
	return CDRegisterInput{
		ClinicID:    "00000000-0000-0000-0000-000000000001",
		ClinicName:  "Riverside Veterinary Hospital",
		ClinicAddr:  "14 Ponsonby Rd, Auckland 1011",
		ClinicMeta:  "NZBN 9429048372910 · Class B/C licence #PHA-CLB-04412",
		PeriodLabel: "Q2 2026 · Apr–Jun",
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		Drugs: []CDRegisterDrug{
			{
				Class: "B", Name: "Methadone HCl",
				FormStrength: "Injectable · 10 mg/ml",
				Storage:      "CD Safe — Treatment Room",
				CatalogID:    "vet.NZ.cd.methadone-10",
				BatchExp:     "M82041 · Exp 2027-08",
				Unit:         "ml",
				Opening:      82.0, ClosingBal: 84.4, InTotal: 50.0, OutTotal: 47.6,
				Operations: []CDOperation{
					{
						WhenPretty: "02 Apr 14:32",
						OpKind:     "RECEIVE", OpTone: "info",
						Subject:   "Restock from supplier · Invoice #SUP-44102",
						QtyDelta:  "+50.0",
						BalBefore: "82.0", BalAfter: "132.0",
						StaffShort: "A. Williams", WitnessShort: "H. Patel",
					},
					{
						WhenPretty: "03 Apr 09:14",
						OpKind:     "ADMIN", OpTone: "ok",
						Subject:    "Buddy (canine, MN) · Pre-op analgesia for OVH",
						QtyDelta:   "−0.4",
						BalBefore:  "132.0", BalAfter: "131.6",
						StaffShort: "A. Williams", WitnessShort: "H. Patel",
					},
				},
			},
		},
		ReconciliationOK: true,
		ReconciledOn:     "2026-04-30 17:42",
		ReconciledByA:    "Dr. A. Williams · VCNZ #VC-04412",
		ReconciledByB:    "RVN H. Patel · NZVNA #VN-22810",
		NextDueOn:        "2026-05-31",
		BundleHash:       "7e3b1c4a9d22f0e85a1b2c3d4e5f6789",
	}
}

func TestBuildCDRegisterBody(t *testing.T) {
	t.Parallel()
	body, err := buildCDRegisterBody(sampleCDRegisterInput())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"Riverside Veterinary Hospital",
		"Q2 2026 · Apr–Jun",
		"Methadone HCl",
		"vet.NZ.cd.methadone-10",
		`pill pill--danger">B</span>`,
		"Reconciliation",
		"Dr. A. Williams · VCNZ #VC-04412",
		"7e3b1c4a9d22f0e85a1b2c3d4e5f6789",
		"Buddy (canine, MN)",
		`pill pill--ok">ADMIN</span>`,
		`pill pill--info">RECEIVE</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CD register body missing %q", want)
		}
	}
}
