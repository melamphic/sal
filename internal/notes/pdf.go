package notes

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/melamphic/sal/internal/domain"
)

// Default styling values used when a DocTheme section is absent or missing
// fields. Keep these in lock-step with apps/lib/.../doc_theme.dart so the
// in-app preview matches the server PDF.
const (
	defaultPrimaryColor = "#2563EB"
	defaultTextColor    = "#1A1A1A"
	defaultMutedColor   = "#6B7280"
	defaultBaseSize     = 11.0
	defaultLineHeight   = 1.4
	defaultMarginMM     = 18.0
)

// PDFSubject is the subject data used to render the system-header card.
// Resolved from the linked patient record at job time — never AI inference.
// Unused fields stay nil; the renderer skips empty values.
type PDFSubject struct {
	DisplayName       *string
	ExternalID        *string
	DOB               *string // YYYY-MM-DD
	Sex               *string
	Species           *string
	Breed             *string
	Microchip         *string
	WeightKg          *float64
	Desexed           *bool
	Color             *string
	Room              *string
	NHINumber         *string
	MedicareNumber    *string
	PreferredLanguage *string
	FundingLevel     *string
	AdmissionDate    *string
	MedicalAlerts    *string
	Medications      *string
	Allergies        *string
	ClinicianName    *string
}

// SystemHeaderConfigForPDF mirrors forms.SystemHeaderConfig but lives in the
// notes package to avoid an import cycle. Adapter sets it at job time.
type SystemHeaderConfigForPDF struct {
	Enabled bool
	Fields  []string
}

// PDFInput holds all data needed to build a branded clinical note PDF. The
// theme drives chrome (header/footer/watermark/colors); SystemHeader+Subject
// drive the patient-identity card; Fields are the form values entered by the
// clinician (or filled by AI then reviewed).
type PDFInput struct {
	Theme         *DocTheme
	ClinicName    string
	ClinicAddress *string
	ClinicPhone   *string
	ClinicEmail   *string
	FormName      string
	FormVersion   string
	Fields        []PDFField
	SubmittedAt   time.Time
	SubmittedBy   string
	NoteID        string
	SystemHeader  *SystemHeaderConfigForPDF
	Subject       *PDFSubject
	VisitDate     *time.Time
}

// PDFField is a label/value pair for rendering in the PDF body.
//
// SystemSummary, when non-nil, replaces the raw [Value] with a small
// labelled table — used for system widgets (drug op, consent, incident,
// pain score) so the PDF surfaces the typed payload as a styled card
// rather than dumping raw JSON.
//
// SystemKind identifies which system widget this is (`drug_op`,
// `consent`, `incident`, `pain_score`). Drives card colour + icon.
//
// SystemPending=true marks the card as an AI-suggested but
// unconfirmed payload — the renderer adds a "PENDING CONFIRMATION"
// banner so the regulator/auditor can tell at a glance it isn't
// ledger-bound yet.
//
// SystemReviewStatus, when non-empty, drives the witness/approval
// footer banner on the card. Values: "not_required" / "pending" /
// "approved" / "challenged". Empty for cards without an approval
// lifecycle (non-controlled drug ops, plain incidents in a country
// where regulator review isn't wired).
type PDFField struct {
	Label              string
	Value              string
	SystemSummary      []PDFSummaryItem
	SystemKind         string
	SystemPending      bool
	SystemReviewStatus string
}

// PDFSummaryItem mirrors notes.NoteFieldSystemSummaryItem — kept in
// this package's vocabulary so the renderer doesn't import service
// types.
type PDFSummaryItem struct {
	Label string
	Value string
}

