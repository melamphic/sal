package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/melamphic/sal/internal/notes"
	"github.com/melamphic/sal/internal/platform/pdf"
)

// Handler exposes the doc-theme preview endpoint. Lives in v2 so the
// preview path delegates to the same Renderer + sample fixtures the
// production reports use — no second source of truth for what a
// report looks like.
type Handler struct {
	renderer *Renderer
	notes    *notes.HTMLRenderer
}

// NewHandler builds a Handler. notesR is the notes-package HTML
// renderer (so the signed-note preview goes through the same builder
// as production); pass nil to disable signed_note previews (the
// endpoint will return 503 for that doc-type).
func NewHandler(r *Renderer, notesR *notes.HTMLRenderer) *Handler {
	return &Handler{renderer: r, notes: notesR}
}

// previewBodyInput is the request body for POST .../preview-pdf.
type previewBodyInput struct {
	Body struct {
		// DocType picks which sample report renders. Slugs:
		// signed_note | audit_pack | cd_register | incident_report |
		// cd_reconciliation | pain_trend | mar_grid.
		DocType string `json:"doc_type" enum:"signed_note,audit_pack,cd_register,incident_report,cd_reconciliation,pain_trend,mar_grid" doc:"Which sample report to render with the supplied theme."`

		// Config is the in-progress doc-theme JSON blob from the
		// designer. Same shape as clinic_form_style_versions.config —
		// see pdf.DocTheme.
		Config json.RawMessage `json:"config" doc:"DocTheme JSON blob from the designer (header/theme/body/watermark/footer/signature/page sections)."`
	}
}

// previewHTTPResponse returns raw PDF bytes with Content-Type set.
type previewHTTPResponse struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// preview handles POST /api/v1/clinic/form-style/preview-pdf.
//
// Auth: caller must have manage_forms (same as the rest of the
// form-style endpoints). The endpoint never persists anything — the
// supplied theme is used for one render and discarded.
func (h *Handler) preview(ctx context.Context, in *previewBodyInput) (*previewHTTPResponse, error) {
	docType := strings.TrimSpace(in.Body.DocType)
	if docType == "" {
		return nil, huma.Error400BadRequest("doc_type is required")
	}
	if !IsPreviewDocType(docType) {
		return nil, huma.Error400BadRequest(fmt.Sprintf("unknown doc_type %q", docType))
	}

	theme, err := pdf.DecodeDocTheme(in.Body.Config)
	if err != nil {
		return nil, huma.Error400BadRequest("config is not valid DocTheme JSON: " + err.Error())
	}

	if docType == "signed_note" && h.notes == nil {
		return nil, huma.Error503ServiceUnavailable("signed-note preview not wired (notes renderer missing)")
	}

	out, err := h.renderer.RenderPreview(ctx, docType, theme, h.notes)
	if err != nil {
		return nil, huma.Error500InternalServerError("preview render failed: " + err.Error())
	}

	return &previewHTTPResponse{
		ContentType:        "application/pdf",
		ContentDisposition: fmt.Sprintf(`inline; filename="preview-%s.pdf"`, docType),
		Body:               out,
	}, nil
}

// MountPreview registers the preview route on the supplied huma API.
// Caller threads the auth + manage_forms middlewares through.
func MountPreview(api huma.API, h *Handler, security []map[string][]string, middlewares huma.Middlewares) {
	huma.Register(api, huma.Operation{
		OperationID: "preview-form-style-pdf",
		Method:      http.MethodPost,
		Path:        "/api/v1/clinic/form-style/preview-pdf",
		Summary:     "Render the doc-theme designer preview as PDF",
		Description: "Renders one of the V1 reports against the supplied (in-progress, unsaved) doc-theme JSON and returns the raw PDF bytes. Used by the doc-theme designer's live preview pane to show the user what each report type looks like with their current branding before they save. Never persists anything.",
		Tags:        []string{"Forms"},
		Security:    security,
		Middlewares: middlewares,
	}, h.preview)
}
