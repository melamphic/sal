package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// CDReconciliationInput drives the monthly CD reconciliation report.
// Caller assembles from drugs.Service + reconciliation repo.
type CDReconciliationInput struct {
	ClinicID   string
	ClinicName string
	ClinicAddr string
	ClinicMeta string

	PeriodLabel  string // "April 2026"
	ReconciledOn string // "2026-04-30 17:42 NZST"

	Drugs []CDReconRow

	// 12-month trend — discrepancy counts per month, aligned with TrendLabels.
	TrendLabels        []string  // e.g. ["May","Jun",…,"Apr"]
	TrendDiscrepancies []int     // discrepancies per month
	TrendDrugCount     []int     // drugs counted per month (overlay)

	PrimaryReconciler   string
	SecondaryReconciler string
	BundleHash          string
}

// CDReconRow is one drug's reconciliation outcome.
type CDReconRow struct {
	Drug        string  // "Methadone HCl 10 mg/ml"
	Ledger      string  // "84.4 ml"
	Physical    string  // "84.4 ml"
	Delta       float64 // 0.0
	DeltaPct    float64 // 0.00
	Status      string  // "Clean" | "Explained" | "Unexplained"
	StatusTone  string  // "ok" | "warn" | "danger"
	Notes       string  // free text — explanation when not clean
}

// RenderCDReconciliation returns PDF bytes for the monthly CD
// reconciliation report including the discrepancy chart + trend.
func (r *Renderer) RenderCDReconciliation(ctx context.Context, in CDReconciliationInput) ([]byte, error) {
	body, head, err := buildCDReconciliationBody(in)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderCDReconciliation: %w", err)
	}
	theme, err := r.resolveTheme(ctx, in.ClinicID, "cd_reconciliation")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderCDReconciliation: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType:   "cd_reconciliation",
		Title:     fmt.Sprintf("CD Reconciliation — %s", in.PeriodLabel),
		Lang:      "en",
		Body:      string(body),
		ExtraHead: head,
		Theme:     theme,
		Options: pdf.Options{
			// Wait until both Chart.js renders flag complete — set
			// in chart-init scripts via window.__chartsReady = true.
			WaitForExpression: "window.__chartsReady === true",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderCDReconciliation: %w", err)
	}
	return out, nil
}

func buildCDReconciliationBody(in CDReconciliationInput) (body []byte, head string, err error) {
	funcs := commonFuncs()
	funcs["jsonArr"] = func(v any) (template.JS, error) {
		b, jErr := json.Marshal(v)
		if jErr != nil {
			return "", fmt.Errorf("jsonArr: %w", jErr)
		}
		return template.JS(b), nil
	}

	tmpl, parseErr := template.New("cd_reconciliation.html.tmpl").
		Funcs(funcs).
		ParseFS(reportFS, "templates/cd_reconciliation.html.tmpl")
	if parseErr != nil {
		return nil, "", fmt.Errorf("parse: %w", parseErr)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "cd_reconciliation.html.tmpl", in); err != nil {
		return nil, "", fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), chartJSCDN(), nil
}