// BuildNotePDF generates a doc-theme-aware branded PDF.
//
// What is honored: theme colors + typography, header/footer content slots,
// text watermarks (with opacity), the patient-header card with system fields
// resolved from the linked subject, and footer template placeholders
// ({clinic_name}/{address}/{phone}/{date}/{n}/{form_name}/{version} etc.).
//
// What is deferred: gradient fills (rendered as the `from` color), curved /
// wave header shapes (rendered as flat bands), image fills, image
// watermarks, custom TTF fonts (mapped to the closest fpdf built-in).
func BuildNotePDF(input PDFInput) (*bytes.Buffer, error) {
	theme := input.Theme
	if theme == nil {
		theme = &DocTheme{}
	}

	bodyFont := mapFont(strPtrOr(themeBodyFont(theme), ""))
	headingFont := mapFont(strPtrOr(themeHeadingFont(theme), bodyFont))
	baseSize := floatPtrOr(themeBaseSize(theme), defaultBaseSize)
	lineHeight := floatPtrOr(themeLineHeight(theme), defaultLineHeight)
	margin := floatPtrOr(pageMargin(theme), defaultMarginMM)
	pageSize, orientation := pageSpec(theme)

	primaryColor := strPtrOr(themePrimaryColor(theme), defaultPrimaryColor)
	textColor := strPtrOr(themeTextColor(theme), defaultTextColor)
	mutedColor := strPtrOr(themeMutedColor(theme), defaultMutedColor)

	pdf := fpdf.New(orientation, "mm", pageSize, "")
	pageW, pageH := pdf.GetPageSize()
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, margin+22)

	// gofpdf's built-in fonts (helvetica, times, courier) encode text as
	// Windows-1252. UTF-8 strings carrying non-Latin1 characters (em-dash
	// U+2014, smart quotes, ellipsis) get rendered byte-by-byte, so an
	// em-dash shows up as `â€"`. Install the cp1252 translator and wrap
	// every dynamic text write with `tx(...)`. Static labels are ASCII
	// and don't strictly need the wrapper, but it's idempotent so we
	// apply it everywhere for safety.
	tx := pdf.UnicodeTranslatorFromDescriptor("")

	pdf.SetFooterFunc(func() {
		drawFooter(pdf, theme, input, primaryColor, mutedColor, baseSize, bodyFont, pageW, pageH, margin, tx)
	})

	pdf.AddPage()

	headerHeight := drawHeader(pdf, theme, input, primaryColor, headingFont, bodyFont, baseSize, pageW, tx)
	drawWatermark(pdf, theme, mutedColor, headingFont, pageW, pageH, tx)

	pdf.SetY(headerHeight + 4)

	r, g, b := hexToRGB(textColor)
	pdf.SetFont(headingFont, "B", baseSize+3)
	pdf.SetTextColor(int(r), int(g), int(b))
	title := fmt.Sprintf("%s (v%s)", input.FormName, input.FormVersion)
	pdf.MultiCell(0, baseSize*0.6, tx(title), "", "L", false)
	pdf.Ln(2)

	if input.SystemHeader != nil && input.SystemHeader.Enabled && len(input.SystemHeader.Fields) > 0 {
		drawSystemHeader(pdf, input.SystemHeader.Fields, input.Subject, input.VisitDate,
			primaryColor, mutedColor, headingFont, bodyFont, baseSize, pageW, margin, tx)
		pdf.Ln(2)
	}

	mr, mg, mb := hexToRGB(mutedColor)
	pageWNow, _ := pdf.GetPageSize()
	leftM, _, rightM, _ := pdf.GetMargins()
	for i, f := range input.Fields {
		// Hairline divider above each field after the first — gives the
		// reading flow a clear rhythm without heavy borders. System
		// widget cards bring their own frame so we skip the divider in
		// front of them to avoid double-line clutter.
		if i > 0 && len(f.SystemSummary) == 0 {
			pdf.Ln(1)
			pdf.SetDrawColor(232, 232, 232)
			pdf.SetLineWidth(0.15)
			y := pdf.GetY()
			pdf.Line(leftM, y, pageWNow-rightM, y)
			pdf.Ln(2.5)
		}

		if len(f.SystemSummary) > 0 {
			drawSystemCard(pdf, f,
				int(mr), int(mg), int(mb), int(r), int(g), int(b),
				headingFont, bodyFont, baseSize, lineHeight, tx)
			continue
		}

		// Eyebrow label — caps, muted, with letter-spacing for that
		// editorial feel. Then a light gap, then the value at body
		// size with a generous line-height for long-form text.
		pdf.SetFont(headingFont, "B", baseSize-2)
		pdf.SetTextColor(int(mr), int(mg), int(mb))
		pdf.CellFormat(0, baseSize*0.55, tx(strings.ToUpper(f.Label)),
			"", 1, "L", false, 0, "")
		pdf.Ln(0.5)

		pdf.SetFont(bodyFont, "", baseSize)
		pdf.SetTextColor(int(r), int(g), int(b))
		val := f.Value
		if strings.TrimSpace(val) == "" {
			val = "—"
		}
		pdf.MultiCell(0, baseSize*lineHeight*0.62, tx(val), "", "L", false)
		pdf.Ln(2)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("notes.BuildNotePDF: %w", err)
	}
	return &buf, nil
}

// ── Header / footer / watermark ──────────────────────────────────────────────

