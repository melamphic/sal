package pdf

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/_base.html.tmpl templates/_theme.css.tmpl
var baseFS embed.FS

// ReportInput is what every report builder passes to RenderReport. The
// builder fills in DocType (slug used for per-doc-type override + CSS
// class) and Body (the actual content HTML — header, sections, tables,
// signatures). The theme + clinic context are picked up from Theme.
type ReportInput struct {
	// DocType identifies the report — drives per-doc-type override
	// resolution and the CSS class on <body>. Slugs:
	//   "signed_note" | "audit_pack" | "cd_register" |
	//   "incident_report" | "cd_reconciliation" |
	//   "pain_trend" | "mar_grid" | …
	DocType string

	// Title goes into the HTML <title> tag. Browsers + Gotenberg may
	// surface it as the PDF document title metadata.
	Title string

	// Lang is the BCP-47 language tag (e.g. "en", "en-NZ", "mi"). The
	// renderer only uses it on <html lang>, but document-level a11y
	// tooling cares about it.
	Lang string

	// Body is the report content as HTML. The base template wraps it
	// with <html><head>…</head><body class="doc doc--{{ DocType }}">.
	// Include any per-page <section class="page"> structure here.
	Body string

	// ExtraHead is optional raw HTML appended to <head>. Use for inline
	// <script> blocks (e.g. Chart.js + chart code), additional <style>,
	// or <link rel="stylesheet"> when an asset needs to be CSS.
	ExtraHead string

	// Theme is the resolved DocTheme (base + per-doc-override merged).
	// Pass nil to render with the renderer defaults.
	Theme *DocTheme

	// Render options forwarded to Gotenberg. The renderer fills in
	// PrintBackground=true automatically (every doc theme has banded
	// chrome that needs background printing) and resolves Paper /
	// Landscape / margins from Theme when the option fields are zero.
	Options Options
}

// RenderReport renders an HTML report to PDF bytes via Gotenberg.
// The base template + theme CSS are merged with the caller-supplied
// Body to produce a full HTML document, then sent to Gotenberg.
//
// Flow:
//
//  1. Resolve theme defaults → CSS via buildThemeCSS.
//  2. Render templates/_base.html.tmpl with the supplied Body, Title,
//     ExtraHead, ThemeCSS.
//  3. Apply theme-driven Options (paper / orientation / margins) when
//     the caller didn't override.
//  4. POST to Gotenberg via Renderer.RenderHTML.
func (r *Renderer) RenderReport(ctx context.Context, in ReportInput) ([]byte, error) {
	if strings.TrimSpace(in.Body) == "" {
		return nil, fmt.Errorf("pdf.RenderReport: empty body for doc_type %q", in.DocType)
	}

	css, err := buildThemeCSS(in.Theme)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderReport: theme css: %w", err)
	}

	html, err := buildBaseHTML(in, css)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderReport: base html: %w", err)
	}

	opts := in.Options
	opts.PrintBackground = true // banded chrome needs this on
	applyThemePageOptions(&opts, in.Theme)

	pdf, err := r.RenderHTML(ctx, html, opts)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderReport: %w", err)
	}
	return pdf, nil
}

// buildBaseHTML wraps the report body with <html><head><body>.
// Uses html/template for HTML-context auto-escape on string fields;
// the Body itself is template.HTML so it's spliced verbatim (the
// builder is responsible for safe content there).
func buildBaseHTML(in ReportInput, css string) ([]byte, error) {
	tmpl, err := template.New("_base").ParseFS(baseFS, "templates/_base.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	data := struct {
		Lang      string
		Title     string
		ThemeCSS  template.CSS
		ExtraHead template.HTML
		DocType   string
		Body      template.HTML
	}{
		Lang:      langOrDefault(in.Lang),
		Title:     in.Title,
		ThemeCSS:  template.CSS(css),
		ExtraHead: template.HTML(in.ExtraHead),
		DocType:   docTypeOrDefault(in.DocType),
		Body:      template.HTML(in.Body),
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "_base.html.tmpl", data); err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return buf.Bytes(), nil
}

func langOrDefault(s string) string {
	if s == "" {
		return "en"
	}
	return s
}

func docTypeOrDefault(s string) string {
	if s == "" {
		return "report"
	}
	return s
}

// applyThemePageOptions fills in paper / orientation / margins on the
// Options struct from the theme's Page block. Only fills fields the
// caller left as zero values, so a builder that knows it wants
// landscape regardless of theme can override.
func applyThemePageOptions(opts *Options, theme *DocTheme) {
	if theme == nil || theme.Page == nil {
		if opts.Paper == (PaperSize{}) {
			opts.Paper = A4
		}
		return
	}
	page := theme.Page
	if opts.Paper == (PaperSize{}) {
		switch deref(page.Size, "a4") {
		case "letter":
			opts.Paper = Letter
		case "legal":
			opts.Paper = Legal
		default:
			opts.Paper = A4
		}
	}
	if !opts.Landscape && deref(page.Orientation, "portrait") == "landscape" {
		opts.Landscape = true
	}
	if page.MarginMm != nil && opts.MarginTopIn == 0 && opts.MarginBottomIn == 0 &&
		opts.MarginLeftIn == 0 && opts.MarginRightIn == 0 {
		// 1in = 25.4mm
		mIn := *page.MarginMm / 25.4
		opts.MarginTopIn = mIn
		opts.MarginBottomIn = mIn
		opts.MarginLeftIn = mIn
		opts.MarginRightIn = mIn
	}
}

func deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
