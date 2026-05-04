package notes

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode"

	"github.com/melamphic/sal/internal/platform/pdf"
)

//go:embed templates/signed_note.html.tmpl
var notesTemplatesFS embed.FS

// HTMLRenderer renders a clinical note PDF via the new HTML+Gotenberg
// pipeline. Receives a fully-built PDFInput (the existing input shape
// is fine — same data, different rendering path) and returns PDF
// bytes.
//
// Today this lives alongside BuildNotePDF (fpdf path) so we can switch
// the worker over and roll back if needed. Once the HTML path is
// proven (P1-H smoke test), the fpdf BuildNotePDF gets deleted in
// P3-Q.
type HTMLRenderer struct {
	pdf *pdf.Renderer
}

// NewHTMLRenderer wires the platform PDF renderer into the notes
// package. The constructor mirrors how other notes-package types are
// built (storage, repository — see PDFRenderer for the legacy shape).
func NewHTMLRenderer(p *pdf.Renderer) *HTMLRenderer {
	return &HTMLRenderer{pdf: p}
}

// RenderNoteAsPDF templates the note body to HTML and ships it through
// Gotenberg. Returns the PDF byte stream.
//
// The PDFInput shape is reused verbatim from the legacy fpdf path so
// jobs.go can swap one call for the other without re-plumbing
// upstream services.
func (r *HTMLRenderer) RenderNoteAsPDF(ctx context.Context, input PDFInput) ([]byte, error) {
	body, err := buildSignedNoteHTML(input)
	if err != nil {
		return nil, fmt.Errorf("notes.RenderNoteAsPDF: build body: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "signed_note",
		Title:   fmt.Sprintf("Signed Note · %s", input.FormName),
		Lang:    "en",
		Body:    string(body),
		Theme:   input.Theme,
	})
	if err != nil {
		return nil, fmt.Errorf("notes.RenderNoteAsPDF: %w", err)
	}
	return out, nil
}

// signedNoteData is the html/template view-model. Keeping the
// derivation in a typed struct (rather than passing PDFInput
// directly) means the template stays simple and we centralise
// nullable-pointer coercion + formatting.
type signedNoteData struct {
	ClinicName        string
	ClinicNameSafe    string // CSS-content-safe (no quote chars)
	ClinicInitials    string
	ContactLine       string
	FormName          string
	FormVersion       string
	SubmittedAtPretty string
	SubmittedBy       string
	NoteIDShort       string

	Subject     bool
	SubjectRows []signedNoteMeta

	Fields []signedNoteField
}

// signedNoteMeta is one row in the patient-identity card.
type signedNoteMeta struct {
	Label string
	Value string
}

// signedNoteField is one item in the body. Kind="system" means the
// renderer should produce a typed card; "field" means a plain
// label/value row.
type signedNoteField struct {
	Kind            string // field | system
	Label           string
	Value           string
	SystemKind      string // drug_op | consent | incident | pain_score
	SystemPending   bool
	SummaryItems    []signedNoteMeta
	ReviewBadge     string
	ReviewBadgeTone string // ok | warn | danger | info | muted
}

func buildSignedNoteHTML(in PDFInput) ([]byte, error) {
	tmpl, err := template.New("signed_note.html.tmpl").ParseFS(
		notesTemplatesFS, "templates/signed_note.html.tmpl",
	)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	data := signedNoteViewModel(in)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "signed_note.html.tmpl", data); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}

func signedNoteViewModel(in PDFInput) signedNoteData {
	d := signedNoteData{
		ClinicName:        nonEmpty(in.ClinicName, "Clinic"),
		ClinicNameSafe:    cssSafe(nonEmpty(in.ClinicName, "Clinic")),
		ClinicInitials:    initials(nonEmpty(in.ClinicName, "Clinic")),
		ContactLine:       contactLine(in),
		FormName:          nonEmpty(in.FormName, "Clinical Note"),
		FormVersion:       nonEmpty(in.FormVersion, "1"),
		SubmittedAtPretty: prettyDateTime(in.SubmittedAt),
		SubmittedBy:       nonEmpty(in.SubmittedBy, ""),
		NoteIDShort:       shortID(in.NoteID),
	}

	if in.Subject != nil {
		d.Subject = true
		d.SubjectRows = subjectRows(in.Subject, in.SystemHeader, in.VisitDate)
	}

	d.Fields = make([]signedNoteField, 0, len(in.Fields))
	for _, f := range in.Fields {
		if f.SystemKind != "" {
			d.Fields = append(d.Fields, systemFieldRow(f))
			continue
		}
		d.Fields = append(d.Fields, signedNoteField{
			Kind:  "field",
			Label: f.Label,
			Value: f.Value,
		})
	}
	return d
}

