package pdf

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// buildThemeCSS generates the per-render CSS string from the resolved
// DocTheme. The template lives in templates/_theme.css.tmpl and is
// embedded via baseFS in render.go. nil theme uses the renderer's
// built-in defaults (clinical teal, Inter, A4 portrait).
func buildThemeCSS(t *DocTheme) (string, error) {
	tmpl, err := template.New("_theme.css.tmpl").ParseFS(baseFS, "templates/_theme.css.tmpl")
	if err != nil {
		return "", fmt.Errorf("pdf.buildThemeCSS: parse: %w", err)
	}
	data := themeCSSData(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "_theme.css.tmpl", data); err != nil {
		return "", fmt.Errorf("pdf.buildThemeCSS: exec: %w", err)
	}
	return buf.String(), nil
}

// themeCSSVars is what the CSS template consumes — every value is
// already a string with a CSS-safe representation. The mapping from
// DocTheme is centralised here so the template stays declarative.
type themeCSSVars struct {
	PrimaryColor    string
	PrimarySoft     string
	SecondaryColor  string
	AccentColor     string
	TextColor       string
	TextEmphasis    string
	MutedTextColor  string
	BorderColor     string
	BorderSubtle    string
	SurfaceMuted    string
	HeadingFont     string
	BodyFont        string
	BaseSize        string // CSS unit string (e.g. "10pt")
	LineHeight      string // dimensionless
	CornerRadius    string // CSS unit (e.g. "6px")
	PageSize        string // a4 | letter | legal
	PageOrientation string // portrait | landscape
	MarginMm        string
	// HeaderPadBottom is the band's vertical breathing room. Driven by
	// theme.header.height (small | medium | tall) — the designer maps
	// this to a chunkier or tighter header band.
	HeaderPadBottom string // e.g. "8px" | "14px" | "22px"
	// HeaderFillCSS is one of:
	//   ""                                  — no background (default flat band, brand colour comes from the bottom border)
	//   "background: #abcdef;"              — solid fill from theme.header.fill.color
	//   "background: linear-gradient(…);"   — gradient fill from theme.header.fill.{from,to}
	// Spliced verbatim into the .doc-header rule.
	HeaderFillCSS string
	// HeaderTextOnFill colors the brand text + clinic strap when the
	// header has a coloured fill (so dark backgrounds get white type).
	// Empty string = use the default --salvia-text-emphasis.
	HeaderTextOnFill string
	WatermarkText   string
	WatermarkOpacity string // "0.04"
}

// Defaults — chosen to look identical to the sandbox mockups so the
// no-theme case still ships polished output.
const (
	defaultPrimary       = "#0e7c66"
	defaultPrimarySoft   = "#d8efe9"
	defaultSecondary     = "#1e6cd1"
	defaultAccent        = "#0e7c66"
	defaultText          = "#334155"
	defaultTextEmphasis  = "#0f172a"
	defaultMuted         = "#64748b"
	defaultBorder        = "#e2e8f0"
	defaultBorderSubtle  = "#edf2f7"
	defaultSurfaceMuted  = "#f8fafc"
	defaultHeadingFont   = `'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`
	defaultBodyFont      = `'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`
	defaultBaseSizePt    = 10.0
	defaultLineHeight    = 1.45
	defaultCornerRadius  = 6.0
	defaultPageSize      = "A4"
	defaultOrientation   = "portrait"
	defaultMarginMm      = 18.0
	defaultWatermarkA    = 0.04
	defaultHeaderPad     = "12px"
)

