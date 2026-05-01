package validators

// ukValidator enforces UK Misuse of Drugs Regulations 2001 — Schedule 6
// (register columns) + Reg 16 / Health Act 2006 amendments
// (collector identity).
//
// Sources:
//   - https://www.legislation.gov.uk/uksi/2001/3998/schedule/6
//   - RPS MEP §3.6.11 — Record-keeping and CD registers
//   - CQC controlled drugs in care homes guidance
type ukValidator struct{}

func (ukValidator) Country() string { return "UK" }

func (ukValidator) Validate(ctx OperationContext) []Issue {
	if !ctx.IsControlled {
		// UK Sch 6 register columns only apply to Sch 2/3 CDs. Non-CDs
		// fall under standard medicines records (out of scope here).
		return nil
	}

	var issues []Issue
	c := ctx.Compliance

	switch ctx.Operation {
	case OpReceive:
		// Sch 6 Part I — drugs obtained: name+address of supplier, qty.
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "uk_receive_supplier_name_required",
			"UK Sch 6 Part I: name of supplier required on receive")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "uk_receive_supplier_address_required",
			"UK Sch 6 Part I: address of supplier required on receive")

	case OpDispense:
		// Sch 6 Part II — drugs supplied: name+address of person
		// supplied; if on prescription, prescriber name+address.
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "uk_dispense_recipient_name_required",
			"UK Sch 6 Part II: name of person supplied required on dispense")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "uk_dispense_recipient_address_required",
			"UK Sch 6 Part II: address of person supplied required on dispense")

		// Prescriber required when dispensed against a prescription
		// (typical for community pharmacy + outpatient dispense).
		if !missing(c.PrescriptionRef) {
			require(&issues, !missing(c.PrescriberName),
				"prescriber_name", "uk_dispense_prescriber_name_required",
				"UK Sch 6 Part II: prescriber name required when dispensing on prescription")
			require(&issues, !missing(c.PrescriberAddress),
				"prescriber_address", "uk_dispense_prescriber_address_required",
				"UK Sch 6 Part II: prescriber address required when dispensing on prescription")
		}

		// Reg 16 + Health Act 2006: collector ID must be recorded
		// (whether evidence was requested + whether provided).
		require(&issues, !missing(c.CollectorName),
			"collector_name", "uk_dispense_collector_name_required",
			"UK Reg 16: collector name required on Sch 2 dispense")
		require(&issues, !missingBool(c.CollectorIDEvidenceRequested),
			"collector_id_evidence_requested", "uk_dispense_collector_id_requested_required",
			"UK Reg 16: must record whether collector ID evidence was requested (Y/N)")
		require(&issues, !missingBool(c.CollectorIDEvidenceProvided),
			"collector_id_evidence_provided", "uk_dispense_collector_id_provided_required",
			"UK Reg 16: must record whether collector ID evidence was provided (Y/N)")

	case OpDiscard:
		// Destruction of CDs needs witness + reason. CQC + RPS expect
		// a destruction record carrying the witness identity.
		require(&issues, ctx.Compliance.Witnessed,
			"waste_witnessed_by", "uk_discard_witness_required",
			"UK CQC/RPS: destruction of Sch 2 CD must be witnessed")

	case OpTransfer:
		// Treated as a supply for register purposes.
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "uk_transfer_recipient_name_required",
			"UK Sch 6: recipient name required on transfer")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "uk_transfer_recipient_address_required",
			"UK Sch 6: recipient address required on transfer")
	}

	return issues
}
