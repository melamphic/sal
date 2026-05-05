// Command smoke-pdf renders one or all of the V1 reports against a
// running Gotenberg sidecar and writes the PDFs to /tmp.
//
// Usage:
//
//	make dev          # starts gotenberg
//	go run ./cmd/smoke-pdf            # renders all 7 (signed note + 6 reports)
//	go run ./cmd/smoke-pdf signed_note
//	go run ./cmd/smoke-pdf cd_register cd_reconciliation
//
// Each PDF lands at /tmp/smoke-{slug}.pdf.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/melamphic/sal/internal/notes"
	"github.com/melamphic/sal/internal/platform/config"
	"github.com/melamphic/sal/internal/platform/pdf"
	"github.com/melamphic/sal/internal/reports/v2"
)

type renderFn func(context.Context, *notes.HTMLRenderer, *v2.Renderer) ([]byte, error)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "smoke-pdf:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := &config.Config{
		GotenbergURL:     envOr("GOTENBERG_URL", "http://localhost:3050"),
		GotenbergTimeout: 60 * time.Second,
	}
	platformPDF := pdf.New(cfg)
	notesR := notes.NewHTMLRenderer(platformPDF)
	v2R := v2.New(platformPDF, nil)

	all := map[string]renderFn{
		"signed_note":       smokeSignedNote,
		"cd_register":       smokeCDRegister,
		"cd_reconciliation": smokeCDReconciliation,
		"incident_report":   smokeIncident,
		"pain_trend":        smokePainTrend,
		"mar_grid":          smokeMAR,
		"audit_pack":        smokeAuditPack,
	}

	want := os.Args[1:]
	if len(want) == 0 {
		for k := range all {
			want = append(want, k)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, slug := range want {
		fn, ok := all[slug]
		if !ok {
			_, _ = fmt.Fprintf(os.Stderr, "smoke-pdf: unknown slug %q\n", slug)
			continue
		}
		bytes, err := fn(ctx, notesR, v2R)
		if err != nil {
			return fmt.Errorf("%s: %w", slug, err)
		}
		path := fmt.Sprintf("/tmp/smoke-%s.pdf", slug)
		if err := os.WriteFile(path, bytes, 0o644); err != nil {
			return fmt.Errorf("%s: write: %w", slug, err)
		}
		_, _ = fmt.Fprintf(os.Stderr, "%-22s %6d bytes → %s\n", slug, len(bytes), path)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ── Per-report smoke fixtures ─────────────────────────────────────────────

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokeSignedNote(ctx context.Context, r *notes.HTMLRenderer, _ *v2.Renderer) ([]byte, error) {
	addr := "14 Ponsonby Rd, Auckland 1011"
	phone := "021 555 4127"
	primary := "#0e7c66"
	dispName := "Buddy"
	species := "Canine"
	breed := "King Charles Spaniel"
	weight := 12.4
	chip := "956000012345678"
	visit := time.Date(2026, 4, 28, 14, 32, 0, 0, time.UTC)

	return r.RenderNoteAsPDF(ctx, notes.PDFInput{
		Theme:         &notes.DocTheme{Theme: &notes.DocThemeTheme{PrimaryColor: &primary}},
		ClinicName:    "Riverside Veterinary Hospital",
		ClinicAddress: &addr,
		ClinicPhone:   &phone,
		FormName:      "SOAP — Small Animal",
		FormVersion:   "3.2",
		NoteID:        "018e7f6d-aaaa-bbbb-cccc-000000000000",
		SubmittedAt:   time.Date(2026, 4, 28, 15, 8, 0, 0, time.UTC),
		SubmittedBy:   "Dr. Aroha Williams",
		Subject: &notes.PDFSubject{
			DisplayName: &dispName, Species: &species, Breed: &breed,
			WeightKg: &weight, Microchip: &chip,
		},
		VisitDate: &visit,
		Fields: []notes.PDFField{
			{Label: "Presenting complaint", Value: "Right hindlimb lameness, intermittent, 4 days. No known trauma."},
			{Label: "Pain score (NRS)", Value: "3"},
			{
				Label: "Drug op — Meloxicam dispense", SystemKind: "drug_op", SystemReviewStatus: "approved",
				SystemSummary: []notes.PDFSummaryItem{
					{Label: "Drug", Value: "Meloxicam 1.5 mg/ml"},
					{Label: "Quantity", Value: "7 ml"},
				},
			},
		},
	})
}

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokeCDRegister(ctx context.Context, _ *notes.HTMLRenderer, r *v2.Renderer) ([]byte, error) {
	return r.RenderCDRegister(ctx, v2.CDRegisterInput{
		Clinic: pdf.ClinicInfo{
			Name:         "Riverside Veterinary Hospital",
			AddressLine1: "14 Ponsonby Rd, Auckland 1011",
			Meta:         "NZBN 9429048372910 · Class B/C licence #PHA-CLB-04412",
		},
		PeriodLabel: "Q2 2026 · Apr–Jun",
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		Drugs: []v2.CDRegisterDrug{
			{
				Class: "B", Name: "Methadone HCl",
				FormStrength: "Injectable · 10 mg/ml",
				Storage:      "CD Safe — Treatment Room",
				CatalogID:    "vet.NZ.cd.methadone-10",
				BatchExp:     "M82041 · Exp 2027-08",
				Unit:         "ml",
				Opening:      82.0, ClosingBal: 84.4, InTotal: 50.0, OutTotal: 47.6,
				Operations: []v2.CDOperation{
					{WhenPretty: "02 Apr 14:32", OpKind: "RECEIVE", OpTone: "info", Subject: "Restock from supplier · Invoice #SUP-44102", QtyDelta: "+50.0", BalBefore: "82.0", BalAfter: "132.0", StaffShort: "A. Williams", WitnessShort: "H. Patel"},
					{WhenPretty: "03 Apr 09:14", OpKind: "ADMIN", OpTone: "ok", Subject: "Buddy (canine, MN) · Pre-op analgesia for OVH", QtyDelta: "−0.4", BalBefore: "132.0", BalAfter: "131.6", StaffShort: "A. Williams", WitnessShort: "H. Patel"},
					{WhenPretty: "06 Apr 08:45", OpKind: "DISCARD", OpTone: "danger", Subject: "Partial vial — drawn but not used (cancelled GA)", QtyDelta: "−0.3", BalBefore: "131.6", BalAfter: "131.3", StaffShort: "A. Williams", WitnessShort: "H. Patel"},
				},
			},
			{
				Class: "B", Name: "Buprenorphine",
				FormStrength: "Injectable · 0.3 mg/ml",
				Storage:      "CD Safe — Treatment Room",
				CatalogID:    "vet.NZ.cd.bup-0.3",
				BatchExp:     "B45221 · Exp 2026-12",
				Unit:         "ml",
				Opening:      14.2, ClosingBal: 14.4, InTotal: 10.0, OutTotal: 9.8,
				Operations: []v2.CDOperation{
					{WhenPretty: "12 Apr 11:08", OpKind: "ADMIN", OpTone: "ok", Subject: "Mochi (feline, FN) · Post-op pain", QtyDelta: "−0.1", BalBefore: "14.2", BalAfter: "14.1", StaffShort: "R. Singh", WitnessShort: "H. Patel"},
				},
			},
		},
		ReconciliationOK: true,
		ReconciledOn:     "2026-04-30 17:42",
		ReconciledByA:    "Dr. A. Williams · VCNZ #VC-04412",
		ReconciledByB:    "RVN H. Patel · NZVNA #VN-22810",
		NextDueOn:        "2026-05-31",
		BundleHash:       "7e3b1c4a9d22f0e85a1b2c3d4e5f6789",
	})
}

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokeCDReconciliation(ctx context.Context, _ *notes.HTMLRenderer, r *v2.Renderer) ([]byte, error) {
	return r.RenderCDReconciliation(ctx, v2.CDReconciliationInput{
		Clinic: pdf.ClinicInfo{
			Name:         "Riverside Veterinary Hospital",
			AddressLine1: "14 Ponsonby Rd, Auckland 1011",
			Meta:         "Class B/C licence #PHA-CLB-04412",
		},
		PeriodLabel:  "April 2026",
		ReconciledOn: "2026-04-30 17:42 NZST",
		Drugs: []v2.CDReconRow{
			{Drug: "Methadone HCl 10 mg/ml", Ledger: "84.4 ml", Physical: "84.4 ml", Delta: 0, DeltaPct: 0, Status: "Clean", StatusTone: "ok"},
			{Drug: "Buprenorphine 0.3 mg/ml", Ledger: "14.4 ml", Physical: "14.4 ml", Delta: 0, DeltaPct: 0, Status: "Clean", StatusTone: "ok"},
			{Drug: "Ketamine HCl 100 mg/ml", Ledger: "102.0 ml", Physical: "101.6 ml", Delta: -0.4, DeltaPct: -0.39, Status: "Explained", StatusTone: "warn", Notes: "Partial-vial waste · GA cancelled"},
			{Drug: "Diazepam inj 5 mg/ml", Ledger: "16.5 ml", Physical: "16.5 ml", Delta: 0, DeltaPct: 0, Status: "Clean", StatusTone: "ok"},
		},
		TrendLabels:         []string{"May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec", "Jan", "Feb", "Mar", "Apr"},
		TrendDiscrepancies:  []int{0, 1, 0, 0, 2, 0, 0, 1, 0, 0, 0, 1},
		TrendDrugCount:      []int{7, 7, 7, 8, 8, 8, 8, 8, 8, 8, 8, 8},
		PrimaryReconciler:   "Aroha Williams",
		SecondaryReconciler: "Hema Patel",
		BundleHash:          "5a7c2e1f8b9d04a3e6c1f9b2d5a8e7f1",
	})
}

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokeIncident(ctx context.Context, _ *notes.HTMLRenderer, r *v2.Renderer) ([]byte, error) {
	return r.RenderIncidentReport(ctx, v2.IncidentReportInput{
		Clinic: pdf.ClinicInfo{
			Name:         "Sycamore House Care Home",
			AddressLine1: "42 Elm Lane, Bristol BS6 7XR",
			Meta:         "CQC location ID 1-247118331",
		},
		IncidentRef:    "INC-2026-00184",
		GeneratedOn:    "2026-04-29",
		SeverityLabel:  "High",
		SeverityTone:   "danger",
		SeverityHint:   "Hospitalised — fractured neck of femur",
		TypeLabel:      "Fall · Injurious",
		TypeHint:       "Witnessed · unobserved at impact",
		CQCNotifiable:  true,
		CQCSubmittedOn: "29 Apr 11:47",
		CQCRefNumber:   "CQC-NTF-3344821",
		ResidentName:   "Mary White",
		ResidentMeta:   "(age 87)",
		Room:           "Maple wing · Room 14",
		CareCategory:   "Residential — moderate dementia (CDR 2)",
		OccurredAt:     "2026-04-28 03:42 BST",
		Location:       "Bedroom — between bed and en-suite",
		ReportedBy:     "Sarah Okonkwo (HCA, on duty)",
		BriefDescription: "Mary was found on the floor of her room at 03:42 by HCA Sarah Okonkwo during routine 4-hourly check. Right hip externally rotated and shortened — suspected fractured NoF.",
		ActionsTimeline: []v2.IncidentTimelineItem{
			{WhenPretty: "03:42", Tone: "default", Body: "<strong>Found by HCA.</strong> Did not move resident. Made safe. Called nurse-in-charge."},
			{WhenPretty: "03:46", Tone: "default", Body: "<strong>Nurse on scene.</strong> J. Patel RN performed primary survey. GCS 15."},
			{WhenPretty: "03:51", Tone: "warn", Body: "<strong>999 ambulance called.</strong> Category 2 dispatch."},
			{WhenPretty: "11:47", Tone: "ok", Body: "<strong>CQC notification submitted.</strong> Ref CQC-NTF-3344821 returned."},
		},
		Outcome24h: []v2.IncidentOutcomeRow{
			{Label: "Hospital diagnosis", Value: "Right intracapsular fractured neck of femur (Garden III)."},
			{Label: "Surgical plan", Value: "Hemi-arthroplasty scheduled 30 Apr"},
			{Label: "Resident status", Value: "Hospitalised, stable, awaiting surgery"},
		},
		Notifications: []v2.IncidentNotificationRow{
			{Authority: "CQC Reg 18(1)", Status: "Acknowledged", StatusTone: "ok"},
			{Authority: "Safeguarding (Bristol)", Status: "In progress", StatusTone: "warn"},
			{Authority: "RIDDOR (HSE)", Status: "Not applicable", StatusTone: "muted"},
		},
		RCA: &v2.IncidentRCA{
			Method: "five_whys",
			Factors: []v2.IncidentRCAFactor{
				{Factor: "Walking frame placement", Finding: "Frame within reach but resident did not use it.", Contributory: "primary", Tone: "danger"},
				{Factor: "Lighting", Finding: "Pathway light not on (motion-sensor failure).", Contributory: "contributory", Tone: "warn"},
				{Factor: "Medication review", Finding: "Zopiclone 3.75mg nocte commenced 18 Apr.", Contributory: "contributory", Tone: "warn"},
				{Factor: "Footwear", Finding: "Resident barefoot at time of fall.", Contributory: "contributory", Tone: "warn"},
				{Factor: "Staffing", Finding: "Night staff at planned ratio.", Contributory: "no", Tone: "muted"},
			},
		},
		RootCause: "Care plan review reduced night-time prompts without re-assessing fall risk. Combined with new zopiclone introduction (18 Apr) and unrepaired pathway light, the protective measures in place at last falls assessment were no longer adequate.",
		ActionPlan: []v2.IncidentActionItem{
			{Action: "Reinstate hourly night-time prompts", Owner: "Care plan lead", DuePretty: "30 Apr", Status: "Done", StatusTone: "ok"},
			{Action: "Repair pathway motion light", Owner: "Maintenance", DuePretty: "02 May", Status: "Done", StatusTone: "ok"},
			{Action: "Pharmacy review — zopiclone risk vs. benefit", Owner: "Clinical lead + GP", DuePretty: "07 May", Status: "In progress", StatusTone: "warn"},
		},
		ReportedBySigName: "Sarah Okonkwo",
		ReviewedBySigName: "Karen Thompson",
		BundleHash:        "4f9c2a8b1d3e7506f2a4b8c1d3e5f9a2",
	})
}

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokePainTrend(ctx context.Context, _ *notes.HTMLRenderer, r *v2.Renderer) ([]byte, error) {
	days := []string{"01 Apr", "05 Apr", "10 Apr", "15 Apr", "20 Apr", "23 Apr", "24 Apr", "25 Apr", "26 Apr", "27 Apr", "28 Apr", "29 Apr", "30 Apr"}
	scores := []int{2, 1, 2, 1, 2, 3, 5, 5, 6, 5, 7, 4, 3}
	return r.RenderPainTrend(ctx, v2.PainTrendInput{
		Clinic: pdf.ClinicInfo{
			Name:         "Sycamore House Care Home",
			AddressLine1: "42 Elm Lane, Bristol BS6 7XR",
		},
		SubjectName:   "Mary White (age 87)",
		SubjectRoom:   "Maple wing · Room 14",
		SubjectMeta:   "Residential — moderate dementia",
		PeriodLabel:   "April 2026",
		WindowDays:    30,
		Assessments:   62,
		ScalesUsed:    "PainAD (28) · NRS (34)",
		MeanScore:     2.4,
		PeakScore:     7,
		PeakWhen:      "28 Apr 03:45 — pre-fall",
		PRNDaysPct:    23,
		PRNDaysCount:  7,
		WitnessedHigh: 8,
		DailyLabels:   days,
		DailyScores:   scores,
		DistLabels:    []string{"0-1 None", "2-3 Mild", "4-6 Mod", "7-10 Severe"},
		DistCounts:    []int{38, 16, 7, 1},
		WeekLabels:    []string{"Wk 14", "Wk 15", "Wk 16", "Wk 17"},
		PRNParacetamol: []int{1, 2, 2, 5},
		PRNOramorph:   []int{0, 0, 0, 3},
		HighScores: []v2.PainHighScoreRow{
			{WhenPretty: "26 Apr 21:42", Score: 6, ScoreTone: "warn", Scale: "NRS", Context: "RN J. Patel · achy, can't get comfortable", Witness: "—", PRNGiven: "Paracetamol 1g · oramorph 5mg"},
			{WhenPretty: "28 Apr 03:45", Score: 7, ScoreTone: "danger", Scale: "PainAD", Context: "HCA S. Okonkwo · post-fall", Witness: "RN Patel", PRNGiven: "Paracetamol 1g (in incident timeline)"},
		},
		GeneratedBy: "Karen Thompson · Registered Manager",
		GeneratedOn: "2026-05-04 09:18",
		BundleHash:  "1c8f3e9d2a7b40c6e1f4a8d3b2c7e9f1",
	})
}

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokeMAR(ctx context.Context, _ *notes.HTMLRenderer, r *v2.Renderer) ([]byte, error) {
	days, dow, weekend := v2.MonthDays(2026, 4, time.UTC)
	cells := func(initials string, n int) []v2.MARCell {
		out := make([]v2.MARCell, n)
		for i := range out {
			out[i] = v2.MARCell{Initials: initials, OutcomeCode: "given", Weekend: weekend[i]}
		}
		return out
	}
	holdAfter := func(base []v2.MARCell, fromCol int) []v2.MARCell {
		for i := fromCol; i < len(base); i++ {
			base[i] = v2.MARCell{Initials: "H", OutcomeCode: "held", Weekend: weekend[i]}
		}
		return base
	}
	return r.RenderMARGrid(ctx, v2.MARGridInput{
		Clinic: pdf.ClinicInfo{
			Name:         "Sycamore House Care Home",
			AddressLine1: "42 Elm Lane, Bristol BS6 7XR",
			Meta:         "CQC location ID 1-247118331",
		},
		ResidentName:  "Mary White",
		ResidentMeta:  "(age 87)",
		Room:          "Maple wing · Room 14",
		NHSNumber:     "488 219 3104",
		GP:            "Dr Patel · Sycamore Health Centre",
		Allergies:     "Penicillin (rash, 1972)",
		CarePlanRev:   "2026-03-15 · next 2026-06-15",
		Pharmacy:      "Boots Bristol Whiteladies (B-12)",
		PeriodLabel:   "April 2026",
		Days:          days,
		DOWLabels:     dow,
		IsWeekend:     weekend,
		Prescriptions: []v2.MARPrescription{
			{
				DrugLine: `<strong>Furosemide 40 mg</strong><em>1 tab PO mane · CHF</em>`,
				Time:     "08:00",
				Cells:    holdAfter(cells("SO", 30), 27),
			},
			{
				DrugLine: `<strong>Donepezil 5 mg</strong><em>1 tab PO nocte · dementia</em>`,
				Time:     "21:00",
				Cells:    holdAfter(cells("JP", 30), 27),
			},
			{
				DrugLine:    `<strong>Zopiclone 3.75 mg</strong><em>1 tab PO nocte PRN · started 18-Apr</em>`,
				Time:        "22:00",
				IsFallsRisk: true,
				StartsLate:  true,
				StartsAtCol: 17,
				Cells:       holdAfter(cells("JP", 30), 27),
			},
		},
		Notes:           `<ul style="margin: 4px 0 0 16px; font-size: 8.5pt; padding: 0;"><li><strong>28 Apr 04:00 →</strong> all medications on hospital hold (transferred to BRI post-fall).</li></ul>`,
		StaffInitialsKey: "SO = Sarah Okonkwo (HCA) · JP = Jodie Patel (RN, NMC #11A2839E)",
		ReviewedBy:      "Karen Thompson",
		BundleHash:      "8c4a1e7d3b9f02c5e8a1f4b7d9c2e5f8",
	})
}

//nolint:wrapcheck // smoke CLI; render errors surface verbatim
func smokeAuditPack(ctx context.Context, n *notes.HTMLRenderer, r *v2.Renderer) ([]byte, error) {
	// Embed the signed-note body inside the audit pack.
	signedBody, err := smokeSignedNote(ctx, n, r)
	if err != nil {
		return nil, err
	}
	// Crudely inline a placeholder for the signed-note body — in
	// production the body builder would render the HTML body without
	// the Gotenberg round-trip and pass that string through. Here
	// we just point at the pre-rendered fact that we have one.
	// (For the smoke we still attach the structural fields so the
	// audit-pack template covers cover + evidence + edits + policy.)
	_ = signedBody

	notePlaceholder := `<div class="callout"><div class="callout__title">Signed clinical note (embedded)</div>The signed note PDF is included in the bundle as page 2; this section reproduces its body inline. In the production renderer the body string is passed via SignedNoteBody.</div>`
	return r.RenderAuditPack(ctx, v2.AuditPackInput{
		Clinic: pdf.ClinicInfo{
			Name:         "Riverside Veterinary Hospital",
			AddressLine1: "14 Ponsonby Rd, Auckland 1011",
			Meta:         "VCNZ Registered Practice",
		},
		NoteID:          "018e7f6d-aaaa-bbbb-cccc-000000000000",
		NoteIDShort:     "018e7f6d",
		GeneratedOn:     "2026-05-04 16:42 NZST",
		BundleHashFull:  "9d4f2c1a8b7e3a05f1c6e9d28a4b1f3c7e3b1c4a9d22f0e85a1b2c3d4e5f6789",
		BundleHashShort: "9d4f2c1a8b7e3a05f1c6e9d28a4b1f3c",
		Patient:         "Buddy",
		PatientMeta:     "Canine · King Charles Spaniel",
		Owner:           "Sarah Nakamura",
		EncounterDate:   "2026-04-28 14:32 NZST",
		FormName:        "SOAP — Small Animal v3.2",
		VetOfRecord:     "Dr. Aroha Williams (VCNZ #VC-04412)",
		Artifacts: []v2.AuditPackArtifact{
			{Idx: 1, Title: "Signed clinical note (PDF)", What: "The clinician's final signed-off record for this encounter.", HashShort: "a47eb4dca18b"},
			{Idx: 2, Title: "Original audio recording", What: "Encrypted at rest, immutable; the source the AI extracted from.", HashShort: "6e9b359aa6b4"},
			{Idx: 3, Title: "Full transcript with confidence", What: "Deepgram Nova-3-medical output; 0.94 mean over 612 tokens.", HashShort: "18e7f6d979d6"},
			{Idx: 4, Title: "Evidence trace (field → source)", What: "Every extracted field linked to the transcript segment.", HashShort: "76734c50a07a"},
			{Idx: 5, Title: "Edit history", What: "3 edits between AI draft and sign-off.", HashShort: "d857c706407b"},
			{Idx: 6, Title: "Form snapshot", What: "Form template version active when signed.", HashShort: "2a3f8c91e004"},
			{Idx: 7, Title: "Policy satisfaction report", What: "Clause-by-clause check.", HashShort: "b17e44a8c901"},
		},
		SignedNoteBody:      notePlaceholder,
		Evidence: []v2.AuditPackEvidence{
			{Field: "Presenting complaint", Value: "Intermittent right hindlimb lameness, 4 days", Source: `<em>"so he's been a bit lame on his back right leg" — 00:14</em>`, Confidence: "0.97"},
			{Field: "Pain score (NRS)", Value: "3", Source: `<em>"I'd put him at a three out of ten" — 02:11</em>`, Confidence: "0.95"},
			{Field: "Drug · Meloxicam dose", Value: "0.1 mg/kg PO SID × 5 days", Source: `<em>"meloxicam, point one mig per kig" — 03:09</em>`, Confidence: "0.88"},
		},
		EvidenceFieldsCount: 14,
		EvidenceConfMean:    0.94,
		AudioLength:         "3:47",
		AudioTokens:         612,
		EditHistory: []v2.AuditPackEditEvent{
			{WhenPretty: "14:32:08", Tone: "default", Body: "<strong>AI draft created.</strong> Gemini 2.5 extracted 14 fields."},
			{WhenPretty: "14:48:21", Tone: "warn", Body: "<strong>Field reviewed:</strong> Recheck date 0.71 confidence (low)."},
			{WhenPretty: "15:08:12", Tone: "ok", Body: "<strong>Submitted &amp; signed.</strong> 14 system widgets materialised to ledger rows."},
		},
		PolicyClauses: []v2.AuditPackPolicyClause{
			{Policy: "NSAID Prescribing Policy v2.1", Clause: "§3.2", Requirement: "Body weight recorded within 14 days", Status: "Met", StatusTone: "ok"},
			{Policy: "NSAID Prescribing Policy v2.1", Clause: "§3.4", Requirement: "Hydration status recorded", Status: "Met", StatusTone: "ok"},
			{Policy: "NSAID Prescribing Policy v2.1", Clause: "§3.7", Requirement: "Owner counselled on side effects", Status: "Implicit", StatusTone: "warn"},
			{Policy: "Lameness Workup Standard v1.0", Clause: "§2.1", Requirement: "Cruciate ligament test documented", Status: "Met", StatusTone: "ok"},
		},
		PolicyAlignmentPct: 93,
	})
}

func init() {
	// Just to keep strings imported in case future fixtures need it.
	_ = strings.TrimSpace
}