func drawHeader(pdf *fpdf.Fpdf, theme *DocTheme, input PDFInput, primary, headingFont, bodyFont string, baseSize, pageW float64, tx func(string) string) float64 {
	bandColor := primary
	height := 28.0
	showName, showContact, showTagline := true, true, false
	clinicName := input.ClinicName
	contactLine, tagline, extraText := "", "", ""

	if theme.Header != nil {
		if theme.Header.Shape == "none" {
			return 0
		}
		switch theme.Header.Height {
		case "small":
			height = 22
		case "tall":
			height = 36
		}
		if f := theme.Header.Fill; f != nil {
			if f.Color != nil {
				bandColor = *f.Color
			} else if f.From != nil {
				bandColor = *f.From
			}
		}
		if theme.Header.ExtraText != nil {
			extraText = *theme.Header.ExtraText
		}
		if c := theme.Header.Content; c != nil {
			if c.ClinicName != nil && *c.ClinicName != "" {
				clinicName = *c.ClinicName
			}
			if c.ContactLine != nil {
				contactLine = *c.ContactLine
			}
			if c.Tagline != nil {
				tagline = *c.Tagline
			}
		}
		if s := theme.Header.Slots; s != nil {
			if s.ClinicName != nil {
				showName = *s.ClinicName
			}
			if s.ContactLine != nil {
				showContact = *s.ContactLine
			}
			if s.Tagline != nil {
				showTagline = *s.Tagline
			}
		}
	}
	if contactLine == "" {
		contactLine = defaultContactLine(input)
	}

	r, g, b := hexToRGB(bandColor)
	pdf.SetFillColor(int(r), int(g), int(b))
	pdf.Rect(0, 0, pageW, height, "F")

	pdf.SetTextColor(255, 255, 255)
	y := 6.0
	if showName && clinicName != "" {
		pdf.SetFont(headingFont, "B", baseSize+5)
		pdf.SetXY(10, y)
		pdf.CellFormat(pageW-20, 8, tx(clinicName), "", 0, "L", false, 0, "")
		y += 9
	}
	if extraText != "" {
		pdf.SetFont(bodyFont, "", baseSize-1)
		pdf.SetXY(10, y)
		pdf.CellFormat(pageW-20, 5, tx(extraText), "", 0, "L", false, 0, "")
		y += 5
	}
	if showContact && contactLine != "" {
		pdf.SetFont(bodyFont, "", baseSize-2)
		pdf.SetXY(10, y)
		pdf.CellFormat(pageW-20, 5, tx(contactLine), "", 0, "L", false, 0, "")
		y += 5
	}
	if showTagline && tagline != "" {
		pdf.SetFont(bodyFont, "I", baseSize-2)
		pdf.SetXY(10, y)
		pdf.CellFormat(pageW-20, 5, tx(tagline), "", 0, "L", false, 0, "")
	}
	return height
}

func drawFooter(pdf *fpdf.Fpdf, theme *DocTheme, input PDFInput, primary, muted string, baseSize float64, bodyFont string, pageW, pageH, margin float64, tx func(string) string) {
	bandColor := primary
	finePrint := false
	left, center, right, footerText := "", "", "", ""

	if theme.Footer != nil {
		if f := theme.Footer.Fill; f != nil {
			if f.Color != nil {
				bandColor = *f.Color
			} else if f.From != nil {
				bandColor = *f.From
			}
		}
		if theme.Footer.Text != nil {
			footerText = *theme.Footer.Text
		}
		if theme.Footer.FinePrint != nil {
			finePrint = *theme.Footer.FinePrint
		}
		if c := theme.Footer.Content; c != nil {
			if c.Left != nil {
				left = *c.Left
			}
			if c.Center != nil {
				center = *c.Center
			}
			if c.Right != nil {
				right = *c.Right
			}
		}
		if s := theme.Footer.Slots; s != nil {
			if left == "" && s.Left != nil {
				left = renderFooterSlot(*s.Left, input, pdf.PageNo())
			}
			if center == "" && s.Center != nil {
				center = renderFooterSlot(*s.Center, input, pdf.PageNo())
			}
			if right == "" && s.Right != nil {
				right = renderFooterSlot(*s.Right, input, pdf.PageNo())
			}
		}
	}
	if left == "" && center == "" && right == "" && footerText == "" {
		// No theme footer configured — fall back to the audit summary the
		// renderer always wrote pre-doc-theme so nothing regresses for
		// clinics that haven't customized.
		footerText = fmt.Sprintf("Form: %s v%s · Submitted: %s · Approved by: %s · Note: %s · Page %d",
			input.FormName, input.FormVersion,
			input.SubmittedAt.UTC().Format(time.RFC3339),
			input.SubmittedBy, input.NoteID, pdf.PageNo())
	}

	bandHeight := 18.0
	r, g, b := hexToRGB(bandColor)
	pdf.SetFillColor(int(r), int(g), int(b))
	pdf.Rect(0, pageH-bandHeight, pageW, bandHeight, "F")

	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(bodyFont, "", baseSize-2)
	colW := (pageW - margin*2) / 3
	subst := substituter(input, pdf.PageNo())

	if left != "" {
		pdf.SetXY(margin, pageH-bandHeight+5)
		pdf.CellFormat(colW, 5, tx(subst(left)), "", 0, "L", false, 0, "")
	}
	if center != "" {
		pdf.SetXY(margin+colW, pageH-bandHeight+5)
		pdf.CellFormat(colW, 5, tx(subst(center)), "", 0, "C", false, 0, "")
	}
	if right != "" {
		pdf.SetXY(margin+2*colW, pageH-bandHeight+5)
		pdf.CellFormat(colW, 5, tx(subst(right)), "", 0, "R", false, 0, "")
	}
	if footerText != "" {
		pdf.SetXY(margin, pageH-bandHeight+11)
		pdf.CellFormat(pageW-margin*2, 5, tx(subst(footerText)), "", 0, "C", false, 0, "")
	}

	if finePrint {
		mr, mg, mb := hexToRGB(muted)
		pdf.SetTextColor(int(mr), int(mg), int(mb))
		pdf.SetFont(bodyFont, "I", baseSize-3)
		pdf.SetXY(margin, pageH-3)
		pdf.CellFormat(pageW-margin*2, 3,
			tx(fmt.Sprintf("Note %s · generated %s", input.NoteID, input.SubmittedAt.UTC().Format(time.RFC3339))),
			"", 0, "C", false, 0, "")
	}
}

