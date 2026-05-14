package notes

import (
	"embed"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Embedded font data so PDFs render with the exact Google Font family
// the clinic picked in the doc-theme designer. fpdf's built-in fonts
// (Helvetica / Times / Courier) ship as PostScript Type 1 with no
// glyph data, so they can only render the 14 base PDF fonts. Bundling
// these TTFs lets a clinic pick Inter / Newsreader / etc. and have
// the printed PDF actually look like the in-app preview.
//
// Provenance: every TTF below was fetched from Google Fonts'
// `fonts.googleapis.com/css2` endpoint, which serves the canonical
// SIL OFL-licensed binaries from fonts.gstatic.com. Files live next to
// this source file at internal/notes/fonts/.

//go:embed fonts/*.ttf
var fontAssets embed.FS

// bundledFontFamilies is the registry of (family, style) pairs we
// register at startup. The mobile picker exposes these exact family
// names in `_fontRailOptions`, and `mapFont` returns the matching
// family name when a clinic selects one.
//
// Format: family display name → file basename (without -Regular suffix).
// We always register all four styles (Regular, Bold, Italic,
// BoldItalic) so the renderer can use any combination without falling
// back to a built-in font mid-document.
var bundledFontFamilies = map[string]string{
	"Inter":             "Inter",
	"Plus Jakarta Sans": "PlusJakartaSans",
	"Newsreader":        "Newsreader",
	"JetBrains Mono":    "JetBrainsMono",
	"Lora":              "Lora",
}

// styleSuffixes enumerates the four fpdf style codes paired with the
// suffix on the bundled .ttf filename.
var styleSuffixes = []struct {
	style  string // "" | "B" | "I" | "BI"
	suffix string // "-Regular" | "-Bold" | "-Italic" | "-BoldItalic"
}{
	{"", "-Regular"},
	{"B", "-Bold"},
	{"I", "-Italic"},
	{"BI", "-BoldItalic"},
}

// registerBundledFonts wires every bundled TTF into the given fpdf
// instance. Called once per document at the top of GeneratePDF. Cheap:
// fpdf reads the font tables once and caches them on the underlying
// document. A failure on any single font is logged-and-skipped so a
// corrupted file doesn't blank the whole PDF.
func registerBundledFonts(pdf *fpdf.Fpdf) {
	for family, base := range bundledFontFamilies {
		for _, s := range styleSuffixes {
			path := fmt.Sprintf("fonts/%s%s.ttf", base, s.suffix)
			data, err := fontAssets.ReadFile(path)
			if err != nil {
				continue
			}
			pdf.AddUTF8FontFromBytes(family, s.style, data)
		}
	}
}

// hasBundledFont reports whether the given display name was registered
// by [registerBundledFonts]. Case-insensitive match against the keys
// of [bundledFontFamilies] so frontend strings ("inter", "Inter",
// "INTER") all map.
func hasBundledFont(name string) (string, bool) {
	want := strings.TrimSpace(name)
	if want == "" {
		return "", false
	}
	for family := range bundledFontFamilies {
		if strings.EqualFold(family, want) {
			return family, true
		}
	}
	return "", false
}
