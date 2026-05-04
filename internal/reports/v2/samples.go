package v2

import "time"

// Per-doc-type sample fixtures used by the doc-theme preview endpoint
// to render the user's in-progress theme against realistic data without
// touching their real records. Same fixtures the smoke CLI uses.
//
// Mutating any field here changes what the designer's live preview
// shows — keep the data plausible and clinical.

// SampleSignedNoteFromTheme is the signed-note view fed to the
// notes.HTMLRenderer. Returned as a function (not a global var) so
// nullable pointer fields don't get aliased across requests.
func SampleSignedNoteFields() (formName, formVersion, noteID, submittedBy, clinicName, clinicAddr, clinicPhone string,
	submittedAt, visit time.Time,
	subjectName, species, breed, microchip string, weight float64,
) {
	return "SOAP — Small Animal", "3.2",
		"018e7f6d-aaaa-bbbb-cccc-000000000000",
		"Dr. Aroha Williams",
		"Riverside Veterinary Hospital",
		"14 Ponsonby Rd, Auckland 1011",
		"021 555 4127",
		time.Date(2026, 4, 28, 15, 8, 0, 0, time.UTC),
		time.Date(2026, 4, 28, 14, 32, 0, 0, time.UTC),
		"Buddy", "Canine", "King Charles Spaniel", "956000012345678",
		12.4
}