func drawWatermark(pdf *fpdf.Fpdf, theme *DocTheme, mutedColor, fontName string, pageW, pageH float64, tx func(string) string) {
	if theme.Watermark == nil {
		return
	}
	w := theme.Watermark
	if w.Kind != "text" || w.Text == nil || *w.Text == "" {
		return
	}
	opacity := 0.08
	if w.Opacity != nil {
		opacity = *w.Opacity
	}
	r, g, b := hexToRGB(mutedColor)
	pdf.SetTextColor(int(r), int(g), int(b))
	pdf.SetFont(fontName, "B", 64)
	pdf.SetAlpha(opacity, "Normal")
	pdf.TransformBegin()
	pdf.TransformRotate(35, pageW/2, pageH/2)
	pdf.SetXY(0, pageH/2-15)
	pdf.CellFormat(pageW, 30, tx(*w.Text), "", 0, "C", false, 0, "")
	pdf.TransformEnd()
	pdf.SetAlpha(1.0, "Normal")
}

// drawSystemCard wraps a system-widget summary in a premium card:
// kind-specific accent colour, generous padding, a vertical accent
// rail, a clear two-column body, and a status footer ("PENDING
// CONFIRMATION" or "Linked to ledger") so an auditor can tell at a
// glance whether the row is regulator-bound. Falls back gracefully
// when SystemKind is unknown or the summary is empty so a partial
// AI payload never breaks the render.
func drawSystemCard(
	pdf *fpdf.Fpdf,
	field PDFField,
	mR, mG, mB, tR, tG, tB int,
	headingFont, bodyFont string,
	baseSize, lineHeight float64,
	tx func(string) string,
) {
	pageW, _ := pdf.GetPageSize()
	left, _, right, _ := pdf.GetMargins()
	contentW := pageW - left - right

	kind := strings.TrimPrefix(field.SystemKind, "system.")
	if kind == "" || len(field.SystemSummary) == 0 {
		// Unknown / unset kind, or nothing to render — fall back to a
		// bare key-value table. Belt-and-braces so a malformed payload
		// never aborts the form pass.
		drawSystemSummary(pdf, field.SystemSummary,
			mR, mG, mB, tR, tG, tB,
			headingFont, bodyFont, baseSize, lineHeight, tx)
		return
	}
	title, accent := systemCardChrome(kind)

	startY := pdf.GetY()
	pad := 5.0
	headerH := 9.0
	railW := 1.4
	footerH := 6.0

	ar, ag, ab := hexToRGB(accent)

	// Header band — accent fill, kind title in white at baseSize+1, with
	// a status pill on the right. Rounded-corner illusion comes from the
	// surrounding outer rectangle's hairline border.
	pdf.SetFillColor(int(ar), int(ag), int(ab))
	pdf.Rect(left, startY, contentW, headerH, "F")

	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(headingFont, "B", baseSize+1)
	pdf.SetXY(left+pad, startY+1.6)
	pdf.CellFormat(contentW-pad*2-30, headerH-2, tx(strings.ToUpper(title)),
		"", 0, "L", false, 0, "")

	pillLabel := "LINKED"
	if field.SystemPending {
		pillLabel = "PENDING CONFIRMATION"
	} else {
		switch field.SystemReviewStatus {
		case "pending":
			pillLabel = "LINKED · PENDING WITNESS"
		case "approved":
			pillLabel = "LINKED · WITNESSED"
		case "challenged":
			pillLabel = "LINKED · CHALLENGED"
		}
	}
	pdf.SetFont(bodyFont, "B", baseSize-3)
	pdf.SetXY(left+pad, startY+1.6)
	pdf.CellFormat(contentW-pad*2, headerH-2, tx(pillLabel),
		"", 0, "R", false, 0, "")

	// Body — left accent rail in the kind colour, rows inside with a
	// soft tint background to separate from the surrounding form. The
	// rail anchors the eye and matches the header band visually.
	bodyY := startY + headerH
	pdf.SetFillColor(int(ar), int(ag), int(ab))
	pdf.Rect(left, bodyY, railW, 0.1, "F") // placeholder; resized after body height known

	// Background tint — derive a 6% opacity tint from accent without
	// blending math by emitting alpha-reduced white over fill. Cheap
	// trick: very faint accent fill beneath the body block.
	tintR, tintG, tintB := mixToward(int(ar), int(ag), int(ab), 245, 245, 245, 0.93)
	pdf.SetFillColor(tintR, tintG, tintB)

	// Render rows with consistent padding inside the card.
	originalLeft, originalTop, originalRight, originalBottom := pdf.GetMargins()
	pdf.SetLeftMargin(left + pad + railW)
	pdf.SetRightMargin(right + pad)
	pdf.SetY(bodyY + pad)
	drawSystemSummary(pdf, field.SystemSummary,
		mR, mG, mB, tR, tG, tB,
		headingFont, bodyFont, baseSize, lineHeight, tx)
	pdf.SetMargins(originalLeft, originalTop, originalRight)
	_ = originalBottom

	bodyEndY := pdf.GetY() + pad
	bodyHeight := bodyEndY - bodyY

	// Re-paint the rail at the correct height.
	pdf.SetFillColor(int(ar), int(ag), int(ab))
	pdf.Rect(left, bodyY, railW, bodyHeight, "F")

	// Footer caption — short, kind-specific, sets expectation of what
	// happens next ("captured at submit" for pending, "logged" for
	// linked) so the reader knows whether the row is regulator-bound.
	// When a review lifecycle applies, the footer is repainted in the
	// review-status colour and the caption swaps to a status-specific
	// line so a regulator can scan straight to the witness state.
	footerY := bodyEndY
	footerR, footerG, footerB := int(ar), int(ag), int(ab)
	switch field.SystemReviewStatus {
	case "pending":
		footerR, footerG, footerB = hexToRGBInt("D97706") // amber 600
	case "approved":
		footerR, footerG, footerB = hexToRGBInt("0F8B5C") // success green
	case "challenged":
		footerR, footerG, footerB = hexToRGBInt("B91C1C") // destructive red
	}
	pdf.SetFillColor(footerR, footerG, footerB)
	pdf.Rect(left, footerY, contentW, footerH, "F")
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(bodyFont, "I", baseSize-3)
	pdf.SetXY(left+pad+railW, footerY+1.2)
	pdf.CellFormat(contentW-pad*2-railW, footerH-2,
		tx(systemCardFooter(kind, field.SystemPending, field.SystemReviewStatus)),
		"", 0, "L", false, 0, "")

	// Outer hairline frame around the whole card to make it feel
	// contained against the body of the document.
	pdf.SetDrawColor(int(ar), int(ag), int(ab))
	pdf.SetLineWidth(0.25)
	pdf.Rect(left, startY, contentW, headerH+bodyHeight+footerH, "D")
	pdf.SetY(startY + headerH + bodyHeight + footerH + 2)
}

