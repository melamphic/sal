package notes

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

// DocThemeHeader maps the `header` block: band shape + fill, optional text
// content overrides ("clinic_name", "contact_line", "tagline") and slot
// visibility flags.
type DocThemeHeader struct {
	Shape     string           `json:"shape,omitempty"`  // flat | single_curve | double_wave | diagonal | none
	Fill      *DocThemeFill    `json:"fill,omitempty"`
	Height    string           `json:"height,omitempty"` // small | medium | tall
	ExtraText *string          `json:"extra_text,omitempty"`
	LogoKey   *string          `json:"logo_key,omitempty"`
	Content   *DocThemeContent `json:"content,omitempty"`
	Slots     *DocThemeSlots   `json:"slots,omitempty"`
}

// DocThemeContent is the free-form text overrides set by the form author.
// Null / empty fields fall back to the clinic's saved profile data.
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

// DocThemeFill is a band fill — solid color, gradient, or image. The
// renderer currently honours solid + the gradient's `from` color (treated
// as a solid). Full gradient + image support is a follow-up.
type DocThemeFill struct {
	Kind     string  `json:"kind,omitempty"` // solid | gradient | image
	Color    *string `json:"color,omitempty"`
	From     *string `json:"from,omitempty"`
	To       *string `json:"to,omitempty"`
	ImageKey *string `json:"image_key,omitempty"`
}

// DocThemeTheme drives colors + typography. `primary_color` is mandatory in
// presets but optional in the JSON for backward-compat — renderer falls back
// to a default brand blue when absent.
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
	LabelStyle      *string `json:"label_style,omitempty"`
	ValueStyle      *string `json:"value_style,omitempty"`
	FieldSeparator  *string `json:"field_separator,omitempty"` // dotted | solid | none
	Density         *string `json:"density,omitempty"`         // compact | comfortable | airy
	SectionHeading  *string `json:"section_heading,omitempty"`
}

// DocThemeWatermark is the diagonal/tiled mark placed behind the body.
// `kind=text` renders the `text` string at the configured opacity; image
// watermarks are deferred to a follow-up.
type DocThemeWatermark struct {
	Kind     string   `json:"kind,omitempty"` // none | image | text
	Asset    *string  `json:"asset,omitempty"`
	Text     *string  `json:"text,omitempty"`
	Opacity  *float64 `json:"opacity,omitempty"`
	Size     *string  `json:"size,omitempty"`
	Position *string  `json:"position,omitempty"`
}

// DocThemeFooter mirrors the `footer` block. Slot+content templates support
// `{clinic_name}`, `{address}`, `{phone}`, `{date}`, `{page_n}`, `{n}`,
// `{contact_line}`, `{tagline}` placeholders.
type DocThemeFooter struct {
	Shape     string           `json:"shape,omitempty"`
	Fill      *DocThemeFill    `json:"fill,omitempty"`
	Text      *string          `json:"text,omitempty"`
	Slots     *DocThemeFooterSlots   `json:"slots,omitempty"`
	Content   *DocThemeFooterContent `json:"content,omitempty"`
	FinePrint *bool            `json:"fine_print,omitempty"`
}

// DocThemeFooterSlots names the slot identifier in each of the three footer
// columns. The renderer uses this when the matching template string is
// absent — older configs never carried templates.
type DocThemeFooterSlots struct {
	Left   *string `json:"left,omitempty"`
	Center *string `json:"center,omitempty"`
	Right  *string `json:"right,omitempty"`
}

// DocThemeFooterContent is the free-form template override for each column.
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

// DocThemePage controls the paper size, orientation, and margins.
type DocThemePage struct {
	Size        *string  `json:"size,omitempty"`        // a4 | letter | legal
	Orientation *string  `json:"orientation,omitempty"` // portrait | landscape
	MarginMm    *float64 `json:"margin_mm,omitempty"`
}

// DecodeDocTheme parses the raw `clinic_form_style_versions.config` JSONB
// into a typed value. Empty / nil bytes return a nil pointer so callers can
// distinguish "no theme configured" from "theme decoded".
func DecodeDocTheme(raw []byte) (*DocTheme, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var dt DocTheme
	if err := json.Unmarshal(raw, &dt); err != nil {
		return nil, fmt.Errorf("notes.DecodeDocTheme: %w", err)
	}
	return &dt, nil
}
