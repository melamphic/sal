// Command smoke-pdf renders a stub signed clinical note via the
// new HTML+Gotenberg pipeline and writes the PDF to disk. Used to
// verify the pipeline end-to-end against a running Gotenberg sidecar
// without needing a full database round-trip.
//
// Usage:
//
//	make dev    # starts the gotenberg sidecar (among others)
//	go run ./cmd/smoke-pdf > /tmp/smoke.pdf
//	open /tmp/smoke.pdf
//
// Anything but the happy path exits non-zero with a message on stderr.
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
)

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
	r := notes.NewHTMLRenderer(pdf.New(cfg))

	addr := "14 Ponsonby Rd, Auckland 1011"
	phone := "021 555 4127"
	primary := "#0e7c66"
	dispName := "Buddy"
	species := "Canine"
	breed := "King Charles Spaniel"
	weight := 12.4
	chip := "956000012345678"
	visit := time.Date(2026, 4, 28, 14, 32, 0, 0, time.UTC)

	in := notes.PDFInput{
		Theme: &notes.DocTheme{
			Theme: &notes.DocThemeTheme{PrimaryColor: &primary},
		},
		ClinicName:    "Riverside Veterinary Hospital",
		ClinicAddress: &addr,
		ClinicPhone:   &phone,
		FormName:      "SOAP — Small Animal",
		FormVersion:   "3.2",
		NoteID:        "018e7f6d-aaaa-bbbb-cccc-000000000000",
		SubmittedAt:   time.Date(2026, 4, 28, 15, 8, 0, 0, time.UTC),
		SubmittedBy:   "Dr. Aroha Williams",
		Subject: &notes.PDFSubject{
			DisplayName: &dispName,
			Species:     &species,
			Breed:       &breed,
			WeightKg:    &weight,
			Microchip:   &chip,
		},
		VisitDate: &visit,
		Fields: []notes.PDFField{
			{Label: "Presenting complaint", Value: "Right hindlimb lameness, intermittent, 4 days. No known trauma. Eating + drinking normally."},
			{Label: "BAR / hydration", Value: "Bright, alert, responsive. Hydrated. MM pink, CRT <2s."},
			{Label: "HR / RR / Temp", Value: "HR 96 bpm · RR 24 rpm · T 38.6°C"},
			{Label: "Body condition", Value: "BCS 5/9 (ideal)"},
			{Label: "Orthopaedic exam", Value: "Grade 2/5 lameness right hindlimb at trot. Mild discomfort on hyperextension of right stifle. Drawer sign negative. Cranial tibial thrust negative. No effusion."},
			{Label: "Differential diagnoses", Value: "1. Soft-tissue strain right stifle (most likely)\n2. Early partial cruciate ligament tear (cannot rule out)\n3. Patellar luxation (negative on exam)"},
			{
				Label:              "Drug op — Meloxicam dispense",
				SystemKind:         "drug_op",
				SystemReviewStatus: "approved",
				SystemSummary: []notes.PDFSummaryItem{
					{Label: "Drug", Value: "Meloxicam 1.5 mg/ml oral suspension"},
					{Label: "Quantity", Value: "7 ml"},
					{Label: "Operation", Value: "dispense"},
					{Label: "Dose", Value: "0.1 mg/kg PO SID × 5 days"},
					{Label: "Batch / Exp", Value: "C82041 · Aug 2027"},
				},
			},
			{
				Label:         "Pain Score",
				SystemKind:    "pain_score",
				SystemPending: true,
				SystemSummary: []notes.PDFSummaryItem{
					{Label: "Score", Value: "3 / 10"},
					{Label: "Scale", Value: "NRS"},
					{Label: "Method", Value: "clinician observation"},
				},
			},
			{Label: "Plan", Value: "Strict rest × 14 days, lead walks only. Recheck Day 7 (2026-05-05) if not improved — radiographs ± referral."},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pdfBytes, err := r.RenderNoteAsPDF(ctx, in)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if !strings.HasPrefix(string(pdfBytes[:5]), "%PDF-") {
		return fmt.Errorf("output does not look like a PDF (first 16 bytes: %q)", string(pdfBytes[:16]))
	}
	if _, err := os.Stdout.Write(pdfBytes); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stderr, "smoke-pdf: %d bytes written to stdout\n", len(pdfBytes))
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