// mixToward blends (sR,sG,sB) toward (tR,tG,tB) by `t` (0..1, where 1 is
// fully `t`). Used to derive a soft tinted background from an accent
// colour without bringing in a full colour library.
func mixToward(sR, sG, sB, tR, tG, tB int, t float64) (int, int, int) {
	mix := func(a, b int) int { return int(float64(a)*(1-t) + float64(b)*t) }
	return mix(sR, tR), mix(sG, tG), mix(sB, tB)
}

// systemCardFooter returns the short caption shown at the bottom of a
// system widget card. Pending payloads tell the reader the row will be
// committed at submit; materialised payloads remind the reader the row
// is already in the regulator-binding ledger. When a witness/approval
// lifecycle applies, the caption swaps to a status-specific line so a
// regulator can scan straight to the witness state.
func systemCardFooter(kind string, pending bool, reviewStatus string) string {
	if pending {
		switch kind {
		case "drug_op":
			return "AI suggestion · clinician confirmation required to log to controlled-drug register"
		case "consent":
			return "AI suggestion · clinician confirmation required to record consent"
		case "incident":
			return "AI suggestion · clinician confirmation required to log incident"
		case "pain_score":
			return "AI suggestion · clinician confirmation required to record score"
		default:
			return "AI suggestion · clinician confirmation required"
		}
	}
	switch reviewStatus {
	case "pending":
		switch kind {
		case "drug_op":
			return "Logged · awaiting second-pair-of-eyes sign-off in the witness queue"
		case "consent":
			return "Logged · awaiting compliance review in the approval queue"
		case "incident":
			return "Logged · awaiting senior clinician sign-off"
		case "pain_score":
			return "Logged · awaiting review"
		default:
			return "Logged · awaiting witness sign-off"
		}
	case "approved":
		switch kind {
		case "drug_op":
			return "Linked to controlled-drug register · witness signed off"
		case "consent":
			return "Linked to patient consent ledger · reviewed and signed"
		case "incident":
			return "Linked to incident register · reviewed and signed"
		case "pain_score":
			return "Linked to patient pain trend · reviewed and signed"
		default:
			return "Linked to compliance ledger · reviewed and signed"
		}
	case "challenged":
		switch kind {
		case "drug_op":
			return "Logged · WITNESS CHALLENGED — see timeline for the reviewer's note"
		case "consent":
			return "Logged · CONSENT CHALLENGED — see timeline for the reviewer's note"
		case "incident":
			return "Logged · INCIDENT CHALLENGED — see timeline for the reviewer's note"
		case "pain_score":
			return "Logged · SCORE CHALLENGED — see timeline for the reviewer's note"
		default:
			return "Logged · CHALLENGED — see timeline for the reviewer's note"
		}
	}
	switch kind {
	case "drug_op":
		return "Linked to controlled-drug register · shelf balance updated"
	case "consent":
		return "Linked to patient consent ledger"
	case "incident":
		return "Linked to incident register · classifier ran server-side"
	case "pain_score":
		return "Linked to patient pain trend"
	default:
		return "Linked to compliance ledger"
	}
}

