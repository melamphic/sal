package notes

import (
	"fmt"

	"github.com/melamphic/sal/internal/platform/pdf"
)

// The DocTheme types live in the platform pdf package now — both the
// legacy fpdf renderer in this package and the new HTML+Gotenberg
// renderer share the same shape. These aliases keep notes consumers
// (pdf.go, app.go) building unchanged while we migrate.
//
// Once the fpdf renderer in this package is deleted (P3-Q), all of
// these aliases — and any code that still references them — go too.

type (
	DocTheme              = pdf.DocTheme
	DocThemeHeader        = pdf.DocThemeHeader
	DocThemeContent       = pdf.DocThemeContent
	DocThemeSlots         = pdf.DocThemeSlots
	DocThemeFill          = pdf.DocThemeFill
	DocThemeTheme         = pdf.DocThemeTheme
	DocThemeBody          = pdf.DocThemeBody
	DocThemeWatermark     = pdf.DocThemeWatermark
	DocThemeFooter        = pdf.DocThemeFooter
	DocThemeFooterSlots   = pdf.DocThemeFooterSlots
	DocThemeFooterContent = pdf.DocThemeFooterContent
	DocThemeSignature     = pdf.DocThemeSignature
	DocThemePage          = pdf.DocThemePage
)

// DecodeDocTheme — see pdf.DecodeDocTheme. Keeps the historic call
// site `notes.DecodeDocTheme(...)` building.
func DecodeDocTheme(raw []byte) (*DocTheme, error) {
	dt, err := pdf.DecodeDocTheme(raw)
	if err != nil {
		return nil, fmt.Errorf("notes.DecodeDocTheme: %w", err)
	}
	return dt, nil
}