// SampleCDRegisterInput returns a representative CD register payload
// (NZ-vet flavor) for the doc-theme preview.
func SampleCDRegisterInput() CDRegisterInput {
	return CDRegisterInput{
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
					{WhenPretty: "02 Apr 14:32", OpKind: "RECEIVE", OpTone: "info", Subject: "Restock from supplier · Invoice #SUP-44102", QtyDelta: "+50.0", BalBefore: "82.0", BalAfter: "132.0", StaffShort: "A. Williams", WitnessShort: "H. Patel"},
					{WhenPretty: "03 Apr 09:14", OpKind: "ADMIN", OpTone: "ok", Subject: "Buddy (canine, MN) · Pre-op analgesia for OVH", QtyDelta: "−0.4", BalBefore: "132.0", BalAfter: "131.6", StaffShort: "A. Williams", WitnessShort: "H. Patel"},
					{WhenPretty: "06 Apr 08:45", OpKind: "DISCARD", OpTone: "danger", Subject: "Partial vial — drawn but not used", QtyDelta: "−0.3", BalBefore: "131.6", BalAfter: "131.3", StaffShort: "A. Williams", WitnessShort: "H. Patel"},
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
				Operations: []CDOperation{
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
	}
}

// SampleCDReconciliationInput — monthly reconciliation preview fixture.
func SampleCDReconciliationInput() CDReconciliationInput {
	return CDReconciliationInput{
		ClinicName:   "Riverside Veterinary Hospital",
		ClinicAddr:   "14 Ponsonby Rd, Auckland 1011",
		ClinicMeta:   "Class B/C licence #PHA-CLB-04412",
		PeriodLabel:  "April 2026",
		ReconciledOn: "2026-04-30 17:42 NZST",
		Drugs: []CDReconRow{
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
	}
}

// SampleIncidentReportInput — single-event incident preview fixture.
func SampleIncidentReportInput() IncidentReportInput {
	return IncidentReportInput{
		ClinicName:     "Sycamore House Care Home",
		ClinicAddr:     "42 Elm Lane, Bristol BS6 7XR",
		ClinicMeta:     "CQC location ID 1-247118331",
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
		ActionsTimeline: []IncidentTimelineItem{
			{WhenPretty: "03:42", Tone: "default", Body: "<strong>Found by HCA.</strong> Did not move resident. Made safe. Called nurse-in-charge."},
			{WhenPretty: "03:46", Tone: "default", Body: "<strong>Nurse on scene.</strong> J. Patel RN performed primary survey. GCS 15."},
			{WhenPretty: "03:51", Tone: "warn", Body: "<strong>999 ambulance called.</strong> Category 2 dispatch."},
			{WhenPretty: "11:47", Tone: "ok", Body: "<strong>CQC notification submitted.</strong> Ref CQC-NTF-3344821."},
		},
		Outcome24h: []IncidentOutcomeRow{
			{Label: "Hospital diagnosis", Value: "Right intracapsular fractured neck of femur (Garden III)."},
			{Label: "Surgical plan", Value: "Hemi-arthroplasty scheduled 30 Apr"},
			{Label: "Resident status", Value: "Hospitalised, stable, awaiting surgery"},
		},
		Notifications: []IncidentNotificationRow{
			{Authority: "CQC Reg 18(1)", Status: "Acknowledged", StatusTone: "ok"},
			{Authority: "Safeguarding (Bristol)", Status: "In progress", StatusTone: "warn"},
			{Authority: "RIDDOR (HSE)", Status: "Not applicable", StatusTone: "muted"},
		},
		RCA: &IncidentRCA{
			Method: "five_whys",
			Factors: []IncidentRCAFactor{
				{Factor: "Walking frame placement", Finding: "Frame within reach but resident did not use it.", Contributory: "primary", Tone: "danger"},
				{Factor: "Lighting", Finding: "Pathway light not on (motion-sensor failure).", Contributory: "contributory", Tone: "warn"},
				{Factor: "Medication review", Finding: "Zopiclone 3.75mg nocte commenced 18 Apr.", Contributory: "contributory", Tone: "warn"},
				{Factor: "Footwear", Finding: "Resident barefoot at time of fall.", Contributory: "contributory", Tone: "warn"},
				{Factor: "Staffing", Finding: "Night staff at planned ratio.", Contributory: "no", Tone: "muted"},
			},
		},
		RootCause: "Care plan review reduced night-time prompts without re-assessing fall risk.",
		ActionPlan: []IncidentActionItem{
			{Action: "Reinstate hourly night-time prompts", Owner: "Care plan lead", DuePretty: "30 Apr", Status: "Done", StatusTone: "ok"},
			{Action: "Repair pathway motion light", Owner: "Maintenance", DuePretty: "02 May", Status: "Done", StatusTone: "ok"},
			{Action: "Pharmacy review — zopiclone risk vs. benefit", Owner: "Clinical lead + GP", DuePretty: "07 May", Status: "In progress", StatusTone: "warn"},
		},
		ReportedBySigName: "Sarah Okonkwo",
		ReviewedBySigName: "Karen Thompson",
		BundleHash:        "4f9c2a8b1d3e7506f2a4b8c1d3e5f9a2",
	}
}

// SamplePainTrendInput — resident pain trend preview fixture (with charts).
func SamplePainTrendInput() PainTrendInput {
	return PainTrendInput{
		ClinicName:    "Sycamore House Care Home",
		ClinicAddr:    "42 Elm Lane, Bristol BS6 7XR",
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
		DailyLabels:   []string{"01 Apr", "05 Apr", "10 Apr", "15 Apr", "20 Apr", "23 Apr", "24 Apr", "25 Apr", "26 Apr", "27 Apr", "28 Apr", "29 Apr", "30 Apr"},
		DailyScores:   []int{2, 1, 2, 1, 2, 3, 5, 5, 6, 5, 7, 4, 3},
		DistLabels:    []string{"0-1 None", "2-3 Mild", "4-6 Mod", "7-10 Severe"},
		DistCounts:    []int{38, 16, 7, 1},
		WeekLabels:    []string{"Wk 14", "Wk 15", "Wk 16", "Wk 17"},
		PRNParacetamol: []int{1, 2, 2, 5},
		PRNOramorph:   []int{0, 0, 0, 3},
		HighScores: []PainHighScoreRow{
			{WhenPretty: "26 Apr 21:42", Score: 6, ScoreTone: "warn", Scale: "NRS", Context: "RN J. Patel · achy, can't get comfortable", Witness: "—", PRNGiven: "Paracetamol 1g · oramorph 5mg"},
			{WhenPretty: "28 Apr 03:45", Score: 7, ScoreTone: "danger", Scale: "PainAD", Context: "HCA S. Okonkwo · post-fall", Witness: "RN Patel", PRNGiven: "Paracetamol 1g (in incident timeline)"},
		},
		GeneratedBy: "Karen Thompson · Registered Manager",
		GeneratedOn: "2026-05-04 09:18",
		BundleHash:  "1c8f3e9d2a7b40c6e1f4a8d3b2c7e9f1",
	}
}

// SampleMARGridInput — monthly MAR preview fixture (landscape A4).
func SampleMARGridInput() MARGridInput {
	days, dow, weekend := MonthDays(2026, 4, time.UTC)
	cells := func(initials string, n int) []MARCell {
		out := make([]MARCell, n)
		for i := range out {
			out[i] = MARCell{Initials: initials, OutcomeCode: "given", Weekend: weekend[i]}
		}
		return out
	}
	holdAfter := func(base []MARCell, fromCol int) []MARCell {
		for i := fromCol; i < len(base); i++ {
			base[i] = MARCell{Initials: "H", OutcomeCode: "held", Weekend: weekend[i]}
		}
		return base
	}
	return MARGridInput{
		ClinicName:   "Sycamore House Care Home",
		ClinicAddr:   "42 Elm Lane, Bristol BS6 7XR",
		ClinicMeta:   "CQC location ID 1-247118331",
		ResidentName: "Mary White",
		ResidentMeta: "(age 87)",
		Room:         "Maple wing · Room 14",
		NHSNumber:    "488 219 3104",
		GP:           "Dr Patel · Sycamore Health Centre",
		Allergies:    "Penicillin (rash, 1972)",
		CarePlanRev:  "2026-03-15 · next 2026-06-15",
		Pharmacy:     "Boots Bristol Whiteladies (B-12)",
		PeriodLabel:  "April 2026",
		Days:         days,
		DOWLabels:    dow,
		IsWeekend:    weekend,
		Prescriptions: []MARPrescription{
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
		Notes:            `<ul style="margin: 4px 0 0 16px; font-size: 8.5pt; padding: 0;"><li><strong>28 Apr 04:00 →</strong> all medications on hospital hold (transferred to BRI post-fall).</li></ul>`,
		StaffInitialsKey: "SO = Sarah Okonkwo (HCA) · JP = Jodie Patel (RN, NMC #11A2839E)",
		ReviewedBy:       "Karen Thompson",
		BundleHash:       "8c4a1e7d3b9f02c5e8a1f4b7d9c2e5f8",
	}
}

// SampleAuditPackInput — audit-pack preview fixture (cover + signed
// note placeholder + evidence + edit history + policy check).
func SampleAuditPackInput() AuditPackInput {
	return AuditPackInput{
		ClinicName:      "Riverside Veterinary Hospital",
		ClinicAddr:      "14 Ponsonby Rd, Auckland 1011",
		ClinicMeta:      "VCNZ Registered Practice",
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
		Artifacts: []AuditPackArtifact{
			{Idx: 1, Title: "Signed clinical note (PDF)", What: "The clinician's final signed-off record.", HashShort: "a47eb4dca18b"},
			{Idx: 2, Title: "Original audio recording", What: "Encrypted at rest, immutable.", HashShort: "6e9b359aa6b4"},
			{Idx: 3, Title: "Full transcript with confidence", What: "Deepgram Nova-3-medical output.", HashShort: "18e7f6d979d6"},
			{Idx: 4, Title: "Evidence trace (field → source)", What: "Every extracted field linked to the transcript segment.", HashShort: "76734c50a07a"},
			{Idx: 5, Title: "Edit history", What: "3 edits between AI draft and sign-off.", HashShort: "d857c706407b"},
			{Idx: 6, Title: "Form snapshot", What: "Form template version active when signed.", HashShort: "2a3f8c91e004"},
			{Idx: 7, Title: "Policy satisfaction report", What: "Clause-by-clause check.", HashShort: "b17e44a8c901"},
		},
		SignedNoteBody: `<div class="callout"><div class="callout__title">Signed clinical note (embedded)</div>The signed note is included in the bundle as page 2; this section reproduces its body inline.</div>`,
		Evidence: []AuditPackEvidence{
			{Field: "Presenting complaint", Value: "Intermittent right hindlimb lameness, 4 days", Source: `<em>"so he's been a bit lame on his back right leg" — 00:14</em>`, Confidence: "0.97"},
			{Field: "Pain score (NRS)", Value: "3", Source: `<em>"I'd put him at a three out of ten" — 02:11</em>`, Confidence: "0.95"},
			{Field: "Drug · Meloxicam dose", Value: "0.1 mg/kg PO SID × 5 days", Source: `<em>"meloxicam, point one mig per kig" — 03:09</em>`, Confidence: "0.88"},
		},
		EvidenceFieldsCount: 14,
		EvidenceConfMean:    0.94,
		AudioLength:         "3:47",
		AudioTokens:         612,
		EditHistory: []AuditPackEditEvent{
			{WhenPretty: "14:32:08", Tone: "default", Body: "<strong>AI draft created.</strong> Gemini 2.5 extracted 14 fields."},
			{WhenPretty: "14:48:21", Tone: "warn", Body: "<strong>Field reviewed:</strong> Recheck date 0.71 confidence (low)."},
			{WhenPretty: "15:08:12", Tone: "ok", Body: "<strong>Submitted &amp; signed.</strong> 14 system widgets materialised."},
		},
		PolicyClauses: []AuditPackPolicyClause{
			{Policy: "NSAID Prescribing Policy v2.1", Clause: "§3.2", Requirement: "Body weight recorded within 14 days", Status: "Met", StatusTone: "ok"},
			{Policy: "NSAID Prescribing Policy v2.1", Clause: "§3.4", Requirement: "Hydration status recorded", Status: "Met", StatusTone: "ok"},
			{Policy: "NSAID Prescribing Policy v2.1", Clause: "§3.7", Requirement: "Owner counselled on side effects", Status: "Implicit", StatusTone: "warn"},
			{Policy: "Lameness Workup Standard v1.0", Clause: "§2.1", Requirement: "Cruciate ligament test documented", Status: "Met", StatusTone: "ok"},
		},
		PolicyAlignmentPct: 93,
	}
}
