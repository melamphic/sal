package v2

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"time"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// CDRegisterInput is what a caller passes to render the controlled-drug
// register for a clinic over a period. The package leaves data
// fetching to the caller (app.go assembles this from drugs +
// reconciliation services) so v2 stays repo-agnostic.
type CDRegisterInput struct {
	ClinicID    string
	ClinicName  string
	ClinicAddr  string
	ClinicMeta  string // e.g. "NZBN 9429048372910 · Class B/C licence #PHA-CLB-04412"

	PeriodLabel string    // "Q2 2026 · Apr–Jun"
	PeriodStart time.Time // for footer / hash inputs
	PeriodEnd   time.Time

	Drugs            []CDRegisterDrug
	ReconciliationOK bool   // green callout on cover when last reconcile clean
	ReconciledOn     string // pretty date — "2026-04-30"
	ReconciledByA    string // "Dr. A. Williams · VCNZ #VC-04412"
	ReconciledByB    string // "RVN H. Patel · NZVNA #VN-22810"
	NextDueOn        string // pretty date

	BundleHash string // first 32 chars of SHA-256 over the period dataset
}

// CDRegisterDrug is one drug in the register. The cover page renders
// every drug as an index row; each gets its own per-drug page.
type CDRegisterDrug struct {
	Class       string // "B" | "C"
	Name        string // "Methadone HCl"
	FormStrength string // "Injectable · 10 mg/ml"
	Storage      string // "CD Safe — Treatment Room"
	CatalogID    string // "vet.NZ.cd.methadone-10"
	BatchExp     string // "M82041 · Exp 2027-08"
	Unit         string // "ml" | "tab"

	Opening      float64
	ClosingBal   float64
	InTotal      float64
	OutTotal     float64

	Operations []CDOperation
}

// CDOperation is one row of the per-drug page table. Mirrors the
// drug_operations_log shape; quantity sign is encoded in OpKind.
type CDOperation struct {
	WhenPretty string // "02 Apr 14:32"
	OpKind     string // "RECEIVE" | "ADMIN" | "DISCARD" | "TRANSFER" | "ADJUST"
	OpTone     string // "ok" | "info" | "danger" — drives pill colour
	Subject    string // "Buddy (canine, MN) · Pre-op analgesia for OVH"
	QtyDelta   string // "+50.0 ml" | "−0.4 ml"
	BalBefore  string // "82.0"
	BalAfter   string // "132.0"
	StaffShort string // "A. Williams"
	WitnessShort string // "H. Patel"
}

// RenderCDRegister returns PDF bytes for the controlled-drug register.
func (r *Renderer) RenderCDRegister(ctx context.Context, in CDRegisterInput) ([]byte, error) {
	body, err := buildCDRegisterBody(in)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderCDRegister: %w", err)
	}
	theme, err := r.resolveTheme(ctx, in.ClinicID, "cd_register")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderCDRegister: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "cd_register",
		Title:   fmt.Sprintf("Controlled Drug Register — %s", in.PeriodLabel),
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderCDRegister: %w", err)
	}
	return out, nil
}

func buildCDRegisterBody(in CDRegisterInput) ([]byte, error) {
	funcs := commonFuncs()
	funcs["classTone"] = func(class string) string {
		if class == "B" {
			return "danger"
		}
		return "warn"
	}
	funcs["qtyFmt"] = func(v float64, unit string) string {
		return fmt.Sprintf("%.1f %s", v, unit)
	}
	funcs["qtyDeltaIn"] = func(v float64, _ string) string { return fmt.Sprintf("+%.1f", v) }
	funcs["qtyDeltaOut"] = func(v float64, _ string) string { return fmt.Sprintf("−%.1f", v) }

	tmpl, err := template.New("cd_register.html.tmpl").
		Funcs(funcs).
		ParseFS(reportFS, "templates/cd_register.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "cd_register.html.tmpl", in); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}
