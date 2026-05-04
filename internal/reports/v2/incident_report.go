package v2

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// IncidentReportInput drives the per-incident PDF (CQC Reg 18 / SIRS /
// SAC equivalent format). Caller assembles from incidents.Service.
type IncidentReportInput struct {
	ClinicID   string
	ClinicName string
	ClinicAddr string
	ClinicMeta string

	IncidentRef     string // "INC-2026-00184"
	GeneratedOn     string // "2026-04-29"
	SeverityLabel   string // "High" — capitalised
	SeverityTone    string // "danger" | "warn" | "info"
	SeverityHint    string // "Hospitalised — fractured neck of femur"
	TypeLabel       string // "Fall · Injurious"
	TypeHint        string // "Witnessed · unobserved at impact"
	CQCNotifiable   bool
	CQCSubmittedOn  string // "29 Apr 11:47" or empty
	CQCRefNumber    string // "CQC-NTF-3344821"

	ResidentName    string
	ResidentMeta    string // "(age 87)"
	Room            string
	CareCategory    string
	OccurredAt      string // "2026-04-28 03:42 BST"
	Location        string
	ReportedBy      string

	BriefDescription string

	ActionsTimeline []IncidentTimelineItem

	Outcome24h []IncidentOutcomeRow

	Notifications []IncidentNotificationRow

	// RCA + ActionPlan are nullable — render as free-text fallback
	// when caller passes nil, or full structured layout when set.
	RCA        *IncidentRCA
	ActionPlan []IncidentActionItem
	RootCause  string

	ReportedBySigName string // for sig block at end
	ReviewedBySigName string

	BundleHash string
}

// IncidentTimelineItem is one bullet in Section C (immediate actions).
type IncidentTimelineItem struct {
	WhenPretty string // "03:42"
	Tone       string // "default" | "warn" | "ok"
	Body       string
}

// IncidentOutcomeRow is one row in the 24h-post-event status table.
type IncidentOutcomeRow struct {
	Label string
	Value string
}

// IncidentNotificationRow is one entry in the notifications table.
type IncidentNotificationRow struct {
	Authority  string
	Status     string
	StatusTone string // "ok" | "warn" | "muted"
}

// IncidentRCA mirrors incident_events.rca JSONB (00082).
type IncidentRCA struct {
	Method  string
	Factors []IncidentRCAFactor
}

// IncidentRCAFactor is one row of the Five Whys / fishbone factor table.
type IncidentRCAFactor struct {
	Factor       string
	Finding      string
	Contributory string // "primary" | "contributory" | "no"
	Tone         string // pill tone
}

// IncidentActionItem mirrors one row of incident_events.action_plan JSONB.
type IncidentActionItem struct {
	Action     string
	Owner      string
	DuePretty  string
	Status     string // "Done" | "In progress" | "Scheduled"
	StatusTone string
}

func (r *Renderer) RenderIncidentReport(ctx context.Context, in IncidentReportInput) ([]byte, error) {
	body, err := buildIncidentReportBody(in)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderIncidentReport: %w", err)
	}
	theme, err := r.resolveTheme(ctx, in.ClinicID, "incident_report")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderIncidentReport: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "incident_report",
		Title:   fmt.Sprintf("Incident Report — %s", in.IncidentRef),
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderIncidentReport: %w", err)
	}
	return out, nil
}

func buildIncidentReportBody(in IncidentReportInput) ([]byte, error) {
	tmpl, err := template.New("incident_report.html.tmpl").
		Funcs(commonFuncs()).
		ParseFS(reportFS, "templates/incident_report.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "incident_report.html.tmpl", in); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}
