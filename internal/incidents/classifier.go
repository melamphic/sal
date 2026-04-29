package incidents

import (
	"strings"
	"time"
)

// Classification is the regulator-driven verdict on a logged incident:
// which scheme picks it up, with what priority, by when. Stamped on the
// row at insert time so audit reports can prove the deadlines ran from
// when the incident was logged, not retrofitted later.
//
// Vertical-agnostic by design: the classifier is one universal function
// parameterised by (vertical, country); per-jurisdiction tables live
// below. Aged care AU/UK ship full SIRS + CQC logic in v1; other combos
// fall through to a "just record it" verdict.
type Classification struct {
	// SIRSPriority is non-empty for AU aged care incidents reportable
	// under the Serious Incident Response Scheme.
	//   "priority_1" — 24h regulator notification window
	//   "priority_2" — 30d regulator notification window
	SIRSPriority string

	// CQCNotifiable flips on for UK aged care incidents the provider
	// must notify CQC about (death, abuse, serious injury, etc.).
	CQCNotifiable bool

	// CQCNotificationType — Regulation 16 / 17 / 18 / 20 categorisation
	// used by CQC's notification form.
	CQCNotificationType string

	// NotificationDeadline is the absolute regulator-notification cut-off
	// timestamp. Computed from occurredAt + the priority window. The
	// background pattern-alert job sweeps incidents past this point with
	// regulator_notified_at IS NULL and pages the privacy officer.
	NotificationDeadline *time.Time

	// Reason is a human-readable explanation for the classification —
	// surfaces in the UI ("Classified Priority 1 because…") and in the
	// SIRS/CQC PDF audit pack.
	Reason string
}

// ClassifyInput is everything the classifier reads off a new incident.
type ClassifyInput struct {
	Vertical       string // canonical clinic vertical: veterinary / dental / general_clinic / aged_care
	Country        string // NZ / AU / UK / US
	IncidentType   string // see CHECK constraint on incident_events.incident_type
	Severity       string // low / medium / high / critical
	SubjectOutcome string // no_harm / minor_injury / moderate_injury / hospitalised / deceased / complaint_resolved / unknown
	OccurredAt     time.Time
}

// Classify is the central regulator-decision function. Pure (no I/O), so
// it's easy to test and reason about. Returns a zero Classification (no
// scheme triggered, no deadline) for combos with no v1 logic.
func Classify(in ClassifyInput) Classification {
	v := normaliseVertical(in.Vertical)
	switch v + ":" + in.Country {
	case "aged_care:AU":
		return classifyACQSC(in)
	case "aged_care:UK":
		return classifyCQC(in)
	default:
		return Classification{
			Reason: "No regulator scheme applies to this (vertical, country) — incident is recorded for internal audit only.",
		}
	}
}

// ── ACQSC SIRS (AU aged care) ─────────────────────────────────────────────
//
// Reference: Aged Care Quality and Safety Commission Rules 2018,
// Serious Incident Response Scheme. Priority 1 incidents must be
// notified to the regulator within 24 hours of becoming aware. Priority
// 2 within 30 days.
//
// The decision tree below is a v1 conservative reading: any abuse /
// neglect / unexpected death gets Priority 1 immediately; everything
// else with a serious outcome gets Priority 1; otherwise Priority 2 if
// the incident type is reportable at all. Edge cases that should be
// reviewed by a privacy officer surface as Priority 1 — false positives
// are cheap; false negatives are catastrophic.

// sirsPriority1Types are auto-Priority-1 regardless of severity. Reading
// of SIRS Rules clauses 14A/B: physical/sexual/psychological abuse,
// neglect, financial abuse, unauthorised use of restraint, missing
// resident, unexpected death.
var sirsPriority1Types = map[string]bool{
	"physical_abuse":       true,
	"sexual_misconduct":    true,
	"psychological_abuse":  true,
	"financial_abuse":      true,
	"neglect":              true,
	"restraint":            true, // unauthorised restraint
	"unauthorised_absence": true,
	"unexplained_injury":   true,
}

// sirsReportableTypes are eligible for the scheme at all. Anything not
// in this set (and not in priority-1) records internally with no
// SIRSPriority + no deadline.
var sirsReportableTypes = map[string]bool{
	"fall":              true,
	"medication_error":  true,
	"behaviour":         true,
	"skin_injury":       true,
	"pressure_injury":   true,
	"complaint":         true,
	"death":             true,
	"other":             true,
}

