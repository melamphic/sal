package auth

import "github.com/melamphic/sal/internal/domain"

// RegulatorDefault is the suggested authority + label shown to the
// invitee on the accept-invite page based on the clinic's vertical and
// country. Pre-fills the authority field so the invitee only needs to
// type their registration number.
type RegulatorDefault struct {
	Authority      string // short code printed on signed PDFs ("VCNZ", "RCVS", "GMC")
	AuthorityLabel string // human-readable form for the placeholder
	NumberLabel    string // what the registration number is called
}

// SuggestedRegulator returns the default authority + reg-number labels
// for the supplied (vertical, country). Returns the zero value when no
// match is registered — the FE then renders the authority field as a
// blank text input the invitee fills in manually.
//
// We hand-curate this map per (vertical, country) rather than scraping
// every regulator on earth. The four verticals × four launch markets
// (NZ, AU, UK, US) cover ~95% of expected sign-ups; everything else
// falls through to a freeform field.
func SuggestedRegulator(vertical domain.Vertical, country string) RegulatorDefault {
	type key struct {
		vert    domain.Vertical
		country string
	}
	switch (key{vertical, country}) {
	// Veterinary
	case key{domain.VerticalVeterinary, "NZ"}:
		return RegulatorDefault{
			Authority: "VCNZ", AuthorityLabel: "Veterinary Council of New Zealand",
			NumberLabel: "VCNZ registration #",
		}
	case key{domain.VerticalVeterinary, "AU"}:
		return RegulatorDefault{
			Authority: "AVBC", AuthorityLabel: "Australasian Veterinary Boards Council",
			NumberLabel: "Vet board registration #",
		}
	case key{domain.VerticalVeterinary, "UK"}:
		return RegulatorDefault{
			Authority: "RCVS", AuthorityLabel: "Royal College of Veterinary Surgeons",
			NumberLabel: "RCVS membership #",
		}
	case key{domain.VerticalVeterinary, "US"}:
		return RegulatorDefault{
			Authority: "STATE_VET", AuthorityLabel: "State veterinary board",
			NumberLabel: "State vet license #",
		}

	// Dental
	case key{domain.VerticalDental, "NZ"}:
		return RegulatorDefault{
			Authority: "DCNZ", AuthorityLabel: "Dental Council of New Zealand",
			NumberLabel: "DCNZ registration #",
		}
	case key{domain.VerticalDental, "AU"}:
		return RegulatorDefault{
			Authority: "DBA", AuthorityLabel: "Dental Board of Australia",
			NumberLabel: "AHPRA dental registration #",
		}
	case key{domain.VerticalDental, "UK"}:
		return RegulatorDefault{
			Authority: "GDC", AuthorityLabel: "General Dental Council",
			NumberLabel: "GDC #",
		}
	case key{domain.VerticalDental, "US"}:
		return RegulatorDefault{
			Authority: "STATE_DENT", AuthorityLabel: "State dental board",
			NumberLabel: "State dental license #",
		}

	// Aged care (nursing-led)
	case key{domain.VerticalAgedCare, "NZ"}:
		return RegulatorDefault{
			Authority: "NCNZ", AuthorityLabel: "Nursing Council of New Zealand",
			NumberLabel: "NCNZ APC #",
		}
	case key{domain.VerticalAgedCare, "AU"}:
		return RegulatorDefault{
			Authority: "NMBA", AuthorityLabel: "Nursing & Midwifery Board of Australia",
			NumberLabel: "AHPRA nursing registration #",
		}
	case key{domain.VerticalAgedCare, "UK"}:
		return RegulatorDefault{
			Authority: "NMC", AuthorityLabel: "Nursing & Midwifery Council",
			NumberLabel: "NMC PIN",
		}
	case key{domain.VerticalAgedCare, "US"}:
		return RegulatorDefault{
			Authority: "STATE_NURSE", AuthorityLabel: "State nursing board",
			NumberLabel: "State RN license #",
		}

	// General clinic (medical doctors)
	case key{domain.VerticalGeneralClinic, "NZ"}:
		return RegulatorDefault{
			Authority: "MCNZ", AuthorityLabel: "Medical Council of New Zealand",
			NumberLabel: "MCNZ registration #",
		}
	case key{domain.VerticalGeneralClinic, "AU"}:
		return RegulatorDefault{
			Authority: "MBA", AuthorityLabel: "Medical Board of Australia",
			NumberLabel: "AHPRA medical registration #",
		}
	case key{domain.VerticalGeneralClinic, "UK"}:
		return RegulatorDefault{
			Authority: "GMC", AuthorityLabel: "General Medical Council",
			NumberLabel: "GMC #",
		}
	case key{domain.VerticalGeneralClinic, "US"}:
		return RegulatorDefault{
			Authority: "STATE_MED", AuthorityLabel: "State medical board",
			NumberLabel: "State medical license #",
		}
	}
	return RegulatorDefault{}
}

// SuggestedTitles returns common professional title prefixes for the
// supplied vertical. Used by the invite page to render a quick-pick
// row above the title input — invitees can also type their own.
func SuggestedTitles(vertical domain.Vertical) []string {
	switch vertical {
	case domain.VerticalVeterinary:
		return []string{"Dr.", "BVSc", "DVM", "RVN", "VN"}
	case domain.VerticalDental:
		return []string{"Dr.", "BDS", "DDS", "Hygienist"}
	case domain.VerticalAgedCare:
		return []string{"RN", "EN", "NP", "CNA", "RA"}
	case domain.VerticalGeneralClinic:
		return []string{"Dr.", "MBBS", "MD", "NP", "RN"}
	}
	return []string{"Dr.", "Mr.", "Ms.", "Mx."}
}

// RoleNeedsRegulatorID reports whether the supplied role/permissions
// combination should require regulator authority + reg-no on the
// accept-invite form. Receptionists / non-clinical hires don't carry
// a regulator number, so we don't block them on it. Clinical roles
// (vet, vet_nurse, dentist, RN, etc.) do.
//
// We key off permissions rather than role slug so a customised role
// (e.g. an admin who also dispenses) gets the right behaviour.
func RoleNeedsRegulatorID(role domain.StaffRole, perms domain.Permissions) bool {
	if perms.Dispense || perms.SubmitForms {
		return true
	}
	// Belt-and-braces by role too — a fresh invite's perms might be
	// stripped by an admin but the role label still implies clinical
	// duties.
	switch role {
	case domain.StaffRoleVet, domain.StaffRoleVetNurse:
		return true
	}
	return false
}
