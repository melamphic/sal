package pdf

import (
	"encoding/json"
	"fmt"
)

// DocTheme is the typed mirror of the rich `clinic_form_style_versions.config`
// JSONB blob produced by the Flutter doc-theme designer. Stays in lock-step
// with `apps/lib/features/doc_theme/domain/doc_theme.dart` so the same JSON
// drives the in-app preview and the server-rendered PDF.
//
// Sections are pointers so a partial config (older row, simple-mode edit,
// preset bootstrap) decodes cleanly — missing sections fall back to the
// renderer defaults instead of zero-valued enums.
//
// Canonical home of the type lives in the pdf package because every PDF
// renderer (notes + reports + audit pack + …) consumes it. The notes
// package re-exports the type alias for back-compat with the legacy fpdf
// renderer until that's deleted.
type DocTheme struct {
	Header    *DocThemeHeader    `json:"header,omitempty"`
	Theme     *DocThemeTheme     `json:"theme,omitempty"`
	Body      *DocThemeBody      `json:"body,omitempty"`
	Watermark *DocThemeWatermark `json:"watermark,omitempty"`
	Footer    *DocThemeFooter    `json:"footer,omitempty"`
	Signature *DocThemeSignature `json:"signature,omitempty"`
	Page      *DocThemePage      `json:"page,omitempty"`
	PresetID  *string            `json:"preset_id,omitempty"`
}

// DocThemeHeader maps the `header` block.
type DocThemeHeader struct {
	Shape     string           `json:"shape,omitempty"` // flat | single_curve | double_wave | diagonal | none
	Fill      *DocThemeFill    `json:"fill,omitempty"`
	Height    string           `json:"height,omitempty"` // small | medium | tall
	ExtraText *string          `json:"extra_text,omitempty"`
	LogoKey   *string          `json:"logo_key,omitempty"`
	Content   *DocThemeContent `json:"content,omitempty"`
	Slots     *DocThemeSlots   `json:"slots,omitempty"`
}

// DocThemeContent — free-form text overrides set by the form author.
type DocThemeContent struct {
	ClinicName  *string `json:"clinic_name,omitempty"`
	ContactLine *string `json:"contact_line,omitempty"`
	Tagline     *string `json:"tagline,omitempty"`
}

// DocThemeSlots toggles which header rows the renderer emits.
type DocThemeSlots struct {
	ClinicName  *bool   `json:"clinic_name,omitempty"`
	ContactLine *bool   `json:"contact_line,omitempty"`
	Tagline     *bool   `json:"tagline,omitempty"`
	Badges      *bool   `json:"badges,omitempty"`
	Logo        *string `json:"logo,omitempty"` // left | center | right
}

// DocThemeFill is a band fill — solid color, gradient, or image. With the
// HTML/CSS renderer all three render natively (CSS gradients via
// linear-gradient, images via background-image), unlike the fpdf path.
type DocThemeFill struct {
	Kind     string  `json:"kind,omitempty"` // solid | gradient | image
	Color    *string `json:"color,omitempty"`
	From     *string `json:"from,omitempty"`
	To       *string `json:"to,omitempty"`
	ImageKey *string `json:"image_key,omitempty"`
}

// DocThemeTheme drives colors + typography. Nil values fall through to the
// renderer's defaults (clinical teal, Inter body, Inter heading, 11pt body,
// 1.45 line-height).
type DocThemeTheme struct {
	PrimaryColor   *string  `json:"primary_color,omitempty"`
	SecondaryColor *string  `json:"secondary_color,omitempty"`
	AccentColor    *string  `json:"accent_color,omitempty"`
	TextColor      *string  `json:"text_color,omitempty"`
	MutedTextColor *string  `json:"muted_text_color,omitempty"`
	HeadingFont    *string  `json:"heading_font,omitempty"`
	BodyFont       *string  `json:"body_font,omitempty"`
	BaseSize       *float64 `json:"base_size,omitempty"`
	LineHeight     *float64 `json:"line_height,omitempty"`
	CornerRadius   *float64 `json:"corner_radius,omitempty"`
}