// hexToRGBInt is a thin wrapper around hexToRGB that returns int channels —
// fpdf's colour setters are int-typed.
func hexToRGBInt(hex string) (int, int, int) {
	r, g, b := hexToRGB(hex)
	return int(r), int(g), int(b)
}

// systemCardChrome returns the human-readable title + accent colour
// for a known system widget kind. Unknown kinds get a neutral grey.
func systemCardChrome(kind string) (title, accent string) {
	switch kind {
	case "drug_op":
		return "Drug operation", "#1e6cd1" // blue
	case "consent":
		return "Consent", "#0e9f5e" // green
	case "incident":
		return "Incident", "#c2410c" // red-orange
	case "pain_score":
		return "Pain score", "#b45309" // amber
	default:
		return strings.ReplaceAll(kind, "_", " "), "#6b7280"
	}
}

// drawSystemSummary renders the labelled key/value rows produced by a
// materialised system widget. Two columns: caps label on the left, body
// value on the right. Wraps long values onto multiple lines.
func drawSystemSummary(
	pdf *fpdf.Fpdf,
	items []PDFSummaryItem,
	mR, mG, mB, tR, tG, tB int,
	headingFont, bodyFont string,
	baseSize, lineHeight float64,
	tx func(string) string,
) {
	pageW, _ := pdf.GetPageSize()
	left, _, right, _ := pdf.GetMargins()
	contentW := pageW - left - right
	labelW := contentW * 0.32
	valueW := contentW - labelW
	for _, it := range items {
		startY := pdf.GetY()
		// Label cell.
		pdf.SetFont(headingFont, "B", baseSize-1)
		pdf.SetTextColor(mR, mG, mB)
		pdf.SetXY(left, startY)
		pdf.MultiCell(labelW, baseSize*lineHeight*0.55, tx(strings.ToUpper(it.Label)), "", "L", false)
		labelEndY := pdf.GetY()
		// Value cell — top-aligned with label.
		pdf.SetFont(bodyFont, "", baseSize)
		pdf.SetTextColor(tR, tG, tB)
		pdf.SetXY(left+labelW, startY)
		val := it.Value
		if strings.TrimSpace(val) == "" {
			val = "—"
		}
		pdf.MultiCell(valueW, baseSize*lineHeight*0.55, tx(val), "", "L", false)
		valueEndY := pdf.GetY()
		// Move cursor below the taller of the two cells.
		if valueEndY > labelEndY {
			pdf.SetY(valueEndY)
		} else {
			pdf.SetY(labelEndY)
		}
	}
}

// ── System header card ──────────────────────────────────────────────────────

