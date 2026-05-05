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
// Initials are derived from the resolved Name when the caller didn't
// supply them — keeps the brand mark non-empty even on minimal input.
//
// LogoURL is taken straight from the caller's ClinicInfo. The builder
// is responsible for fetching the signed URL from the storage layer
// (the platform pdf package can't reach storage).
func ResolveClinicFromTheme(in ClinicInfo, theme *DocTheme) ClinicInfo {
	out := in
	if theme != nil && theme.Header != nil && theme.Header.Content != nil {
		c := theme.Header.Content
		if c.ClinicName != nil && strings.TrimSpace(*c.ClinicName) != "" {
			out.Name = strings.TrimSpace(*c.ClinicName)
		}
		// theme.header.content.contact_line is the user's free-text
		// override for the address line ("14 Ponsonby Rd · 021 555…").
		if c.ContactLine != nil && strings.TrimSpace(*c.ContactLine) != "" {
			out.AddressLine1 = strings.TrimSpace(*c.ContactLine)
		}
		// theme.header.content.tagline maps to the regulatory meta line
		// when the caller hasn't provided one explicitly.
		if c.Tagline != nil && strings.TrimSpace(*c.Tagline) != "" && out.Meta == "" {
			out.Meta = strings.TrimSpace(*c.Tagline)
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