// DocThemeBody controls field rendering style.
type DocThemeBody struct {
	LabelStyle     *string `json:"label_style,omitempty"`
	ValueStyle     *string `json:"value_style,omitempty"`
	FieldSeparator *string `json:"field_separator,omitempty"` // dotted | solid | none
	Density        *string `json:"density,omitempty"`         // compact | comfortable | airy
	SectionHeading *string `json:"section_heading,omitempty"`
}

// DocThemeWatermark is the diagonal/tiled mark placed behind the body.
type DocThemeWatermark struct {
	Kind     string   `json:"kind,omitempty"` // none | image | text
	Asset    *string  `json:"asset,omitempty"`
	Text     *string  `json:"text,omitempty"`
	Opacity  *float64 `json:"opacity,omitempty"`
	Size     *string  `json:"size,omitempty"`
	Position *string  `json:"position,omitempty"`
}

// DocThemeFooter mirrors the `footer` block.
type DocThemeFooter struct {
	Shape     string                 `json:"shape,omitempty"`
	Fill      *DocThemeFill          `json:"fill,omitempty"`
	Text      *string                `json:"text,omitempty"`
	Slots     *DocThemeFooterSlots   `json:"slots,omitempty"`
	Content   *DocThemeFooterContent `json:"content,omitempty"`
	FinePrint *bool                  `json:"fine_print,omitempty"`
}

// DocThemeFooterSlots names the slot identifier in each footer column.
type DocThemeFooterSlots struct {
	Left   *string `json:"left,omitempty"`
	Center *string `json:"center,omitempty"`
	Right  *string `json:"right,omitempty"`
}

// DocThemeFooterContent is the free-form template override per column.
// Supports `{clinic_name}`, `{address}`, `{phone}`, `{date}`, `{page_n}`,
// `{n}`, `{contact_line}`, `{tagline}` placeholders.
type DocThemeFooterContent struct {
	Left   *string `json:"left,omitempty"`
	Center *string `json:"center,omitempty"`
	Right  *string `json:"right,omitempty"`
}

// DocThemeSignature toggles the signature block at the end of the document.
type DocThemeSignature struct {
	Show               *bool   `json:"show,omitempty"`
	Label              *string `json:"label,omitempty"`
	IncludePrintedName *bool   `json:"include_printed_name,omitempty"`
	IncludeRole        *bool   `json:"include_role,omitempty"`
	IncludeRegNo       *bool   `json:"include_reg_no,omitempty"`
	LineStyle          *string `json:"line_style,omitempty"`
}

// DocThemePage controls paper size, orientation, and margins.
type DocThemePage struct {
	Size        *string  `json:"size,omitempty"`        // a4 | letter | legal
	Orientation *string  `json:"orientation,omitempty"` // portrait | landscape
	MarginMm    *float64 `json:"margin_mm,omitempty"`
}

// DecodeDocTheme parses raw `clinic_form_style_versions.config` JSONB.
// Empty / nil bytes return a nil pointer so callers can distinguish
// "no theme configured" from "theme decoded".
func DecodeDocTheme(raw []byte) (*DocTheme, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var dt DocTheme
	if err := json.Unmarshal(raw, &dt); err != nil {
		return nil, fmt.Errorf("pdf.DecodeDocTheme: %w", err)
	}
	return &dt, nil
}

// MergeOverride returns a new DocTheme = base merged with override at the
// section-pointer level. Used to apply per-document-type overrides from
// `clinic_form_style_versions.per_doc_overrides`. Only top-level sections
// merge today (whole Header / Theme / Footer etc. swap when overridden);
// shallow-by-design so the override is predictable.
//
// nil base returns override; nil override returns base; both nil returns nil.
func MergeOverride(base, override *DocTheme) *DocTheme {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	out := *base // shallow copy
	if override.Header != nil {
		out.Header = override.Header
	}
	if override.Theme != nil {
		out.Theme = override.Theme
	}
	if override.Body != nil {
		out.Body = override.Body
	}
	if override.Watermark != nil {
		out.Watermark = override.Watermark
	}
	if override.Footer != nil {
		out.Footer = override.Footer
	}
	if override.Signature != nil {
		out.Signature = override.Signature
	}
	if override.Page != nil {
		out.Page = override.Page
	}
	if override.PresetID != nil {
		out.PresetID = override.PresetID
	}
	return &out
}