func drawSystemHeader(pdf *fpdf.Fpdf, fields []string, subject *PDFSubject, visitDate *time.Time, primary, muted, headingFont, bodyFont string, baseSize, pageW, margin float64, tx func(string) string) {
	rows := buildHeaderRows(fields, subject, visitDate)
	if len(rows) == 0 {
		return
	}

	r, g, b := hexToRGB(primary)
	mr, mg, mb := hexToRGB(muted)

	pad := 4.0
	colW := (pageW - margin*2 - pad*2) / 2
	rowH := baseSize * 0.95
	totalRows := (len(rows) + 1) / 2
	cardH := pad*2 + 6 + float64(totalRows)*rowH

	startY := pdf.GetY()
	pdf.SetDrawColor(int(r), int(g), int(b))
	pdf.SetLineWidth(0.3)
	pdf.RoundedRect(margin, startY, pageW-margin*2, cardH, 2.0, "1234", "D")

	pdf.SetFont(headingFont, "B", baseSize-2)
	pdf.SetTextColor(int(r), int(g), int(b))
	pdf.SetXY(margin+pad, startY+pad-1)
	pdf.CellFormat(pageW-margin*2-pad*2, 5, "PATIENT", "", 0, "L", false, 0, "")

	for i, row := range rows {
		col := i % 2
		rowIdx := i / 2
		x := margin + pad + float64(col)*colW
		y := startY + pad + 6 + float64(rowIdx)*rowH

		pdf.SetFont(headingFont, "B", baseSize-3)
		pdf.SetTextColor(int(mr), int(mg), int(mb))
		pdf.SetXY(x, y)
		pdf.CellFormat(colW, rowH*0.5, tx(strings.ToUpper(row.Label)), "", 0, "L", false, 0, "")

		pdf.SetFont(bodyFont, "", baseSize-1)
		pdf.SetTextColor(0, 0, 0)
		pdf.SetXY(x, y+rowH*0.5)
		pdf.CellFormat(colW, rowH*0.5, tx(row.Value), "", 0, "L", false, 0, "")
	}
	pdf.SetY(startY + cardH + 2)
}

type headerRow struct {
	Label string
	Value string
}

func buildHeaderRows(fields []string, s *PDFSubject, visitDate *time.Time) []headerRow {
	rows := make([]headerRow, 0, len(fields))
	for _, f := range fields {
		label, value := resolveHeaderField(f, s, visitDate)
		if label == "" {
			continue
		}
		if value == "" {
			value = "—"
		}
		rows = append(rows, headerRow{Label: label, Value: value})
	}
	return rows
}

func resolveHeaderField(key string, s *PDFSubject, visitDate *time.Time) (string, string) {
	if s == nil {
		s = &PDFSubject{}
	}
	switch key {
	case "name":
		return "Name", strPtrOr(s.DisplayName, "")
	case "photo":
		// Photo rendering deferred — needs presigned image fetch + cache.
		return "", ""
	case "id":
		return "Patient ID", strPtrOr(s.ExternalID, "")
	case "dob":
		return "Date of Birth", strPtrOr(s.DOB, "")
	case "age":
		return "Age", ageFromDOB(strPtrOr(s.DOB, ""))
	case "sex":
		return "Sex", strPtrOr(s.Sex, "")
	case "visit_date":
		v := ""
		if visitDate != nil {
			v = visitDate.UTC().Format("2006-01-02")
		}
		return "Visit Date", v
	case "clinician":
		return "Clinician", strPtrOr(s.ClinicianName, "")
	case "species":
		return "Species", strPtrOr(s.Species, "")
	case "breed":
		return "Breed", strPtrOr(s.Breed, "")
	case "microchip":
		return "Microchip", strPtrOr(s.Microchip, "")
	case "weight":
		if s.WeightKg != nil {
			return "Weight", fmt.Sprintf("%.1f kg", *s.WeightKg)
		}
		return "Weight", ""
	case "desexed":
		if s.Desexed != nil {
			if *s.Desexed {
				return "Desexed", "Yes"
			}
			return "Desexed", "No"
		}
		return "Desexed", ""
	case "color":
		return "Color", strPtrOr(s.Color, "")
	case "room":
		return "Room", strPtrOr(s.Room, "")
	case "nhi_number":
		return "NHI Number", strPtrOr(s.NHINumber, "")
	case "medicare_number":
		return "Medicare Number", strPtrOr(s.MedicareNumber, "")
	case "preferred_language":
		return "Preferred Language", strPtrOr(s.PreferredLanguage, "")
	case "funding_level":
		return "Funding Level", strPtrOr(s.FundingLevel, "")
	case "admission_date":
		return "Admission Date", strPtrOr(s.AdmissionDate, "")
	case "medical_alerts":
		return "Medical Alerts", strPtrOr(s.MedicalAlerts, "")
	case "medications":
		return "Medications", strPtrOr(s.Medications, "")
	case "allergies":
		return "Allergies", strPtrOr(s.Allergies, "")
	}
	return "", ""
}

