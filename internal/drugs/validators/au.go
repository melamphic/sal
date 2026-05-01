package validators

// auValidator dispatches to a per-state rule set. AU CD record-keeping
// is governed by state poisons regs (Therapeutic Goods Act 1989 sets
// scheduling but state law sets register format).
//
// Sources:
//   - NSW Poisons and Therapeutic Goods Regulation 2008 (PD2022_032)
//   - VIC Drugs, Poisons and Controlled Substances Regulations 2017
//   - QLD Medicines and Poisons (Medicines) Regulation 2021
//   - WA Medicines and Poisons Regulations 2016 (strictest)
//   - SA / TAS / ACT / NT — covered by state-rules table
//
// Per the design doc §3.3 we cover all 8 states day one. Default
// rules are WA-strict; states relax via flags in au_state_rules.go.
type auValidator struct{}

func (auValidator) Country() string { return "AU" }

func (auValidator) Validate(ctx OperationContext) []Issue {
	if !ctx.IsControlled {
		return nil
	}

	rules := ruleForState(ctx.ClinicState)

	var issues []Issue
	c := ctx.Compliance

	switch ctx.Operation {
	case OpReceive:
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "au_receive_supplier_name_required",
			"AU state poisons reg: supplier name required on receive")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "au_receive_supplier_address_required",
			"AU state poisons reg: supplier address required on receive")
		require(&issues, !missing(c.BatchNumber),
			"batch_number", "au_receive_batch_required",
			"AU state poisons reg: batch number required on Sch 8 receive")
		require(&issues, !missing(c.ExpiryDate),
			"expiry_date", "au_receive_expiry_required",
			"AU state poisons reg: expiry date required on Sch 8 receive")

	case OpDispense:
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "au_dispense_recipient_name_required",
			"AU state poisons reg: recipient name required on dispense")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "au_dispense_recipient_address_required",
			"AU state poisons reg: recipient address required on dispense")
		if !missing(c.PrescriptionRef) {
			require(&issues, !missing(c.PrescriberName),
				"prescriber_name", "au_dispense_prescriber_name_required",
				"AU state poisons reg: prescriber name required on Rx dispense")
			require(&issues, !missing(c.PrescriberAddress),
				"prescriber_address", "au_dispense_prescriber_address_required",
				"AU state poisons reg: prescriber address required on Rx dispense")
		}
		if rules.RequireDispenseWitness {
			require(&issues, ctx.Compliance.Witnessed,
				"witnessed_by", "au_dispense_witness_required",
				"AU "+rules.State+" poisons reg: dispense must be witnessed")
		}

	case OpDiscard:
		// All states require destruction-witness for Sch 8.
		require(&issues, ctx.Compliance.Witnessed,
			"waste_witnessed_by", "au_discard_witness_required",
			"AU state poisons reg: Sch 8 destruction must be witnessed")
		if rules.RequireDestructionReason {
			require(&issues, !missing(c.WasteReason),
				"waste_reason", "au_discard_reason_required",
				"AU "+rules.State+" poisons reg: destruction reason required")
		}
	}

	return issues
}
