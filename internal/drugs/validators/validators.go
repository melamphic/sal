// Package validators enforces per-country compliance rules on drug
// operations. Each country (UK / US / NZ / AU) has different mandatory
// fields and different rules about who counts as a witness, what
// counterparty info must be captured on a receive vs supply, and so
// on. This package is the single source of truth for those rules.
//
// Design: docs/drug-register-compliance-v2.md §5.1
//
// Wiring: drugs.Service builds an OperationContext from its
// LogOperationInput, calls Dispatch(country).Validate(ctx), and either
// blocks (Mode == Enforce) or logs (Mode == Shadow) the returned
// Issues. Phase 1 ships in Shadow mode behind the
// drug_register.compliance_v2 feature flag — we want to see what real
// clinic traffic violates the new rules before we block it.
//
// Adding a country: write a new file (e.g. ie.go) implementing
// Validator, add it to the dispatch table in dispatcher().
//
// Adding a state (AU): edit au_state_rules.go — table-driven, no
// branches in code.
package validators

import (
	"strings"
)

// Mode controls whether returned Issues block the operation.
//
//   - Shadow: Issues are logged + telemetry'd; the op proceeds.
//   - Enforce: any Issue with Severity == Error blocks the op (returns
//     domain.ErrValidation in the calling service).
type Mode int

const (
	Shadow Mode = iota
	Enforce
)

// Severity classifies an Issue. Warnings never block even in Enforce
// mode; Errors block in Enforce mode and are logged loudly in Shadow.
type Severity int

const (
	Warning Severity = iota
	Error
)

// Issue is one validator finding. Field is the dotted path of the
// missing/invalid field on the OperationContext; Code is a stable
// string used by tests + telemetry.
type Issue struct {
	Field    string
	Code     string
	Severity Severity
	Message  string
}

// Operation enumerates the ledger op types — must match
// drug_operations_log.operation CHECK.
type Operation string

const (
	OpAdminister Operation = "administer"
	OpDispense   Operation = "dispense"
	OpDiscard    Operation = "discard"
	OpReceive    Operation = "receive"
	OpTransfer   Operation = "transfer"
	OpAdjust     Operation = "adjust"
)

// OperationContext is the validator input. The drugs service builds
// this from LogOperationInput + the catalog/shelf/clinic lookups it
// already does.
//
// All compliance fields are optional pointers because legacy callers
// (Phase 1 shadow mode) don't populate them. Country validators check
// presence per their jurisdiction.
type OperationContext struct {
	Operation Operation

	// Schedule is the regulatory schedule string for the underlying
	// drug ('CD2','CII','S1','S8',...) as carried in the catalog. Used
	// to gate witness rules + container-count rules.
	Schedule string

	// IsControlled is the catalog Controls flag — country-aware
	// pre-computed truthy bit. When false, most country rules relax.
	IsControlled bool

	// ClinicCountry is the canonical 2-letter clinic country.
	ClinicCountry string

	// ClinicState is required for AU dispatch (NSW/VIC/QLD/WA/SA/TAS/ACT/NT).
	// Empty string outside AU.
	ClinicState string

	// Compliance bundles every optional jurisdiction-specific field.
	Compliance ComplianceInput
}

// ComplianceInput carries the optional jurisdiction-specific fields
// that flow through the API + into drug_operations_log. Pointer
// strings so "missing" and "empty" stay distinguishable.
type ComplianceInput struct {
	CounterpartyName       *string
	CounterpartyAddress    *string
	CounterpartyDEANumber  *string

	PrescriberName        *string
	PrescriberAddress     *string
	PrescriberDEANumber   *string
	DEARegistrationID     *string

	PatientAddress *string

	CollectorName              *string
	CollectorIDEvidenceRequested *bool
	CollectorIDEvidenceProvided  *bool

	PrescriptionRef  *string
	OrderFormSerial  *string

	CommercialContainerCount *int
	UnitsPerContainer        *float64

	BatchNumber *string
	ExpiryDate  *string // YYYY-MM-DD; date parsing happens in service

	WasteReason *string // populated for OpDiscard

	Witnessed bool // catalog says required; this records whether one was supplied
}

// Validator is implemented by each country's rule set.
type Validator interface {
	Country() string
	Validate(ctx OperationContext) []Issue
}

// Dispatch returns the validator for a clinic country. Falls back to
// noopValidator for unknown countries — we'd rather under-validate
// than crash, and any new country is added by writing a file in this
// package.
func Dispatch(country string) Validator {
	switch strings.ToUpper(country) {
	case "UK", "GB":
		return ukValidator{}
	case "US":
		return usValidator{}
	case "NZ":
		return nzValidator{}
	case "AU":
		return auValidator{}
	default:
		return noopValidator{country: country}
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func missing(s *string) bool {
	return s == nil || strings.TrimSpace(*s) == ""
}

func missingBool(b *bool) bool {
	return b == nil
}

func require(out *[]Issue, ok bool, field, code, msg string) {
	if !ok {
		*out = append(*out, Issue{
			Field:    field,
			Code:     code,
			Severity: Error,
			Message:  msg,
		})
	}
}

// ── noop fallback for unknown countries ──────────────────────────────

type noopValidator struct {
	country string
}

func (n noopValidator) Country() string { return n.country }

func (n noopValidator) Validate(_ OperationContext) []Issue {
	// Unknown country — no rules to apply. Service still enforces the
	// universal rules in validateOperation().
	return nil
}
