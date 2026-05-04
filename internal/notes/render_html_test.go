package notes

import (
	"strings"
	"testing"
	"time"

	"github.com/melamphic/sal/internal/platform/config"
	"github.com/melamphic/sal/internal/platform/pdf"
)

func TestBuildSignedNoteHTML_AllSections(t *testing.T) {
	t.Parallel()

	addr := "14 Ponsonby Rd, Auckland 1011"
	phone := "021 555 4127"
	primary := "#0e7c66"

	dispName := "Buddy"
	species := "Canine"
	breed := "King Charles Spaniel"
	weight := 12.4
	chip := "956000012345678"
	visit := time.Date(2026, 4, 28, 14, 32, 0, 0, time.UTC)

	in := PDFInput{
		Theme: &DocTheme{
			Theme: &DocThemeTheme{PrimaryColor: &primary},
		},
		ClinicName:    "Riverside Veterinary Hospital",
		ClinicAddress: &addr,
		ClinicPhone:   &phone,
		FormName:      "SOAP — Small Animal",
		FormVersion:   "3.2",
		NoteID:        "018e7f6d-0000-0000-0000-000000000000",
		SubmittedAt:   time.Date(2026, 4, 28, 15, 8, 0, 0, time.UTC),
		SubmittedBy:   "Dr. Aroha Williams",
		Subject: &PDFSubject{
			DisplayName: &dispName,
			Species:     &species,
			Breed:       &breed,
			WeightKg:    &weight,
			Microchip:   &chip,
		},
		VisitDate: &visit,
		Fields: []PDFField{
			{Label: "Presenting complaint", Value: "Right hindlimb lameness, 4 days"},
			{Label: "Pain score (NRS)", Value: "3"},
			{
				Label:              "Drug op — Meloxicam dispense",
				SystemKind:         "drug_op",
				SystemReviewStatus: "approved",
				SystemSummary: []PDFSummaryItem{
					{Label: "Drug", Value: "Meloxicam 1.5 mg/ml"},
					{Label: "Quantity", Value: "7 ml"},
					{Label: "Operation", Value: "dispense"},
				},
			},
			{
				Label:         "Pain Score",
				SystemKind:    "pain_score",
				SystemPending: true,
				SystemSummary: []PDFSummaryItem{
					{Label: "Score", Value: "3"},
					{Label: "Scale", Value: "nrs"},
				},
			},
		},
	}

	body, err := buildSignedNoteHTML(in)
	if err != nil {
		t.Fatalf("buildSignedNoteHTML: %v", err)
	}
	got := string(body)

	for _, want := range []string{
		"Riverside Veterinary Hospital",
		"021 555 4127",
		"SOAP — Small Animal",
		"Buddy",
		"12.4 kg",
		"Right hindlimb lameness",
		"syscard syscard--drug_op",
		"Witness signed",
		"syscard syscard--pain_score",
		"AI-suggested · pending clinician confirmation",
		"018e7f6d", // short note id
		"2026-04-28 15:08",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in rendered body", want)
		}
	}
}

func TestSignedNoteViewModel_NoSubject(t *testing.T) {
	t.Parallel()
	in := PDFInput{
		ClinicName:  "Cedar Creek",
		FormName:    "Encounter",
		SubmittedAt: time.Now(),
	}
	d := signedNoteViewModel(in)
	if d.Subject {
		t.Errorf("Subject should be false when input has nil Subject")
	}
	if d.ClinicInitials != "CC" {
		t.Errorf("Initials = %q, want CC", d.ClinicInitials)
	}
}

// Smoke test that NewHTMLRenderer + buildSignedNoteHTML stitch
// together without errors. The actual Gotenberg call is covered by
// pdf.RenderHTML's own tests; here we just verify the body builder.
func TestRenderNoteAsPDF_ComposesInputCorrectly(t *testing.T) {
	t.Parallel()
	r := NewHTMLRenderer(pdf.New(&config.Config{
		GotenbergURL:     "http://unreachable",
		GotenbergTimeout: time.Second,
	}))
	if r == nil {
		t.Fatal("NewHTMLRenderer returned nil")
	}
	body, err := buildSignedNoteHTML(PDFInput{
		ClinicName:  "x",
		FormName:    "x",
		SubmittedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("buildSignedNoteHTML: %v", err)
	}
	if !strings.Contains(string(body), "Signed Clinical Note") {
		t.Errorf("body missing eyebrow")
	}
}
