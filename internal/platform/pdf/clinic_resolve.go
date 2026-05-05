package pdf

import (
	"strings"
	"unicode"
)

// ResolveClinicFromTheme merges the caller-supplied ClinicInfo with
// the doc-theme's content overrides (theme.header.content.{clinic_name,
// contact_line, tagline}) so the doc-builder's "use this name on
// every PDF" intent actually flows. Theme overrides win when set.
//
// Slot toggles (theme.header.slots.{clinic_name, contact_line, tagline})
// drive which fields the partial actually renders. When the theme
// doesn't supply slots the defaults are: clinic name on, contact line
// on, tagline off — same defaults the designer ships.
//
// Extra text (theme.header.extra_text) is forwarded as-is for the
// right-aligned strapline.
//
// Initials are derived from the resolved Name when the caller didn't
// supply them — keeps the brand mark non-empty even on minimal input.
//
// LogoURL is taken straight from the caller's ClinicInfo. The builder
// is responsible for fetching the signed URL from the storage layer
// (the platform pdf package can't reach storage).
func ResolveClinicFromTheme(in ClinicInfo, theme *DocTheme) ClinicInfo {
	out := in
	// Defaults — designer ships these on for clinic-name and
	// contact-line and off for tagline.
	out.ShowName = true
	out.ShowAddressLine = true
	out.ShowMeta = false

	if theme != nil && theme.Header != nil {
		h := theme.Header
		if h.Content != nil {
			c := h.Content
			if c.ClinicName != nil && strings.TrimSpace(*c.ClinicName) != "" {
				out.Name = strings.TrimSpace(*c.ClinicName)
			}
			// theme.header.content.contact_line is the user's free-text
			// override for the address line ("14 Ponsonby Rd · 021 555…").
			if c.ContactLine != nil && strings.TrimSpace(*c.ContactLine) != "" {
				out.AddressLine1 = strings.TrimSpace(*c.ContactLine)
			}
			// theme.header.content.tagline always populates Meta when
			// supplied — the slot toggle below controls visibility.
			if c.Tagline != nil && strings.TrimSpace(*c.Tagline) != "" {
				out.Meta = strings.TrimSpace(*c.Tagline)
			}
		}
		if h.Slots != nil {
			if h.Slots.ClinicName != nil {
				out.ShowName = *h.Slots.ClinicName
			}
			if h.Slots.ContactLine != nil {
				out.ShowAddressLine = *h.Slots.ContactLine
			}
			if h.Slots.Tagline != nil {
				out.ShowMeta = *h.Slots.Tagline
			}
		}
		if h.ExtraText != nil {
			out.ExtraText = strings.TrimSpace(*h.ExtraText)
		}
	}
	if out.Initials == "" {
		out.Initials = deriveInitials(out.Name)
	}
	return out
}

// deriveInitials returns 1-2 uppercase letters from the supplied name.
// Strips punctuation, takes the first letter of each of the first two
// words. Falls back to "C" (Clinic) when name is empty so a brand
// mark always renders something.
func deriveInitials(name string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	if len(parts) == 0 {
		return "C"
	}
	out := make([]rune, 0, 2)
	for _, p := range parts {
		for _, r := range p {
			if unicode.IsLetter(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) >= 2 {
			break
		}
	}
	if len(out) == 0 {
		return "C"
	}
	return string(out)
}
