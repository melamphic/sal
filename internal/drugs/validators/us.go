package validators

import "strings"

// usValidator enforces 21 CFR Part 1304 (records + reports) and the
// per-op rules from 1304.21–.22. DEA registration numbers and
// container-count separation (1304.11(e)(1)(iv)(A)) are the
// distinctive US requirements.
//
// Sources:
//   - https://www.ecfr.gov/current/title-21/chapter-II/part-1304
//   - https://www.ecfr.gov/current/title-21/chapter-II/part-1305 (Form 222 / CSOS)
type usValidator struct{}

func (usValidator) Country() string { return "US" }

func (usValidator) Validate(ctx OperationContext) []Issue {
	if !ctx.IsControlled {
		// 1304 applies to controlled substances. Non-CDs are out of scope.
		return nil
	}

	var issues []Issue
	c := ctx.Compliance
	isSchII := isScheduleII(ctx.Schedule)

	switch ctx.Operation {
	case OpReceive:
		// 1304.22(a)(2)(iv): name/address/DEA# of supplier on receive.
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "us_receive_supplier_name_required",
			"21 CFR 1304.22: supplier name required on receive")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "us_receive_supplier_address_required",
			"21 CFR 1304.22: supplier address required on receive")
		require(&issues, !missing(c.CounterpartyDEANumber),
			"counterparty_dea_number", "us_receive_supplier_dea_required",
			"21 CFR 1304.22: supplier DEA registration number required on receive")

		// Sch II receives must reference the Form 222 / CSOS order serial.
		if isSchII {
			require(&issues, !missing(c.OrderFormSerial),
				"order_form_serial", "us_sch2_receive_form222_required",
				"21 CFR 1305: Sch II receive must reference Form 222 / CSOS order serial")
		}

	case OpDispense:
		// 1304.22(c): patient name/address; person who dispensed; Rx info.
		require(&issues, !missing(c.CounterpartyName),
			"counterparty_name", "us_dispense_patient_name_required",
			"21 CFR 1304.22(c): patient name required on dispense")
		require(&issues, !missing(c.CounterpartyAddress),
			"counterparty_address", "us_dispense_patient_address_required",
			"21 CFR 1304.22(c): patient address required on dispense")
		require(&issues, !missing(c.PrescriptionRef),
			"prescription_ref", "us_dispense_rx_required",
			"21 CFR 1304.22(c): prescription number required on dispense")
		require(&issues, !missing(c.PrescriberDEANumber),
			"prescriber_dea_number", "us_dispense_prescriber_dea_required",
			"21 CFR 1304.22(c): prescriber DEA number required on dispense")

	case OpDiscard:
		// Form 41 destruction: witness expected (DEA Practitioner Manual).
		require(&issues, ctx.Compliance.Witnessed,
			"waste_witnessed_by", "us_discard_witness_required",
			"DEA Practitioner Manual: CD destruction must be witnessed")
	}

	// Container-count rule applies to ALL Sch II ops that move stock.
	if isSchII && (ctx.Operation == OpReceive || ctx.Operation == OpDispense) {
		require(&issues, c.CommercialContainerCount != nil,
			"commercial_container_count", "us_sch2_container_count_required",
			"21 CFR 1304.11(e)(1)(iv)(A): commercial container count required for Sch II")
		require(&issues, c.UnitsPerContainer != nil,
			"units_per_container", "us_sch2_units_per_container_required",
			"21 CFR 1304.11(e)(1)(iv)(A): units per container required for Sch II")
	}

	// Every controlled-substance op must reference the registrant's DEA
	// registration (snapshot ID linking back to dea_registrations table).
	require(&issues, !missing(c.DEARegistrationID),
		"dea_registration_id", "us_dea_registration_id_required",
		"21 CFR 1301: op must reference the registrant's DEA registration")

	return issues
}

// isScheduleII reports whether a US schedule label is Schedule II.
// Catalog values for US are 'CII','CIII','CIV','CV'.
func isScheduleII(schedule string) bool {
	s := strings.ToUpper(strings.TrimSpace(schedule))
	return s == "CII" || s == "C-II" || s == "II" || s == "SCHEDULE II"
}