func systemFieldRow(f PDFField) signedNoteField {
	row := signedNoteField{
		Kind:          "system",
		Label:         f.Label,
		SystemKind:    f.SystemKind,
		SystemPending: f.SystemPending,
		SummaryItems:  make([]signedNoteMeta, 0, len(f.SystemSummary)),
	}
	for _, it := range f.SystemSummary {
		row.SummaryItems = append(row.SummaryItems, signedNoteMeta(it))
	}
	switch f.SystemReviewStatus {
	case "approved":
		row.ReviewBadge = "Witness signed"
		row.ReviewBadgeTone = "ok"
	case "pending":
		row.ReviewBadge = "Awaiting witness"
		row.ReviewBadgeTone = "warn"
	case "challenged":
		row.ReviewBadge = "Challenged"
		row.ReviewBadgeTone = "danger"
	}
	return row
}

func subjectRows(s *PDFSubject, sh *SystemHeaderConfigForPDF, visit *time.Time) []signedNoteMeta {
	out := []signedNoteMeta{}
	add := func(label, value string) {
		if value == "" {
			return
		}
		out = append(out, signedNoteMeta{Label: label, Value: value})
	}
	add("Patient", strDeref(s.DisplayName))
	if s.Species != nil || s.Breed != nil {
		bits := []string{strDeref(s.Species), strDeref(s.Breed)}
		add("Species / breed", joinNonEmpty(bits, " · "))
	}
	add("DOB", strDeref(s.DOB))
	add("Sex", strDeref(s.Sex))
	if s.WeightKg != nil {
		add("Weight", fmt.Sprintf("%.1f kg", *s.WeightKg))
	}
	add("Microchip", strDeref(s.Microchip))
	add("Room", strDeref(s.Room))
	add("NHI", strDeref(s.NHINumber))
	add("Medicare", strDeref(s.MedicareNumber))
	add("External ID", strDeref(s.ExternalID))
	if visit != nil && !visit.IsZero() {
		add("Encounter", visit.Format("2006-01-02 15:04 MST"))
	}
	if sh != nil && len(sh.Fields) > 0 {
		// SystemHeader.Fields is a curated whitelist — when set, only
		// those fields render. Otherwise the default order above wins.
		// (Filter not applied today — caller pre-curates upstream.)
		_ = sh
	}
	return out
}

// ── Small helpers ───────────────────────────────────────────────────────────

func contactLine(in PDFInput) string {
	bits := []string{}
	if in.ClinicAddress != nil && *in.ClinicAddress != "" {
		bits = append(bits, *in.ClinicAddress)
	}
	if in.ClinicPhone != nil && *in.ClinicPhone != "" {
		bits = append(bits, *in.ClinicPhone)
	}
	if in.ClinicEmail != nil && *in.ClinicEmail != "" {
		bits = append(bits, *in.ClinicEmail)
	}
	return strings.Join(bits, " · ")
}

func initials(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "C"
	}
	out := []rune{}
	for _, p := range parts {
		for _, r := range p {
			if unicode.IsLetter(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) >= 2 {
			break
		}
	}
	if len(out) == 0 {
		return "C"
	}
	return string(out)
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func prettyDateTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04 MST")
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func joinNonEmpty(parts []string, sep string) string {
	out := []string{}
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func cssSafe(s string) string {
	r := strings.NewReplacer(`"`, `\"`, `\`, `\\`)
	return r.Replace(s)
}
