package pdf

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/melamphic/sal/internal/platform/config"
)

// fakeGotenberg returns an httptest server that mimics enough of the
// Gotenberg /forms/chromium/convert/html surface for client tests:
// it inspects the multipart body and either returns the supplied PDF
// bytes (or status code + message) so the client's parsing paths get
// covered without docker.
func fakeGotenberg(t *testing.T, status int, pdf []byte, want func(t *testing.T, parts map[string][]byte, fields map[string]string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/forms/chromium/convert/html" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		parts := map[string][]byte{}
		fields := map[string]string{}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			body, _ := io.ReadAll(p)
			if p.FileName() != "" {
				parts[p.FileName()] = body
			} else {
				fields[p.FormName()] = string(body)
			}
		}
		if want != nil {
			want(t, parts, fields)
		}
		if status != http.StatusOK {
			http.Error(w, string(pdf), status)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pdf)
	}))
}

func newTestRenderer(t *testing.T, baseURL string) *Renderer {
	t.Helper()
	cfg := &config.Config{
		GotenbergURL:     baseURL,
		GotenbergTimeout: 5 * time.Second,
	}
	return New(cfg)
}

func TestRenderer_RenderHTML_OK(t *testing.T) {
	t.Parallel()

	wantHTML := []byte("<!doctype html><html><body><h1>Hi</h1></body></html>")
	wantAsset := []byte("body{color:red}")
	pdfBytes := []byte("%PDF-1.4\n%fake\n%%EOF")

	srv := fakeGotenberg(t, http.StatusOK, pdfBytes, func(t *testing.T, parts map[string][]byte, fields map[string]string) {
		t.Helper()
		if got := parts["index.html"]; string(got) != string(wantHTML) {
			t.Errorf("index.html = %q, want %q", got, wantHTML)
		}
		if got := parts["theme.css"]; string(got) != string(wantAsset) {
			t.Errorf("theme.css = %q, want %q", got, wantAsset)
		}
		if fields["paperWidth"] == "" {
			t.Errorf("paperWidth field missing — should default to A4")
		}
		if fields["printBackground"] != "true" {
			t.Errorf("printBackground = %q, want true", fields["printBackground"])
		}
		if fields["landscape"] != "true" {
			t.Errorf("landscape = %q, want true", fields["landscape"])
		}
		if fields["waitForExpression"] != "window.__chartsReady === true" {
			t.Errorf("waitForExpression mismatch: %q", fields["waitForExpression"])
		}
	})
	defer srv.Close()

	r := newTestRenderer(t, srv.URL)
	out, err := r.RenderHTML(context.Background(), wantHTML, Options{
		Landscape:         true,
		PrintBackground:   true,
		WaitForExpression: "window.__chartsReady === true",
		Assets:            []Asset{{Filename: "theme.css", Content: wantAsset}},
	})
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if string(out) != string(pdfBytes) {
		t.Errorf("output PDF body = %q, want %q", out, pdfBytes)
	}
}

func TestRenderer_RenderHTML_GotenbergError(t *testing.T) {
	t.Parallel()
	srv := fakeGotenberg(t, http.StatusBadRequest, []byte("template error: missing partial"), nil)
	defer srv.Close()

	r := newTestRenderer(t, srv.URL)
	_, err := r.RenderHTML(context.Background(), []byte("<html></html>"), Options{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "gotenberg 400") {
		t.Errorf("error should mention gotenberg status, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "template error") {
		t.Errorf("error should surface gotenberg body, got %q", err.Error())
	}
}

func TestRenderer_RenderHTML_EmptyHTML(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(t, "http://unused")
	_, err := r.RenderHTML(context.Background(), nil, Options{})
	if err == nil {
		t.Fatal("expected error for empty HTML, got nil")
	}
	if !strings.Contains(err.Error(), "empty HTML body") {
		t.Errorf("expected empty-body error, got %q", err.Error())
	}
}

func TestRenderer_RenderHTML_AssetCannotShadowIndex(t *testing.T) {
	t.Parallel()
	r := newTestRenderer(t, "http://unused")
	_, err := r.RenderHTML(context.Background(), []byte("<html></html>"), Options{
		Assets: []Asset{{Filename: "index.html", Content: []byte("oops")}},
	})
	if err == nil {
		t.Fatal("expected error when asset shadows index.html")
	}
	if !strings.Contains(err.Error(), "shadow index.html") {
		t.Errorf("expected shadow error, got %q", err.Error())
	}
}

// Sanity: confirm that buildMultipart produces a body the standard
// multipart reader can parse (so consumers can rely on it).
func TestBuildMultipart_RoundTrip(t *testing.T) {
	t.Parallel()
	html := []byte("<html><body>x</body></html>")
	body, ct, err := buildMultipart(html, Options{
		Paper:           Letter,
		MarginTopIn:     0.5,
		PrintBackground: true,
	})
	if err != nil {
		t.Fatalf("buildMultipart: %v", err)
	}
	// Parse the boundary out of content-type to drive a multipart.Reader.
	const prefix = "multipart/form-data; boundary="
	if !strings.HasPrefix(ct, prefix) {
		t.Fatalf("unexpected content-type %q", ct)
	}
	boundary := strings.TrimPrefix(ct, prefix)
	mr := multipart.NewReader(body, boundary)
	seen := map[string]bool{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		seen[p.FormName()] = true
	}
	for _, want := range []string{"files", "paperWidth", "paperHeight", "marginTop", "printBackground"} {
		if !seen[want] {
			t.Errorf("part %q missing from multipart body", want)
		}
	}
}