// ── Templating ───────────────────────────────────────────────────────────────

// renderFooterSlot is the canned-string fallback the renderer uses when only
// a slot identifier (not a full template) is configured. Matches the slot
// vocabulary used by the form-style designer: "address", "form_meta",
// "page_number", "contact".
func renderFooterSlot(slot string, input PDFInput, pageNo int) string {
	switch slot {
	case "address":
		return strPtrOr(input.ClinicAddress, "")
	case "form_meta":
		return fmt.Sprintf("%s v%s · Approved by %s", input.FormName, input.FormVersion, input.SubmittedBy)
	case "page_number":
		return fmt.Sprintf("Page %d", pageNo)
	case "contact":
		phone := strPtrOr(input.ClinicPhone, "")
		email := strPtrOr(input.ClinicEmail, "")
		switch {
		case phone != "" && email != "":
			return phone + " · " + email
		case phone != "":
			return phone
		case email != "":
			return email
		}
		return ""
	case "":
		return ""
	}
	return slot
}

func substituter(input PDFInput, pageNo int) func(string) string {
	repl := strings.NewReplacer(
		"{clinic_name}", input.ClinicName,
		"{address}", strPtrOr(input.ClinicAddress, ""),
		"{phone}", strPtrOr(input.ClinicPhone, ""),
		"{email}", strPtrOr(input.ClinicEmail, ""),
		"{date}", input.SubmittedAt.UTC().Format("2006-01-02"),
		"{page_n}", strconv.Itoa(pageNo),
		"{n}", strconv.Itoa(pageNo),
		"{form_name}", input.FormName,
		"{version}", input.FormVersion,
		"{contact_line}", defaultContactLine(input),
		"{tagline}", "",
		"{reg_no}", "",
		"{license}", "",
		"{facility_name}", input.ClinicName,
	)
	return func(s string) string { return repl.Replace(s) }
}

func defaultContactLine(input PDFInput) string {
	parts := make([]string, 0, 3)
	if a := strPtrOr(input.ClinicAddress, ""); a != "" {
		parts = append(parts, a)
	}
	if p := strPtrOr(input.ClinicPhone, ""); p != "" {
		parts = append(parts, p)
	}
	if e := strPtrOr(input.ClinicEmail, ""); e != "" {
		parts = append(parts, e)
	}
	return strings.Join(parts, " · ")
}

// ── Theme accessors (nil-safe) ───────────────────────────────────────────────

func themePrimaryColor(t *DocTheme) *string {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.PrimaryColor
}
func themeTextColor(t *DocTheme) *string {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.TextColor
}
func themeMutedColor(t *DocTheme) *string {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.MutedTextColor
}
func themeBodyFont(t *DocTheme) *string {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.BodyFont
}
func themeHeadingFont(t *DocTheme) *string {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.HeadingFont
}
func themeBaseSize(t *DocTheme) *float64 {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.BaseSize
}
func themeLineHeight(t *DocTheme) *float64 {
	if t.Theme == nil {
		return nil
	}
	return t.Theme.LineHeight
}
func pageMargin(t *DocTheme) *float64 {
	if t.Page == nil {
		return nil
	}
	return t.Page.MarginMm
}

func pageSpec(t *DocTheme) (size, orientation string) {
	size, orientation = "A4", "P"
	if t.Page == nil {
		return
	}
	if t.Page.Size != nil {
		switch strings.ToLower(*t.Page.Size) {
		case "a4":
			size = "A4"
		case "letter":
			size = "Letter"
		case "legal":
			size = "Legal"
		}
	}
	if t.Page.Orientation != nil && strings.ToLower(*t.Page.Orientation) == "landscape" {
		orientation = "L"
	}
	return
}

// ── Tiny helpers ─────────────────────────────────────────────────────────────

// mapFont normalises a theme font name into one fpdf supports built-in. We
// don't load custom TTFs server-side yet, so anything outside Helvetica /
// Times / Courier falls back to Helvetica.
func mapFont(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lora", "merriweather", "times":
		return "Times"
	case "courier":
		return "Courier"
	default:
		return "Helvetica"
	}
}

func strPtrOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

func floatPtrOr(p *float64, fallback float64) float64 {
	if p == nil {
		return fallback
	}
	return *p
}

func ageFromDOB(dob string) string {
	if dob == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", dob)
	if err != nil {
		return ""
	}
	now := domain.TimeNow()
	years := now.Year() - t.Year()
	if now.YearDay() < t.YearDay() {
		years--
	}
	return strconv.Itoa(years) + "y"
}

// hexToRGB converts a hex color string to RGB components.
func hexToRGB(hex string) (uint8, uint8, uint8) {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return 37, 99, 235
	}
	var r, g, b uint8
	_, _ = fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return r, g, b
}
