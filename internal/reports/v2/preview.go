package v2

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/melamphic/sal/internal/notes"
	"github.com/melamphic/sal/internal/platform/pdf"
)

// logoFetchClient bounds logo HTTP fetches at 10s — Gotenberg has its
// own ~60s budget but we want a fast-fail path so a wedged storage
// layer doesn't make the whole preview hang.
var logoFetchClient = &http.Client{Timeout: 10 * time.Second}

// PreviewDocType lists every doc-type the doc-theme designer can
// preview. Slugs match the templates and CSS classes elsewhere.
var PreviewDocType = []string{
	"signed_note",
	"audit_pack",
	"cd_register",
	"incident_report",
	"cd_reconciliation",
	"pain_trend",
	"mar_grid",
}

// IsPreviewDocType reports whether s is a known doc-type slug.
func IsPreviewDocType(s string) bool {
	for _, k := range PreviewDocType {
		if k == s {
			return true
		}
	}
	return false
}

// resolveThemeLogoURL turns an in-progress theme's header.logo_key
// (storage object key) into a base64 `data:` URI the renderer can
// embed inline. Empty string when no key is set or no signer is wired
// — the brand mark partial falls back to initials.
//
// We can't return the raw signed S3 URL: Gotenberg runs in Docker and
// can't resolve the host's `localhost:9000` (or any externally-facing
// MinIO endpoint) to a reachable address from inside the container.
// Inlining as a data URI removes the network hop entirely.
func resolveThemeLogoURL(ctx context.Context, theme *pdf.DocTheme, signer LogoSigner) (string, error) {
	if signer == nil || theme == nil || theme.Header == nil || theme.Header.LogoKey == nil {
		return "", nil
	}
	key := strings.TrimSpace(*theme.Header.LogoKey)
	if key == "" {
		return "", nil
	}
	url, err := signer.SignStyleLogoURL(ctx, key)
	if err != nil {
		return "", fmt.Errorf("v2.resolveThemeLogoURL: %w", err)
	}
	dataURI, err := fetchAsDataURI(ctx, url)
	if err != nil {
		return "", fmt.Errorf("v2.resolveThemeLogoURL: %w", err)
	}
	return dataURI, nil
}

// fetchAsDataURI GETs url with the supplied context and returns a
// `data:<content-type>;base64,<payload>` string. The backend is on
// the host so it CAN reach the presigned MinIO URL — the data URI
// is purely so the downstream Gotenberg render doesn't need to.
func fetchAsDataURI(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("v2.fetchAsDataURI: build request: %w", err)
	}
	resp, err := logoFetchClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("v2.fetchAsDataURI: get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("v2.fetchAsDataURI: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("v2.fetchAsDataURI: read: %w", err)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(body), nil
}

// stampLogoURL returns a copy of the supplied ClinicInfo with LogoURL
// set when non-empty. Used by the preview path to inject the user's
// uploaded logo into sample fixtures without mutating the package-
// level placeholder vars.
func stampLogoURL(in pdf.ClinicInfo, logoURL string) pdf.ClinicInfo {
	if logoURL != "" {
		in.LogoURL = template.URL(logoURL)
	}
	return in
}

// RenderPreview produces a sample-data render of the supplied doc-type
// against the supplied theme. Used by the doc-theme designer's live
// preview pane — the user sees what each report type looks like with
// their in-progress branding before they save.
//
// baseClinic is the real clinic info fetched from the DB by the handler.
// pdf.ResolveClinicFromTheme is applied on top of it so any theme-level
// content overrides (clinic_name, contact_line, tagline) still win.
// logoURL (the resolved data-URI for the in-progress logo upload) is
// stamped last. Empty baseClinic falls back to placeholder data.
//
// The notes-package signed note renderer is taken as a dependency so
// the preview path uses the exact same builder as production.
func (r *Renderer) RenderPreview(ctx context.Context, docType string, theme *pdf.DocTheme, logoURL string, baseClinic pdf.ClinicInfo, notesR *notes.HTMLRenderer) ([]byte, error) {
	if !IsPreviewDocType(docType) {
		return nil, fmt.Errorf("v2.RenderPreview: unknown doc_type %q", docType)
	}
	// Theme content overrides (clinic_name, contact_line) win over the
	// base; logo URL is stamped last so the in-progress upload shows.
	clinic := stampLogoURL(pdf.ResolveClinicFromTheme(baseClinic, theme), logoURL)
	switch docType {
	case "signed_note":
		return previewSignedNote(ctx, theme, clinic, notesR)
	case "audit_pack":
		in := SampleAuditPackInput()
		in.Clinic = clinic
		return r.renderWithThemeOverride(ctx, theme, func(ctx context.Context) ([]byte, error) {
			return r.RenderAuditPack(ctx, in)
		})
	case "cd_register":
		in := SampleCDRegisterInput()
		in.Clinic = clinic
		return r.renderWithThemeOverride(ctx, theme, func(ctx context.Context) ([]byte, error) {
			return r.RenderCDRegister(ctx, in)
		})
	case "incident_report":
		in := SampleIncidentReportInput()
		in.Clinic = clinic
		return r.renderWithThemeOverride(ctx, theme, func(ctx context.Context) ([]byte, error) {
			return r.RenderIncidentReport(ctx, in)
		})
	case "cd_reconciliation":
		in := SampleCDReconciliationInput()
		in.Clinic = clinic
		return r.renderWithThemeOverride(ctx, theme, func(ctx context.Context) ([]byte, error) {
			return r.RenderCDReconciliation(ctx, in)
		})
	case "pain_trend":
		in := SamplePainTrendInput()
		in.Clinic = clinic
		return r.renderWithThemeOverride(ctx, theme, func(ctx context.Context) ([]byte, error) {
			return r.RenderPainTrend(ctx, in)
		})
	case "mar_grid":
		in := SampleMARGridInput()
		in.Clinic = clinic
		return r.renderWithThemeOverride(ctx, theme, func(ctx context.Context) ([]byte, error) {
			return r.RenderMARGrid(ctx, in)
		})
	}
	// Unreachable — IsPreviewDocType gate above.
	return nil, fmt.Errorf("v2.RenderPreview: unhandled doc_type %q", docType)
}