func classifyACQSC(in ClassifyInput) Classification {
	if sirsPriority1Types[in.IncidentType] {
		deadline := in.OccurredAt.Add(24 * time.Hour)
		return Classification{
			SIRSPriority:         "priority_1",
			NotificationDeadline: &deadline,
			Reason: "SIRS Priority 1 — auto-classified by incident type (" + in.IncidentType +
				"). Notify ACQSC within 24 hours of awareness.",
		}
	}
	// Death is always Priority 1 if unexpected, otherwise Priority 2.
	if in.IncidentType == "death" {
		if in.SubjectOutcome == "deceased" && (in.Severity == "high" || in.Severity == "critical") {
			deadline := in.OccurredAt.Add(24 * time.Hour)
			return Classification{
				SIRSPriority:         "priority_1",
				NotificationDeadline: &deadline,
				Reason:               "SIRS Priority 1 — unexpected death. Notify ACQSC within 24 hours.",
			}
		}
	}
	// Hospitalisation or critical-severity outcome bumps to Priority 1.
	if in.SubjectOutcome == "hospitalised" || in.Severity == "critical" {
		deadline := in.OccurredAt.Add(24 * time.Hour)
		return Classification{
			SIRSPriority:         "priority_1",
			NotificationDeadline: &deadline,
			Reason: "SIRS Priority 1 — outcome (" + in.SubjectOutcome +
				") + severity (" + in.Severity + ") meets the serious-injury threshold.",
		}
	}
	// Reportable but not Priority 1 → Priority 2 (30 days).
	if sirsReportableTypes[in.IncidentType] {
		deadline := in.OccurredAt.Add(30 * 24 * time.Hour)
		return Classification{
			SIRSPriority:         "priority_2",
			NotificationDeadline: &deadline,
			Reason: "SIRS Priority 2 — reportable incident type. Notify ACQSC within 30 days.",
		}
	}
	return Classification{
		Reason: "Incident type does not meet ACQSC SIRS reporting threshold; recorded for internal audit only.",
	}
}

// ── CQC notifiable events (UK aged care) ────────────────────────────────────
//
// Reference: Care Quality Commission Regulations 2009 (as amended).
// The CQC requires notification of: deaths (Reg 16), serious injuries
// (Reg 18), abuse allegations (Reg 18), deprivation of liberty (Reg 17),
// police involvement (Reg 18), changes to provider or location info
// (Reg 14/15 — out of scope here).
//
// Default deadline: "without delay" (we use 24h as the operational
// cut-off so the deadline-sweep job has a number to fire on).

// cqcRegMap maps internal incident_type → CQC notification regulation
// citation. Used both to flag notifiable and to populate
// cqc_notification_type for the audit log.
var cqcRegMap = map[string]string{
	"death":                "Reg 16 — Notification of death of a service user",
	"unexplained_injury":   "Reg 18 — Notification of other incidents (serious injury)",
	"unauthorised_absence": "Reg 18 — Notification of other incidents (missing person)",
	"sexual_misconduct":    "Reg 18 — Notification of allegation of abuse",
	"physical_abuse":       "Reg 18 — Notification of allegation of abuse",
	"psychological_abuse":  "Reg 18 — Notification of allegation of abuse",
	"financial_abuse":      "Reg 18 — Notification of allegation of abuse",
	"neglect":              "Reg 18 — Notification of allegation of abuse",
	"restraint":            "Reg 17 — Notification of deprivation of liberty",
}

func classifyCQC(in ClassifyInput) Classification {
	regCitation, notifiable := cqcRegMap[in.IncidentType]
	// Hospitalisation OR critical severity OR death outcome triggers Reg 18
	// even if the incident type itself wasn't pre-mapped (e.g. a fall that
	// led to hospitalisation).
	if !notifiable && (in.SubjectOutcome == "hospitalised" || in.SubjectOutcome == "deceased" || in.Severity == "critical") {
		regCitation = "Reg 18 — Notification of other incidents (serious injury / consequence)"
		notifiable = true
	}
	if !notifiable {
		return Classification{
			Reason: "Incident type does not meet CQC notification threshold; recorded for internal audit only.",
		}
	}
	deadline := in.OccurredAt.Add(24 * time.Hour)
	return Classification{
		CQCNotifiable:        true,
		CQCNotificationType:  regCitation,
		NotificationDeadline: &deadline,
		Reason: "CQC notifiable — " + regCitation +
			". Notify the CQC without delay (operationally treated as 24 hours).",
	}
}

// normaliseVertical translates the canonical clinic strings used elsewhere
// (veterinary / general_clinic) into the short forms used throughout this
// package + the catalog (vet / general). Mirrors catalog.normalizeVertical.
func normaliseVertical(v string) string {
	switch strings.ToLower(v) {
	case "veterinary":
		return "vet"
	case "general_clinic":
		return "general"
	default:
		return strings.ToLower(v)
	}
}
