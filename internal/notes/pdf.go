package notes

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// PDFInput holds all data needed to build a branded clinical note PDF.
type PDFInput struct {
	ClinicName  string
	ClinicColor string // hex color for header bar, e.g. "#2563EB"
	FormName    string
	FormVersion string
	Fields      []PDFField
	SubmittedAt time.Time
	SubmittedBy string // staff name
	NoteID      string
}

// PDFField is a label/value pair for rendering in the PDF body.
type PDFField struct {
	Label string
	Value string
}

// BuildNotePDF generates a branded PDF with clinic header, fields, and audit footer.
func BuildNotePDF(input PDFInput) (*bytes.Buffer, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 25) // leave room for audit footer

	color := input.ClinicColor
	if color == "" {
		color = "#2563EB" // default blue
	}
	r, g, b := hexToRGB(color)

	pdf.SetFooterFunc(func() {
		pdf.SetY(-20)
		pdf.SetFont("Helvetica", "", 7)
		pdf.SetTextColor(100, 100, 100)
		footer := fmt.Sprintf("Form: %s v%s | Submitted: %s | Approved by: %s | Note: %s | Page %d",
			input.FormName,
			input.FormVersion,
			input.SubmittedAt.UTC().Format(time.RFC3339),
			input.SubmittedBy,
			input.NoteID,
			pdf.PageNo(),
		)
		pdf.CellFormat(0, 8, footer, "T", 0, "C", false, 0, "")
	})

	pdf.AddPage()

	// ── Header bar ────────────────────────────────────────────────────────────
	pdf.SetFillColor(int(r), int(g), int(b))
	pdf.Rect(0, 0, 210, 18, "F")
	pdf.SetFont("Helvetica", "B", 14)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetXY(10, 4)
	pdf.CellFormat(190, 10, input.ClinicName, "", 0, "L", false, 0, "")

	// ── Form title ────────────────────────────────────────────────────────────
	pdf.SetY(24)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(0, 8, fmt.Sprintf("%s (v%s)", input.FormName, input.FormVersion), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	// ── Fields ────────────────────────────────────────────────────────────────
	for _, f := range input.Fields {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(0, 6, f.Label, "", 1, "L", false, 0, "")

		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(0, 0, 0)
		pdf.MultiCell(0, 5, f.Value, "", "L", false)
		pdf.Ln(3)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("notes.BuildNotePDF: %w", err)
	}
	return &buf, nil
}

// hexToRGB converts a hex color string to RGB components.
func hexToRGB(hex string) (uint8, uint8, uint8) {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return 37, 99, 235 // fallback blue
	}
	var r, g, b uint8
	_, _ = fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}
