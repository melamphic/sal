// Package pdf renders HTML to PDF via a Gotenberg sidecar (headless
// Chromium under the hood). All generated PDFs in the system —
// signed clinical notes, compliance reports, audit packs — flow
// through this single client. Replaces the legacy fpdf renderer.
//
// Why Gotenberg + HTML instead of native Go PDF libs:
//   - HTML/CSS gives us charts, multi-column layouts, branded headers,
//     web fonts, signature panels — everything fpdf physically cannot.
//   - Designers and frontend folks already know HTML; iterating on a
//     report template is a .html.tmpl edit, not a fpdf.Cell() rewrite.
//   - Doc-theme rendering parity between in-app preview and the PDF
//     output collapses to one CSS path.
//
// Latency budget: 1-3s per render. Every consumer queues through
// report_jobs (async worker) so the user never waits inline.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/melamphic/sal/internal/platform/config"
)

// Renderer is the sole entry point for HTML→PDF conversion.
// Construct one with New and reuse it across the application.
type Renderer struct {
	httpClient *http.Client
	baseURL    string
}

// New builds a Renderer from application config. The underlying HTTP
// client uses the configured Gotenberg timeout; per-call deadlines
// in ctx are honoured on top.
func New(cfg *config.Config) *Renderer {
	return &Renderer{
		httpClient: &http.Client{Timeout: cfg.GotenbergTimeout},
		baseURL:    strings.TrimRight(cfg.GotenbergURL, "/"),
	}
}

// PaperSize is a named paper-size preset. Gotenberg accepts width/height
// in inches, so each preset embeds the inch values.
type PaperSize struct {
	WidthIn  float64
	HeightIn float64
}

// Common paper sizes. Add more as new report types need them.
var (
	A4     = PaperSize{WidthIn: 8.27, HeightIn: 11.69}
	Letter = PaperSize{WidthIn: 8.5, HeightIn: 11.0}
	Legal  = PaperSize{WidthIn: 8.5, HeightIn: 14.0}
)

// Options drive a single render. Zero values mean "Gotenberg default":
// portrait A4 with sensible margins. Override only what the report needs.
type Options struct {
	// Paper. Zero = A4.
	Paper PaperSize
	// Landscape orientation. Defaults to portrait.
	Landscape bool
	// Margins in inches. Zero values fall back to Gotenberg defaults
	// (~0.4in all round). Set explicitly when the doc-theme has a
	// margin preset.
	MarginTopIn    float64
	MarginBottomIn float64
	MarginLeftIn   float64
	MarginRightIn  float64
	// PrintBackground forces background colors / images to print —
	// otherwise Chromium strips them like a browser print dialog.
	// We need this on for branded headers/footers.
	PrintBackground bool
	// WaitForExpression is a JavaScript expression Gotenberg waits to
	// evaluate truthy before snapshotting. Use it when the page renders
	// Chart.js — set `window.__chartsReady === true` from the chart
	// onComplete callback and pass it here. nil = no wait.
	WaitForExpression string
	// Assets are auxiliary files the HTML references by relative path
	// (CSS, images, fonts, JS). Each is uploaded alongside index.html;
	// the HTML can reference them as `<link href="theme.css">`.
	Assets []Asset
}

// Asset is a single auxiliary file uploaded alongside the HTML.
// Filename must be relative (e.g. "theme.css", "logo.png") and is
// referenced from the HTML by that exact name.
type Asset struct {
	Filename string
	Content  []byte
}

// RenderHTML POSTs the given HTML (and any assets) to Gotenberg and
// returns the resulting PDF bytes. Honours both ctx cancellation and
// the renderer's configured timeout.
func (r *Renderer) RenderHTML(ctx context.Context, html []byte, opts Options) ([]byte, error) {
	if len(html) == 0 {
		return nil, errors.New("pdf.RenderHTML: empty HTML body")
	}
	body, contentType, err := buildMultipart(html, opts)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderHTML: build multipart: %w", err)
	}

	url := r.baseURL + "/forms/chromium/convert/html"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderHTML: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderHTML: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Gotenberg surfaces failures as a plain-text body — surface it
		// so a bad template / missing asset is obvious in logs.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf(
			"pdf.RenderHTML: gotenberg %d: %s",
			resp.StatusCode, strings.TrimSpace(string(msg)),
		)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pdf.RenderHTML: read response: %w", err)
	}
	return out, nil
}

// buildMultipart constructs the multipart/form-data body Gotenberg
// expects. The HTML must be uploaded as a file named "index.html" —
// other filenames are rejected by the chromium-html route. Field
// values for paper size / margins / wait-for are sent as plain
// form fields per Gotenberg API.
func buildMultipart(html []byte, opts Options) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// index.html is the entry point Gotenberg renders.
	if err := writeFilePart(w, "files", "index.html", html); err != nil {
		return nil, "", err
	}
	for _, a := range opts.Assets {
		if a.Filename == "index.html" {
			return nil, "", errors.New("pdf.buildMultipart: asset cannot shadow index.html")
		}
		if err := writeFilePart(w, "files", a.Filename, a.Content); err != nil {
			return nil, "", err
		}
	}

	// Gotenberg form-field options. All optional; omit when zero so
	// Gotenberg picks its own defaults.
	paper := opts.Paper
	if paper == (PaperSize{}) {
		paper = A4
	}
	if err := writeField(w, "paperWidth", strconv.FormatFloat(paper.WidthIn, 'f', 4, 64)); err != nil {
		return nil, "", err
	}
	if err := writeField(w, "paperHeight", strconv.FormatFloat(paper.HeightIn, 'f', 4, 64)); err != nil {
		return nil, "", err
	}
	if opts.Landscape {
		if err := writeField(w, "landscape", "true"); err != nil {
			return nil, "", err
		}
	}
	for _, m := range []struct {
		name string
		val  float64
	}{
		{"marginTop", opts.MarginTopIn},
		{"marginBottom", opts.MarginBottomIn},
		{"marginLeft", opts.MarginLeftIn},
		{"marginRight", opts.MarginRightIn},
	} {
		if m.val == 0 {
			continue
		}
		if err := writeField(w, m.name, strconv.FormatFloat(m.val, 'f', 4, 64)); err != nil {
			return nil, "", err
		}
	}
	if opts.PrintBackground {
		if err := writeField(w, "printBackground", "true"); err != nil {
			return nil, "", err
		}
	}
	if opts.WaitForExpression != "" {
		if err := writeField(w, "waitForExpression", opts.WaitForExpression); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("pdf.buildMultipart: close writer: %w", err)
	}
	return &buf, w.FormDataContentType(), nil
}

func writeFilePart(w *multipart.Writer, field, filename string, content []byte) error {
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		return fmt.Errorf("pdf.writeFilePart: create part %q: %w", filename, err)
	}
	if _, err := part.Write(content); err != nil {
		return fmt.Errorf("pdf.writeFilePart: write %q: %w", filename, err)
	}
	return nil
}

func writeField(w *multipart.Writer, name, value string) error {
	if err := w.WriteField(name, value); err != nil {
		return fmt.Errorf("pdf.writeField: %s: %w", name, err)
	}
	return nil
}
