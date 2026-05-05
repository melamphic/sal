package v2

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// ComplianceLogInput is the shared input shape for every "list of
// events" compliance report — records audit, incidents log, sentinel
// events log, aged-care evidence pack, HIPAA disclosure log, DEA
// biennial inventory. Each of these is structurally a styled cover
// page + one or more tables; bespoke per-type templates will replace
// this when each report's specific layout requirements firm up.
//
// Sections render in order; each is a titled table with column
// headers + rows. Empty sections render an italic "no rows" placeholder
// so the report doesn't have hidden gaps.
type ComplianceLogInput struct {
	ClinicID    string
	Clinic      pdf.ClinicInfo
	ReportID    string
	ReportTitle string // "Records Activity Audit", "Incidents Log", etc.
	Eyebrow     string // small-caps cover label
	Description string // 1–3 sentence cover description
	PeriodStart time.Time
	PeriodEnd   time.Time
	GeneratedAt time.Time
	Vertical    string
	Country     string
	Regulator   string // optional: "CQC" / "DEA" / etc — surfaces as a pill on cover

	// One styled section per list. Audit packs render 1; richer
	// reports render 3–4. Empty Sections list = cover-only PDF
	// (the report still ships, just signals "no activity").
	Sections []ComplianceLogSection
}

// ComplianceLogSection is one titled table on the report.
type ComplianceLogSection struct {
	Title    string
	Hint     string // optional sub-line under the title
	Columns  []ComplianceLogColumn
	Rows     []ComplianceLogRow
	EmptyMsg string // shown when Rows is empty; defaults to "No rows in period."
}

// ComplianceLogColumn defines header + optional per-column styling.
type ComplianceLogColumn struct {
	Label string
	Width string // optional CSS width like "90px"; empty = auto
	Align string // "left" | "right" | "center"; empty = left
}

// ComplianceLogRow holds one row's cells. Cells len must equal
// Columns len; missing cells render "—".
type ComplianceLogRow struct {
	Cells []ComplianceLogCell
	// Optional pill: when Status is set, the LAST cell renders as a
	// coloured pill instead of plain text. Tone drives the colour.
	StatusTone string
}

// ComplianceLogCell is one cell — Value is plain text; Pill turns
// the cell into a coloured pill (use sparingly, e.g. status columns).
type ComplianceLogCell struct {
	Value string
	Pill  string // "ok" | "warn" | "danger" | "info" | ""
}

// RenderComplianceLog returns PDF bytes for a generic styled compliance
// log. Same Gotenberg pipeline as every other v2 report — picks up
// the clinic's saved doc theme via the resolved Clinic.
func (r *Renderer) RenderComplianceLog(ctx context.Context, in ComplianceLogInput) ([]byte, error) {
	theme, err := r.resolveTheme(ctx, in.ClinicID, "audit_pack")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderComplianceLog: %w", err)
	}
	clinic := pdf.ResolveClinicFromTheme(in.Clinic, theme)
	body, err := buildComplianceLogBody(in, clinic)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderComplianceLog: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "audit_pack",
		Title:   fmt.Sprintf("%s — %s", in.ReportTitle, periodLabelOf(in.PeriodStart, in.PeriodEnd)),
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
		Clinic:  clinic,
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderComplianceLog: %w", err)
	}
	return out, nil
}

func periodLabelOf(start, end time.Time) string {
	return fmt.Sprintf("%s → %s",
		start.UTC().Format("02 Jan 2006"),
		end.UTC().Format("02 Jan 2006"),
	)
}

type complianceLogView struct {
	ComplianceLogInput
	Clinic       pdf.ClinicInfo
	GeneratedStr string
	PeriodStr    string
}

func buildComplianceLogBody(in ComplianceLogInput, clinic pdf.ClinicInfo) ([]byte, error) {
	funcs := commonFuncs()
	funcs["headerInfo"] = func(eyebrow, title, meta string) pdf.HeaderInfo {
		return pdf.HeaderInfo{Clinic: clinic, Eyebrow: eyebrow, Title: title, Meta: meta}
	}
	funcs["footerInfo"] = func(subject, pageLabel, footnote string) pdf.FooterInfo {
		return pdf.FooterInfo{
			Clinic:     clinic,
			Subject:    subject,
			BundleHash: shortReportID(in.ReportID),
			PageLabel:  pageLabel,
			Footnote:   footnote,
		}
	}

	view := complianceLogView{
		ComplianceLogInput: in,
		Clinic:             clinic,
		GeneratedStr:       in.GeneratedAt.UTC().Format("02 Jan 2006 15:04 UTC"),
		PeriodStr:          periodLabelOf(in.PeriodStart, in.PeriodEnd),
	}

	tmpl, err := pdf.NewReportTemplate("compliance_log.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("partials: %w", err)
	}
	tmpl, err = tmpl.Funcs(funcs).
		ParseFS(reportFS, "templates/compliance_log.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "compliance_log.html.tmpl", view); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderPlaceholderPDF returns a tiny styled "this report needs more
// inputs" PDF — used by the preview path for per-resident reports
// (MAR / Pain Trend) when no resident has been picked yet. Same
// pipeline as the others, just one cover page with the supplied
// message body.
func (r *Renderer) RenderPlaceholderPDF(ctx context.Context, clinicID, title, message string, clinicInfo pdf.ClinicInfo) ([]byte, error) {
	theme, err := r.resolveTheme(ctx, clinicID, "audit_pack")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderPlaceholderPDF: %w", err)
	}
	clinic := pdf.ResolveClinicFromTheme(clinicInfo, theme)
	in := ComplianceLogInput{
		ClinicID:    clinicID,
		Clinic:      clinic,
		ReportID:    "preview",
		ReportTitle: title,
		Eyebrow:     "Preview unavailable",
		Description: message,
		PeriodStart: time.Now().AddDate(0, 0, -7),
		PeriodEnd:   time.Now(),
		GeneratedAt: time.Now().UTC(),
		Sections:    nil,
	}
	body, err := buildComplianceLogBody(in, clinic)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderPlaceholderPDF: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "audit_pack",
		Title:   title,
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
		Clinic:  clinic,
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderPlaceholderPDF: %w", err)
	}
	return out, nil
}