// renderWithThemeOverride temporarily swaps in a static-theme provider
// so the call delegates to the existing RenderXxx methods (which fetch
// theme via r.theme.GetActiveDocTheme) but get back the supplied theme
// without touching the database. Restores the original provider on
// return.
//
// Single-renderer instance is shared, so callers MUST NOT use the
// preview endpoint concurrently with production rendering — the doc-
// theme designer is the only caller in practice and runs serial via
// a debounce on the FE. If concurrent preview becomes needed we'll
// wrap the provider in a request-scoped clone instead.
func (r *Renderer) renderWithThemeOverride(ctx context.Context, theme *pdf.DocTheme, doRender func(context.Context) ([]byte, error)) ([]byte, error) {
	prev := r.theme
	r.theme = staticTheme{theme: theme}
	defer func() { r.theme = prev }()
	out, err := doRender(ctx)
	if err != nil {
		return nil, fmt.Errorf("v2.renderWithThemeOverride: %w", err)
	}
	return out, nil
}

type staticTheme struct{ theme *pdf.DocTheme }

func (s staticTheme) GetActiveDocTheme(_ context.Context, _ string) (*pdf.DocTheme, error) {
	return s.theme, nil
}

// previewSignedNote builds the signed-note input from sample data and
// runs the supplied HTMLRenderer. clinic carries the already-resolved
// branding (real name + address + logo from ResolveClinicFromTheme +
// stampLogoURL). Theme is forwarded so PDFInput.Theme drives chrome.
func previewSignedNote(ctx context.Context, theme *pdf.DocTheme, clinic pdf.ClinicInfo, notesR *notes.HTMLRenderer) ([]byte, error) {
	if notesR == nil {
		return nil, fmt.Errorf("v2.previewSignedNote: notes renderer not wired")
	}
	formName, formVersion, noteID, submittedBy, _, _, clinicPhone,
		submittedAt, visit,
		subjectName, species, breed, microchip, weight :=
		SampleSignedNoteFields()

	addr := clinic.AddressLine1
	phone := clinicPhone
	visitT := visit
	in := notes.PDFInput{
		Theme:         theme,
		ClinicName:    clinic.Name,
		ClinicAddress: &addr,
		ClinicPhone:   &phone,
		FormName:      formName,
		FormVersion:   formVersion,
		NoteID:        noteID,
		SubmittedAt:   submittedAt,
		SubmittedBy:   submittedBy,
		Subject: &notes.PDFSubject{
			DisplayName: ptrStr(subjectName),
			Species:     ptrStr(species),
			Breed:       ptrStr(breed),
			Microchip:   ptrStr(microchip),
			WeightKg:    &weight,
		},
		VisitDate: &visitT,
		Fields: []notes.PDFField{
			{Label: "Presenting complaint", Value: "Right hindlimb lameness, intermittent, 4 days. No known trauma."},
			{Label: "Pain score (NRS)", Value: "3"},
			{
				Label: "Drug op — Meloxicam dispense", SystemKind: "drug_op", SystemReviewStatus: "approved",
				SystemSummary: []notes.PDFSummaryItem{
					{Label: "Drug", Value: "Meloxicam 1.5 mg/ml"},
					{Label: "Quantity", Value: "7 ml"},
				},
			},
		},
	}
	if logoURL := string(clinic.LogoURL); logoURL != "" {
		in.ClinicLogoURL = &logoURL
	}

	out, err := notesR.RenderNoteAsPDF(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("v2.previewSignedNote: %w", err)
	}
	return out, nil
}

func ptrStr(s string) *string { return &s }
