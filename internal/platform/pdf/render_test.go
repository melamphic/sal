package pdf

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestRenderReport_StubGotenberg drives the full RenderReport pipeline
// against a stub Gotenberg that captures the multipart body it sees.
// Verifies: theme CSS gets inlined, body gets spliced, doc-type lands
// on body class, and theme-driven page options propagate.
func TestRenderReport_StubGotenberg(t *testing.T) {
	t.Parallel()

	type capture struct {
		html   string
		fields map[string]string
	}
	var got capture

	srv := fakeGotenberg(t, http.StatusOK, []byte("%PDF-fake%"),
		func(t *testing.T, parts map[string][]byte, fields map[string]string) {
			t.Helper()
			got.html = string(parts["index.html"])
			got.fields = fields
		})
	defer srv.Close()
	r := newTestRenderer(t, srv.URL)

	primary := "#0e7c66"
	bodyFont := "Lora"
	pageSize := "letter"
	orientation := "landscape"
	margin := 25.4 // 1 inch

	theme := &DocTheme{
		Theme: &DocThemeTheme{
			PrimaryColor: &primary,
			BodyFont:     &bodyFont,
		},
		Page: &DocThemePage{
			Size:        &pageSize,
			Orientation: &orientation,
			MarginMm:    &margin,
		},
	}
	out, err := r.RenderReport(context.Background(), ReportInput{
		DocType: "audit_pack",
		Title:   "Audit Pack — Buddy",
		Lang:    "en-NZ",
		Body:    `<section class="page"><h1>Hello</h1></section>`,
		Theme:   theme,
	})
	if err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	if string(out) != "%PDF-fake%" {
		t.Errorf("output bytes did not pass through: %q", out)
	}

	// HTML body — theme CSS inlined + body spliced + class on body.
	for _, want := range []string{
		`<html lang="en-NZ">`,
		`<title>Audit Pack — Buddy</title>`,
		`class="doc doc--audit_pack"`,
		`<h1>Hello</h1>`,
		`--salvia-primary:        #0e7c66;`,
		`'Lora'`,
		`size: Letter landscape;`,
	} {
		if !strings.Contains(got.html, want) {
			t.Errorf("rendered HTML missing %q\n--- got ---\n%s", want, got.html)
		}
	}

	// Page options — theme drove paper Letter + landscape + 1in margins.
	if got.fields["paperWidth"] == "" || got.fields["paperHeight"] == "" {
		t.Errorf("paperWidth/paperHeight not set")
	}
	if got.fields["landscape"] != "true" {
		t.Errorf("landscape = %q, want true", got.fields["landscape"])
	}
	if got.fields["printBackground"] != "true" {
		t.Errorf("printBackground = %q, want true", got.fields["printBackground"])
	}
	if got.fields["marginTop"] == "" {
		t.Errorf("marginTop not propagated from theme")
	}
}

// TestRenderReport_NoTheme covers the nil-theme path — render still
// succeeds with renderer defaults and produces a sensible HTML body.
func TestRenderReport_NoTheme(t *testing.T) {
	t.Parallel()

	var captured string
	srv := fakeGotenberg(t, http.StatusOK, []byte("%PDF%"),
		func(t *testing.T, parts map[string][]byte, fields map[string]string) {
			t.Helper()
			captured = string(parts["index.html"])
		})
	defer srv.Close()
	r := newTestRenderer(t, srv.URL)

	_, err := r.RenderReport(context.Background(), ReportInput{
		DocType: "signed_note",
		Title:   "x",
		Body:    "<p>x</p>",
	})
	if err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	for _, want := range []string{
		`--salvia-primary:        #0e7c66;`,
		`size: A4 portrait;`,
		`class="doc doc--signed_note"`,
	} {
		if !strings.Contains(captured, want) {
			t.Errorf("nil-theme HTML missing %q", want)
		}
	}
}

// TestRenderReport_EmptyBody surfaces the developer error early
// instead of going through Gotenberg with an unhelpful response.
func TestRenderReport_EmptyBody(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(t, "http://unused")
	_, err := r.RenderReport(context.Background(), ReportInput{
		DocType: "audit_pack",
		Body:    "   \n  ",
	})
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if !strings.Contains(err.Error(), "empty body") {
		t.Errorf("expected empty body error, got %q", err.Error())
	}
}

func TestMergeOverride(t *testing.T) {
	t.Parallel()

	primary := "#0e7c66"
	primaryOverride := "#b91c1c"
	base := &DocTheme{
		Theme:  &DocThemeTheme{PrimaryColor: &primary},
		Header: &DocThemeHeader{Shape: "single_curve"},
	}
	override := &DocTheme{
		Theme: &DocThemeTheme{PrimaryColor: &primaryOverride},
	}

	merged := MergeOverride(base, override)
	if merged == nil || merged.Theme == nil || merged.Header == nil {
		t.Fatalf("merged structure incomplete: %+v", merged)
	}
	if merged.Theme.PrimaryColor == nil || *merged.Theme.PrimaryColor != primaryOverride {
		t.Errorf("override theme didn't win: %+v", merged.Theme)
	}
	if merged.Header.Shape != "single_curve" {
		t.Errorf("base header should have been preserved: %+v", merged.Header)
	}

	got := MergeOverride(nil, override)
	if got == nil || got.Theme == nil || got.Theme.PrimaryColor == nil ||
		*got.Theme.PrimaryColor != primaryOverride {
		t.Errorf("nil base should return override unchanged")
	}
	got = MergeOverride(base, nil)
	if got == nil || got.Theme == nil || got.Theme.PrimaryColor == nil ||
		*got.Theme.PrimaryColor != primary {
		t.Errorf("nil override should return base unchanged")
	}
	if MergeOverride(nil, nil) != nil {
		t.Errorf("nil + nil should return nil")
	}
}

// Ensure embedded templates load without a fs error at package init.
func TestEmbeddedTemplatesPresent(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		"templates/_base.html.tmpl",
		"templates/_theme.css.tmpl",
	} {
		f, err := baseFS.Open(p)
		if err != nil {
			t.Errorf("template %q not embedded: %v", p, err)
			continue
		}
		b, _ := io.ReadAll(f)
		_ = f.Close()
		if len(b) == 0 {
			t.Errorf("template %q is empty", p)
		}
	}
}