func themeCSSData(t *DocTheme) themeCSSVars {
	v := themeCSSVars{
		PrimaryColor:     defaultPrimary,
		PrimarySoft:      defaultPrimarySoft,
		SecondaryColor:   defaultSecondary,
		AccentColor:      defaultAccent,
		TextColor:        defaultText,
		TextEmphasis:     defaultTextEmphasis,
		MutedTextColor:   defaultMuted,
		BorderColor:      defaultBorder,
		BorderSubtle:     defaultBorderSubtle,
		SurfaceMuted:     defaultSurfaceMuted,
		HeadingFont:      defaultHeadingFont,
		BodyFont:         defaultBodyFont,
		BaseSize:         fmt.Sprintf("%gpt", defaultBaseSizePt),
		LineHeight:       fmt.Sprintf("%g", defaultLineHeight),
		CornerRadius:     fmt.Sprintf("%gpx", defaultCornerRadius),
		PageSize:         defaultPageSize,
		PageOrientation:  defaultOrientation,
		MarginMm:         fmt.Sprintf("%g", defaultMarginMm),
		HeaderPadBottom:  defaultHeaderPad,
		WatermarkText:    "",
		WatermarkOpacity: fmt.Sprintf("%g", defaultWatermarkA),
	}
	if t == nil {
		return v
	}
	if t.Theme != nil {
		v.PrimaryColor = derefStr(t.Theme.PrimaryColor, v.PrimaryColor)
		v.PrimarySoft = softenColor(v.PrimaryColor, defaultPrimarySoft)
		v.SecondaryColor = derefStr(t.Theme.SecondaryColor, v.SecondaryColor)
		v.AccentColor = derefStr(t.Theme.AccentColor, v.AccentColor)
		v.TextColor = derefStr(t.Theme.TextColor, v.TextColor)
		v.TextEmphasis = derefStr(t.Theme.TextColor, v.TextEmphasis)
		v.MutedTextColor = derefStr(t.Theme.MutedTextColor, v.MutedTextColor)
		if t.Theme.HeadingFont != nil {
			v.HeadingFont = quoteFont(*t.Theme.HeadingFont)
		}
		if t.Theme.BodyFont != nil {
			v.BodyFont = quoteFont(*t.Theme.BodyFont)
		}
		if t.Theme.BaseSize != nil {
			v.BaseSize = fmt.Sprintf("%gpt", *t.Theme.BaseSize)
		}
		if t.Theme.LineHeight != nil {
			v.LineHeight = fmt.Sprintf("%g", *t.Theme.LineHeight)
		}
		if t.Theme.CornerRadius != nil {
			v.CornerRadius = fmt.Sprintf("%gpx", *t.Theme.CornerRadius)
		}
	}
	if t.Header != nil {
		v.HeaderPadBottom = headerPadFromHeight(t.Header.Height)
		v.HeaderFillCSS, v.HeaderTextOnFill = headerFillCSS(t.Header.Fill)
	}
	if t.Page != nil {
		switch derefStr(t.Page.Size, "a4") {
		case "letter":
			v.PageSize = "Letter"
		case "legal":
			v.PageSize = "Legal"
		default:
			v.PageSize = "A4"
		}
		v.PageOrientation = derefStr(t.Page.Orientation, defaultOrientation)
		if t.Page.MarginMm != nil {
			v.MarginMm = fmt.Sprintf("%g", *t.Page.MarginMm)
		}
	}
	if t.Watermark != nil && strings.EqualFold(t.Watermark.Kind, "text") &&
		t.Watermark.Text != nil && *t.Watermark.Text != "" {
		v.WatermarkText = escapeForCSSContent(*t.Watermark.Text)
		if t.Watermark.Opacity != nil {
			v.WatermarkOpacity = fmt.Sprintf("%g", *t.Watermark.Opacity)
		}
	}
	return v
}

func derefStr(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}

// quoteFont wraps a single family in quotes if it isn't already, then
// appends our system fallback stack. Designer surfaces a single-name
// pick (e.g. "Lora"); the renderer expands it into a safe CSS family.
func quoteFont(family string) string {
	f := strings.Trim(strings.TrimSpace(family), "'\"")
	if f == "" {
		return defaultBodyFont
	}
	return fmt.Sprintf("'%s', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif", f)
}

// softenColor returns a lightened tint for KPI/info surfaces. Today
// it's a fixed table for a few common brand colors; future work can
// derive it from the primary via HSL math. Falls back to the supplied
// default when the primary isn't recognised.
func softenColor(primary, fallback string) string {
	switch strings.ToLower(primary) {
	case "#0e7c66":
		return "#d8efe9"
	case "#1e6cd1":
		return "#dbeafe"
	case "#5b21b6":
		return "#ede9fe"
	case "#b45309":
		return "#fef3c7"
	}
	return fallback
}

// escapeForCSSContent makes a string safe to splice into a CSS
// content: "..." declaration. Quotes and backslashes get escaped;
// nothing else needs special treatment for our usage (short text,
// no newlines).
func escapeForCSSContent(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}
