// Package v2 holds the HTML-rendered compliance reports that replace
// the fpdf-driven internal/reports/pdf.go. Each report is built from
// data the existing domain repositories already expose — this package
// owns templating + the Gotenberg call but pulls SQL through narrow
// adapter interfaces declared per report.
//
// Adding a report:
//
//  1. Pick a doc-type slug (`audit_pack`, `cd_register`, …).
//  2. Define the data adapters + view-model in a {report}.go file.
//  3. Embed the body template as templates/{report}.html.tmpl.
//  4. Wire a builder method on Renderer.
//
// The shared base layout, theme CSS, header/footer chrome live in
// internal/platform/pdf — this package only emits the inner body.
package v2

import (
	"context"
	"embed"
	"fmt"

	"github.com/melamphic/sal/internal/platform/pdf"
)

//go:embed templates/*.html.tmpl
var reportFS embed.FS

// Renderer is the entry point shared by every report builder. It
// holds the platform pdf renderer + the doc-theme provider so each
// report can resolve a clinic's theme without re-implementing the
// fetch.
type Renderer struct {
	pdf   *pdf.Renderer
	theme ThemeProvider
}

// ThemeProvider returns the active doc-theme for a clinic. nil
// theme is OK — the platform pdf package falls back to renderer
// defaults. Mirrors notes.DocThemeProvider but lives in this
// package so v2 doesn't take a notes import.
type ThemeProvider interface {
	GetActiveDocTheme(ctx context.Context, clinicID string) (*pdf.DocTheme, error)
}

// New constructs a Renderer. Passing a nil theme provider is
// supported — every report renders against the renderer defaults.
func New(p *pdf.Renderer, t ThemeProvider) *Renderer {
	return &Renderer{pdf: p, theme: t}
}

// resolveTheme fetches the clinic's theme via the provider, returning
// the renderer-default (nil) when no provider is wired.
func (r *Renderer) resolveTheme(ctx context.Context, clinicID string) (*pdf.DocTheme, error) {
	if r.theme == nil {
		return nil, nil
	}
	dt, err := r.theme.GetActiveDocTheme(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("v2.resolveTheme: %w", err)
	}
	return dt, nil
}
