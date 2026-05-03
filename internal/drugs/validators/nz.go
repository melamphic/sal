package validators

import "strings"

// nzValidator enforces Misuse of Drugs Regulations 1977 Reg 37–46
// (register format + retention) and the Class A/B/C distinctions
// per Reg 29 / 31.
//
// Class A and B controlled drugs require the formal register with
// counterparty and prescriber details. Class C drugs require records
// but with looser format (invoices + prescription records suffice).
//
// Sources:
//   - https://www.legislation.govt.nz/regulation/public/1977/0037/latest/whole.html
//   - VCNZ Code of Professional Conduct (NZ vet) — monthly recon best practice
//   - Medicines Care Guides for Residential Aged Care 2011
type nzValidator struct{}

func (nzValidator) Country() string { return "NZ" }

func (nzValidator) Validate(ctx OperationContext) []Issue {
	if !ctx.IsControlled {
		return nil
	}

	var issues []Issue
	c := ctx.Compliance
	classAB := isClassAOrB(ctx.Schedule)

	// Reg 40: running balance is part of the register. Service computes
	// balance_before/after on every op already, so no field check here —
	// it's structural.

	switch ctx.Operation {
	case OpReceive:
		if classAB {
			require(&issues, !missing(c.CounterpartyName),
				"counterparty_name", "nz_receive_supplier_name_required",
				"NZ Reg 40: supplier name required on Class A/B receive")
			require(&issues, !missing(c.CounterpartyAddress),
				"counterparty_address", "nz_receive_supplier_address_required",
				"NZ Reg 40: supplier address required on Class A/B receive")
		}

	case OpDispense:
		if classAB {
			// Patient identity (covered by system patient header in our
			// architecture — see project memory feedback_system_fields).
			// We still require the counterparty fields on the ledger
			// row for legal-artefact purposes.
			require(&issues, !missing(c.CounterpartyName),
				"counterparty_name", "nz_dispense_recipient_name_required",
				"NZ Reg 40: recipient name required on Class A/B dispense")
			require(&issues, !missing(c.CounterpartyAddress),
				"counterparty_address", "nz_dispense_recipient_address_required",
				"NZ Reg 40: recipient address required on Class A/B dispense")
			if !missing(c.PrescriptionRef) {
				require(&issues, !missing(c.PrescriberName),
					"prescriber_name", "nz_dispense_prescriber_name_required",
					"NZ Reg 40: prescriber name required when dispensing on prescription")
				require(&issues, !missing(c.PrescriberAddress),
					"prescriber_address", "nz_dispense_prescriber_address_required",
					"NZ Reg 40: prescriber address required when dispensing on prescription")
			}
		}

	case OpDiscard:
		// VCNZ Code: destruction signed by two staff. We treat this as
		// a hard rule (witness required) — pharmacy + vet practice
		// share the expectation.
		require(&issues, ctx.Compliance.Witnessed,
			"waste_witnessed_by", "nz_discard_witness_required",
			"NZ VCNZ/Pharmacy Council: CD destruction must be witnessed")
	}

	return issues
}

// isClassAOrB returns true when the catalog schedule string indicates
// Class A or Class B (the formal-register classes per NZ Reg 29).
// Class C is records-required-but-format-relaxed.
func isClassAOrB(schedule string) bool {
	s := strings.ToUpper(strings.TrimSpace(schedule))
	switch s {
	case "S1", "CLASS A", "A":
		return true
	case "S2", "CLASS B", "B":
		return true
	default:
		return false
	}
}
