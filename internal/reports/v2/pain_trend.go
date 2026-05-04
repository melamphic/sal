package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// PainTrendInput drives the per-resident / per-patient pain trend
// report. Caller assembles from pain.Service + drug ops (PRN
// correlation) over a chosen window.
type PainTrendInput struct {
	ClinicID   string
	ClinicName string
	ClinicAddr string

	SubjectName    string // "Mary White (age 87)"
	SubjectRoom    string // "Maple wing · Room 14"
	SubjectMeta    string // "Residential — moderate dementia"

	PeriodLabel string // "April 2026"
	WindowDays  int    // 30
	Assessments int
	ScalesUsed  string // "PainAD (28) · NRS (34)"

	MeanScore     float64
	PeakScore     int
	PeakWhen      string // "28 Apr 03:45 — pre-fall"
	PRNDaysPct    int    // 23
	PRNDaysCount  int    // 7
	WitnessedHigh int    // 8 — assessments score≥4 with witness

	// Daily series — labels and values aligned. Values 0-10 normalised.
	DailyLabels []string // e.g. ["01 Apr","02 Apr",…]
	DailyScores []int

	// Distribution buckets.
	DistLabels []string // ["0-1 None","2-3 Mild","4-6 Mod","7-10 Severe"]
	DistCounts []int

	// PRN doses by week, two series.
	WeekLabels      []string // ["Wk 14","Wk 15",…]
	PRNParacetamol  []int
	PRNOramorph     []int

	// Top-of-range assessments (scores ≥5) — small detail table.
	HighScores []PainHighScoreRow

	GeneratedBy string // "Karen Thompson · Registered Manager · …"
	GeneratedOn string // "2026-05-04 09:18"

	BundleHash string
}

// PainHighScoreRow is one row of the "scores ≥5" detail table.
type PainHighScoreRow struct {
	WhenPretty string
	Score      int
	ScoreTone  string // warn | danger
	Scale      string
	Context    string
	Witness    string // "RN Patel" or "—"
	PRNGiven   string // "Paracetamol 1g · oramorph 5mg" or "—"
}

func (r *Renderer) RenderPainTrend(ctx context.Context, in PainTrendInput) ([]byte, error) {
	body, head, err := buildPainTrendBody(in)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderPainTrend: %w", err)
	}
	theme, err := r.resolveTheme(ctx, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderPainTrend: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType:   "pain_trend",
		Title:     fmt.Sprintf("Pain Trend — %s · %s", in.SubjectName, in.PeriodLabel),
		Lang:      "en",
		Body:      string(body),
		ExtraHead: head,
		Theme:     theme,
		Options: pdf.Options{
			WaitForExpression: "window.__chartsReady === true",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderPainTrend: %w", err)
	}
	return out, nil
}

func buildPainTrendBody(in PainTrendInput) (body []byte, head string, err error) {
	funcs := commonFuncs()
	funcs["jsonArr"] = func(v any) (template.JS, error) {
		b, jErr := json.Marshal(v)
		if jErr != nil {
			return "", fmt.Errorf("jsonArr: %w", jErr)
		}
		return template.JS(b), nil
	}
	tmpl, parseErr := template.New("pain_trend.html.tmpl").Funcs(funcs).
		ParseFS(reportFS, "templates/pain_trend.html.tmpl")
	if parseErr != nil {
		return nil, "", fmt.Errorf("parse: %w", parseErr)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "pain_trend.html.tmpl", in); err != nil {
		return nil, "", fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), chartJSCDN(), nil
}
