package v2

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// AuditPackInput drives the audit-pack PDF — the bundled artifact
// for one signed clinical note (cover + signed note + evidence trace
// + edit history + policy check). The marketing site's "Audit Pack"
// promise. Header chrome stamps from Clinic via the shared partial —
// templates MUST NOT hardcode brand letters or colors.
type AuditPackInput struct {
	ClinicID string
	Clinic   pdf.ClinicInfo

	NoteID         string
	NoteIDShort    string
	GeneratedOn    string // "2026-05-04 16:42 NZST"
	BundleHashFull string // 64-char hex
	BundleHashShort string

	Patient        string // "Buddy"
	PatientMeta    string // "Canine · King Charles Spaniel"
	Owner          string
	EncounterDate  string
	FormName       string
	VetOfRecord    string

	// Bundle index — list of every artifact section.
	Artifacts []AuditPackArtifact

	// Signed clinical note body (HTML, pre-rendered safe). Use the
	// same template a Phase-1 note render produces — the audit pack
	// embeds it verbatim as Section 1.
	SignedNoteBody string

	// Evidence trace rows (Section 4).
	Evidence            []AuditPackEvidence
	EvidenceFieldsCount int
	EvidenceConfMean    float64
	AudioLength         string
	AudioTokens         int

	// Edit history (Section 5).
	EditHistory []AuditPackEditEvent

	// Policy satisfaction (Section 7).
	PolicyClauses []AuditPackPolicyClause
	PolicyAlignmentPct int
}

// AuditPackArtifact is one row of the bundle index.
type AuditPackArtifact struct {
	Idx       int
	Title     string
	What      string
	HashShort string
}

// AuditPackEvidence is one row of the field-to-source trace.
type AuditPackEvidence struct {
	Field      string
	Value      string
	Source     string // "<em>"…transcript…"</em> — 00:14"
	Confidence string // "0.97" or "—" for system fields
}

// AuditPackEditEvent is one bullet of the timeline.
type AuditPackEditEvent struct {
	WhenPretty string // "14:48:21"
	Tone       string // "default" | "warn" | "ok"
	Body       string // pre-rendered safe HTML
}

// AuditPackPolicyClause is one row of the policy satisfaction table.
type AuditPackPolicyClause struct {
	Policy     string
	Clause     string
	Requirement string
	Status     string // "Met" | "Implicit" | "Missed"
	StatusTone string // "ok" | "warn" | "danger"
}

func (r *Renderer) RenderAuditPack(ctx context.Context, in AuditPackInput) ([]byte, error) {
	theme, err := r.resolveTheme(ctx, in.ClinicID, "audit_pack")
	if err != nil {
		return nil, fmt.Errorf("v2.RenderAuditPack: %w", err)
	}
	clinic := pdf.ResolveClinicFromTheme(in.Clinic, theme)
	body, err := buildAuditPackBody(in, clinic)
	if err != nil {
		return nil, fmt.Errorf("v2.RenderAuditPack: %w", err)
	}
	out, err := r.pdf.RenderReport(ctx, pdf.ReportInput{
		DocType: "audit_pack",
		Title:   fmt.Sprintf("Audit Pack — Note %s", in.NoteIDShort),
		Lang:    "en",
		Body:    string(body),
		Theme:   theme,
		Clinic:  clinic,
	})
	if err != nil {
		return nil, fmt.Errorf("v2.RenderAuditPack: %w", err)
	}
	return out, nil
}

type auditPackView struct {
	AuditPackInput
	Clinic pdf.ClinicInfo
}

func buildAuditPackBody(in AuditPackInput, clinic pdf.ClinicInfo) ([]byte, error) {
	funcs := commonFuncs()
	funcs["safeHTML"] = func(s string) template.HTML { return template.HTML(s) }
	funcs["headerInfo"] = func(eyebrow, title, meta string) pdf.HeaderInfo {
		return pdf.HeaderInfo{Clinic: clinic, Eyebrow: eyebrow, Title: title, Meta: meta}
	}
	funcs["footerInfo"] = func(subject, pageLabel, footnote string) pdf.FooterInfo {
		return pdf.FooterInfo{
			Clinic:     clinic,
			Subject:    subject,
			BundleHash: in.BundleHashShort,
			PageLabel:  pageLabel,
			Footnote:   footnote,
		}
	}

	tmpl, err := pdf.NewReportTemplate("audit_pack.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("partials: %w", err)
	}
	tmpl, err = tmpl.Funcs(funcs).
		ParseFS(reportFS, "templates/audit_pack.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "audit_pack.html.tmpl",
		auditPackView{AuditPackInput: in, Clinic: clinic}); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}
