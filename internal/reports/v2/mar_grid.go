package v2

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"time"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// MARGridInput drives the per-resident, per-month MAR (Medication
// Administration Record) grid. Caller assembles from
// mar_prescriptions + mar_administration_events for the given
// (clinic, resident, month). Landscape A4 — see Options below.
// Header chrome stamps from Clinic via the shared `doc-header` partial;
// the template MUST NOT hardcode brand letters or colors.
type MARGridInput struct {
	ClinicID string
	Clinic   pdf.ClinicInfo

	ResidentName  string
	ResidentMeta  string // "(age 87)"
	NHSNumber     string
	GP            string
	Allergies     string
	CarePlanRev   string // "2026-03-15 · next 2026-06-15"
	Pharmacy      string
	Room          string

	PeriodLabel string // "April 2026"

	// Days[i] is "01 Apr"-style label; len(Days) is the column count.
	// DOWLabels[i] is the day-of-week single-letter strip ("M","T",…).
	// IsWeekend[i] flags weekend columns for shading.
	Days       []string
	DOWLabels  []string
	IsWeekend  []bool

	Prescriptions []MARPrescription

	// Free-text "notable events" callout under the grid (optional).
	Notes string

	// Sign-off
	StaffInitialsKey string // "SO = Sarah Okonkwo (HCA) · JP = Jodie Patel (RN)"
	ReviewedBy       string

	BundleHash string
}

// MARPrescription is one row of the grid: a drug + per-day cells
// indexed by day position (0..len(Days)-1).
type MARPrescription struct {
	DrugLine        string // "<strong>Furosemide 40 mg</strong><em>1 tab PO mane · CHF</em>" — pre-rendered safe HTML
	Time            string // "08:00" or "PRN"
	IsCD            bool   // shows "SCHEDULE 5 CD · WITNESS" pill
	IsFallsRisk     bool   // shows "⚠ FALLS RISK" pill
	StartsLate      bool   // bool — if true, render the "not yet prescribed" pre-shade up to StartsAtCol
	StartsAtCol     int    // column index when starts late
	Cells           []MARCell
}

// MARCell is one day slot. OutcomeCode encodes the visual state.
type MARCell struct {
	Initials    string // "SO" / "JP" / "JP+SO" / ""
	OutcomeCode string // "given" | "refused" | "omit-clinical" | "missed" | "held" | "" (not scheduled)
	Weekend     bool
}

// RenderMARGrid returns landscape PDF bytes for the MAR grid.
func (r *Renderer) RenderMARGrid(ctx context.Context, in MARGridInput) ([]byte, error) {
	theme, err := r.resolveTheme(ctx, in.ClinicID, "mar_grid")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderMARGrid: %w", err)
	}
	clinic := pdf.ResolveClinicFromTheme(in.Clinic, theme)
	body, err := buildMARGridBody(in, clinic)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderMARGrid: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "mar_grid",
		Title:   fmt.Sprintf("MAR · %s · %s", in.ResidentName, in.PeriodLabel),
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
		Clinic:  clinic,
		Options: pdf.Options{
			// Landscape A4 with tight margins for grid density.
			Landscape:      true,
			MarginTopIn:    0.5,
			MarginBottomIn: 0.5,
			MarginLeftIn:   0.4,
			MarginRightIn:  0.4,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderMARGrid: %w", err)
	}
	return out, nil
}

type marGridView struct {
	MARGridInput
	Clinic pdf.ClinicInfo
}

func buildMARGridBody(in MARGridInput, clinic pdf.ClinicInfo) ([]byte, error) {
	funcs := commonFuncs()
	funcs["safeHTML"] = func(s string) template.HTML { return template.HTML(s) }
	funcs["headerInfo"] = func(eyebrow, title, meta string) pdf.HeaderInfo {
		return pdf.HeaderInfo{Clinic: clinic, Eyebrow: eyebrow, Title: title, Meta: meta}
	}
	funcs["footerInfo"] = func(subject, pageLabel, footnote string) pdf.FooterInfo {
		return pdf.FooterInfo{
			Clinic:     clinic,
			Subject:    subject,
			BundleHash: in.BundleHash,
			PageLabel:  pageLabel,
			Footnote:   footnote,
		}
	}

	tmpl, err := pdf.NewReportTemplate("mar_grid.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("partials: %w", err)
	}
	tmpl, err = tmpl.Funcs(funcs).
		ParseFS(reportFS, "templates/mar_grid.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "mar_grid.html.tmpl",
		marGridView{MARGridInput: in, Clinic: clinic}); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}

// MonthDays generates the Days/DOWLabels/IsWeekend slices for a given
// month — convenience for callers who don't already have them.
func MonthDays(year int, month time.Month, loc *time.Location) (days, dow []string, weekend []bool) {
	if loc == nil {
		loc = time.UTC
	}
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	next := first.AddDate(0, 1, 0)
	for d := first; d.Before(next); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("02"))
		dowLetter := d.Format("Mon")[:1]
		dow = append(dow, dowLetter)
		weekend = append(weekend, d.Weekday() == time.Saturday || d.Weekday() == time.Sunday)
	}
	return
}
