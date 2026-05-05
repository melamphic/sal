package v2

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// ComplianceAuditPackInput drives the period-wide regulator-facing
// audit pack — the one a CQC / VCNZ / DEA inspector reads. Distinct
// from the per-note AuditPackInput which bundles ONE clinical note's
// evidence trail; this one rolls up an entire reporting period.
//
// Header chrome is rendered from Clinic via the shared `doc-header`
// partial so the doc-theme designer's brand mark + colors flow
// through to every page.
type ComplianceAuditPackInput struct {
	ClinicID string
	Clinic   pdf.ClinicInfo

	ReportID    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	GeneratedAt time.Time
	Vertical    string // "veterinary" | "aged_care" | …
	Country     string // "NZ" | "UK" | …

	// Records-activity counts keyed by status — one row per status
	// in the Notes table (extracting / draft / submitted / failed).
	NoteCounts map[string]int

	// Drug operations during the period — used to render the highlights
	// table. Only the first ~50 are shown; the audit pack is a summary.
	DrugOps []ComplianceDrugOp

	// Reconciliations completed during the period.
	Reconciliations []ComplianceReconciliation
}

// ComplianceDrugOp is one row in the controlled-drug highlights table.
// Mirrors reports.DrugOpView but keeps v2 free of cross-package types.
type ComplianceDrugOp struct {
	When           string // pre-formatted date+time
	Drug           string // shelf label, e.g. "Methadone HCl 10 mg/ml"
	Operation      string // administer | dispense | discard | …
	OperationTone  string // ok | warn | danger | info
	Quantity       string // "0.4 ml" — pre-formatted
	BalanceAfter   string // "131.6 ml" — pre-formatted
	Subject        string // "Buddy (canine)" or "—"
	AdministeredBy string
	WitnessedBy    string // "—" when not required
}

// ComplianceReconciliation is one row in the reconciliations table.
type ComplianceReconciliation struct {
	Drug              string
	Period            string // "01 Apr → 30 Apr"
	Physical          string
	Ledger            string
	DiscrepancyDelta  string // "+0.0" / "−0.4"
	Status            string // "Clean" | "Explained" | "Discrepancy"
	StatusTone        string // "ok" | "warn" | "danger"
	PrimarySignedBy   string
	SecondarySignedBy string
	Explanation       string
}

// RenderComplianceAuditPack returns PDF bytes for the period-wide
// audit pack. Goes through the same Gotenberg pipeline as every other
// V1 report — picks up the clinic's saved doc theme automatically.
func (r *Renderer) RenderComplianceAuditPack(ctx context.Context, in ComplianceAuditPackInput) ([]byte, error) {
	theme, err := r.resolveTheme(ctx, in.ClinicID, "audit_pack")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderComplianceAuditPack: %w", err)
	}
	clinic := pdf.ResolveClinicFromTheme(in.Clinic, theme)
	body, err := buildComplianceAuditPackBody(in, clinic)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderComplianceAuditPack: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "audit_pack",
		Title:   fmt.Sprintf("Compliance Audit Pack — %s", in.PeriodLabel()),
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
		Clinic:  clinic,
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderComplianceAuditPack: %w", err)
	}
	return out, nil
}

// PeriodLabel formats the period for the title.
func (in ComplianceAuditPackInput) PeriodLabel() string {
	return fmt.Sprintf("%s → %s",
		in.PeriodStart.UTC().Format("02 Jan 2006"),
		in.PeriodEnd.UTC().Format("02 Jan 2006"),
	)
}

type complianceAuditPackView struct {
	ComplianceAuditPackInput
	Clinic       pdf.ClinicInfo
	GeneratedStr string
	PeriodStr    string
	NoteRows     []noteCountRow
	HasDrugOps   bool
	HasRecons    bool
}

type noteCountRow struct {
	Label string
	Count int
	Tone  string
}

func buildComplianceAuditPackBody(in ComplianceAuditPackInput, clinic pdf.ClinicInfo) ([]byte, error) {
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

	view := complianceAuditPackView{
		ComplianceAuditPackInput: in,
		Clinic:                   clinic,
		GeneratedStr:             in.GeneratedAt.UTC().Format("02 Jan 2006 15:04 UTC"),
		PeriodStr:                in.PeriodLabel(),
		NoteRows:                 noteCountRowsFor(in.NoteCounts),
		HasDrugOps:               len(in.DrugOps) > 0,
		HasRecons:                len(in.Reconciliations) > 0,
	}

	tmpl, err := pdf.NewReportTemplate("compliance_audit_pack.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("partials: %w", err)
	}
	tmpl, err = tmpl.Funcs(funcs).
		ParseFS(reportFS, "templates/compliance_audit_pack.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "compliance_audit_pack.html.tmpl", view); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}

// noteCountRowsFor projects a map[status]count into ordered rows so
// the template renders status buckets in a stable order regardless of
// Go map iteration order. Submitted shows green (ok); failed red.
func noteCountRowsFor(counts map[string]int) []noteCountRow {
	keys := []string{"submitted", "draft", "extracting", "failed"}
	out := make([]noteCountRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, noteCountRow{
			Label: prettyNoteStatus(k),
			Count: counts[k],
			Tone:  toneForNoteStatus(k),
		})
	}
	return out
}

func prettyNoteStatus(s string) string {
	switch s {
	case "submitted":
		return "Signed & submitted"
	case "draft":
		return "In draft"
	case "extracting":
		return "Extracting"
	case "failed":
		return "Failed"
	default:
		return s
	}
}

func toneForNoteStatus(s string) string {
	switch s {
	case "submitted":
		return "ok"
	case "failed":
		return "danger"
	default:
		return ""
	}
}

func shortReportID(id string) string {
	if len(id) >= 12 {
		return id[:12]
	}
	return id
}
