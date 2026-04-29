package reports

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/go-pdf/fpdf"
)

// ── Evidence-pack section registry ──────────────────────────────────────────
//
// One PDF builder, one regulator-section list per (vertical, country). This
// is the same pattern the controlled-drugs register uses (regulatorContexts)
// — adding a new combo means a one-line registry entry, never a new code
// path. Sprint B3 / C1 / C2 + the universal records-audit + incidents-log
// reports all funnel through here.

// Section is one renderable block inside an evidence pack. Each section is
// pure-function over the data + clinic snapshot — no I/O — so the PDF
// builder is deterministic given its inputs.
type Section string

const (
	SectionClinicOverview     Section = "clinic_overview"
	SectionRegulatorQuotes    Section = "regulator_quotes"
	SectionNotesSummary       Section = "notes_summary"
	SectionConsentSummary     Section = "consent_summary"
	SectionPainSummary        Section = "pain_summary"
	SectionDrugLedger         Section = "drug_ledger"
	SectionReconciliations    Section = "reconciliations"
	SectionIncidentsSummary   Section = "incidents_summary"
	SectionIncidentsLogTable  Section = "incidents_log_table"
	SectionSentinelEvents     Section = "sentinel_events" // high-severity incidents only
	SectionDisclosureLog      Section = "disclosure_log"  // HIPAA / similar
	SectionDEAInventory       Section = "dea_inventory"   // controlled drug snapshot
	SectionRegulatorSignoff   Section = "regulator_signoff"
)

// EvidencePackContext — what one (vertical, country) wants in its pack.
// Title + sections + signoff text. Title overrides the generic "Evidence
// Pack" cover so e.g. AU aged care reads "ACQSC Self-Assessment Evidence
// Pack" while UK aged care reads "CQC Single-Assessment Evidence Pack".
type EvidencePackContext struct {
	Title    string
	Sections []Section
}

// evidencePackContexts is the per-jurisdiction registry. All 16 combos are
// pinned so every clinic can request its native pack today. Section lists
// are tuned to what the regulator actually inspects:
//   - Veterinary  → drug ledger heavy + consent + incidents
//   - Dental      → records + consent + pain (procedure) + lighter drug
//   - General     → records + consent + prescribing + incidents
//   - Aged care   → everything (the most heavily inspected vertical)
//
// Adding a new combo: drop a row here. No code path changes.
var evidencePackContexts = map[string]EvidencePackContext{
	// ── Veterinary ────────────────────────────────────────────────────────

	"vet:NZ": {
		Title: "VCNZ Records & Compliance Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionReconciliations,
			SectionIncidentsSummary, SectionRegulatorSignoff,
		},
	},
	"vet:AU": {
		Title: "AVA Records & Compliance Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionReconciliations,
			SectionIncidentsSummary, SectionRegulatorSignoff,
		},
	},
	"vet:UK": {
		Title: "RCVS Practice Standards Evidence Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionReconciliations,
			SectionIncidentsSummary, SectionRegulatorSignoff,
		},
	},
	"vet:US": {
		Title: "AVMA / State Board Records Audit",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionReconciliations,
			SectionRegulatorSignoff,
		},
	},

	// ── Dental ────────────────────────────────────────────────────────────

	"dental:NZ": {
		Title: "DCNZ Records Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionPainSummary, SectionDrugLedger,
			SectionRegulatorSignoff,
		},
	},
	"dental:AU": {
		Title: "DBA / AHPRA Records Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionPainSummary, SectionDrugLedger,
			SectionRegulatorSignoff,
		},
	},
	"dental:UK": {
		Title: "GDC FGDP Records Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionPainSummary, SectionDrugLedger,
			SectionRegulatorSignoff,
		},
	},
	"dental:US": {
		Title: "ADA / HIPAA Records Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionPainSummary, SectionDrugLedger,
			SectionRegulatorSignoff,
		},
	},

	// ── General clinical (medical / GP) ───────────────────────────────────

	"general:NZ": {
		Title: "MCNZ Records & Prescribing Audit",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionIncidentsSummary,
			SectionRegulatorSignoff,
		},
	},
	"general:AU": {
		Title: "RACGP Standards Self-Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionIncidentsSummary,
			SectionRegulatorSignoff,
		},
	},
	"general:UK": {
		Title: "GMC / CQC GP Evidence Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionIncidentsSummary,
			SectionRegulatorSignoff,
		},
	},
	"general:US": {
		Title: "HIPAA / CMS Records Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary,
			SectionDrugLedger, SectionIncidentsSummary,
			SectionRegulatorSignoff,
		},
	},

	// ── Aged care ─────────────────────────────────────────────────────────

	"aged_care:NZ": {
		Title: "NZS 8134 Aged Care Self-Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary, SectionPainSummary,
			SectionIncidentsSummary, SectionDrugLedger, SectionReconciliations,
			SectionRegulatorSignoff,
		},
	},
	"aged_care:AU": {
		Title: "ACQSC Self-Assessment Evidence Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary, SectionPainSummary,
			SectionIncidentsSummary, SectionDrugLedger, SectionReconciliations,
			SectionRegulatorSignoff,
		},
	},
	"aged_care:UK": {
		Title: "CQC Single-Assessment Evidence Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary, SectionPainSummary,
			SectionIncidentsSummary, SectionDrugLedger, SectionReconciliations,
			SectionRegulatorSignoff,
		},
	},
	"aged_care:US": {
		Title: "CMS / MDS 3.0 Self-Audit Pack",
		Sections: []Section{
			SectionClinicOverview, SectionRegulatorQuotes,
			SectionNotesSummary, SectionConsentSummary, SectionPainSummary,
			SectionIncidentsSummary, SectionDrugLedger, SectionReconciliations,
			SectionRegulatorSignoff,
		},
	},
}

// LookupEvidencePack returns the section list for a (vertical, country).
// Falls back to a sensible universal pack when the combo isn't pinned.
func LookupEvidencePack(vertical, country string) EvidencePackContext {
	if ctx, ok := evidencePackContexts[vertical+":"+country]; ok {
		return ctx
	}
	regulator := LookupRegulatorContext(vertical, country)
	return EvidencePackContext{
		Title: regulator.RegulatorShort + " Evidence Pack",
		Sections: []Section{
			SectionClinicOverview,
			SectionRegulatorQuotes,
			SectionNotesSummary,
			SectionConsentSummary,
			SectionDrugLedger,
			SectionIncidentsSummary,
			SectionRegulatorSignoff,
		},
	}
}

// ── Builders ────────────────────────────────────────────────────────────────

// EvidencePackInput collects every dataset the section renderers can pull
// from. The worker fetches each dataset once and the renderers read them;
// keeps the IO surface narrow and makes the render path pure.
type EvidencePackInput struct {
	Clinic         *ClinicSnapshot
	PeriodStart    time.Time
	PeriodEnd      time.Time
	ReportID       string
	NoteCounts     map[string]int
	DrugOps        []DrugOpView
	Recons         []DrugReconciliationView
	Incidents      []IncidentView
	Consent        *ConsentSummary
	Pain           *PainSummary
	AccessLog      []SubjectAccessView // for HIPAA disclosure log
	ShelfSnapshot  []ShelfSnapshotView // for DEA biennial inventory
}

// BuildEvidencePackPDF is the unified per-jurisdiction evidence-pack
// builder. It looks up the section list from evidencePackContexts and
// renders each in order. New regulator packs only need a registry entry.
func BuildEvidencePackPDF(in EvidencePackInput) (*bytes.Buffer, string, error) {
	pack := LookupEvidencePack(in.Clinic.Vertical, in.Clinic.Country)
	regulator := LookupRegulatorContext(in.Clinic.Vertical, in.Clinic.Country)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdfCellFormat(pdf, 0, 5,
			fmt.Sprintf("%s — %s — Report %s — Page %d",
				in.Clinic.Name, regulator.RegulatorShort, shortID(in.ReportID), pdf.PageNo()),
			"", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	for i, section := range pack.Sections {
		if i > 0 && needsPageBreak(section) {
			pdf.AddPage()
		}
		renderSection(pdf, section, pack, regulator, in)
		pdf.Ln(4)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("reports.evidence.BuildEvidencePackPDF: %w", err)
	}
	return &buf, sha256Hex(buf.Bytes()), nil
}

// BuildRecordsAuditPDF — the universal "Records Audit" report. Same
// builder, generic section list, no per-jurisdiction registry lookup.
// Universal across all 16 combos.
func BuildRecordsAuditPDF(in EvidencePackInput) (*bytes.Buffer, string, error) {
	pack := EvidencePackContext{
		Title: "Records Audit",
		Sections: []Section{
			SectionClinicOverview,
			SectionNotesSummary,
			SectionConsentSummary,
			SectionPainSummary,
			SectionDrugLedger,
			SectionIncidentsSummary,
		},
	}
	regulator := LookupRegulatorContext(in.Clinic.Vertical, in.Clinic.Country)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdfCellFormat(pdf, 0, 5,
			fmt.Sprintf("%s — Records Audit — Report %s — Page %d",
				in.Clinic.Name, shortID(in.ReportID), pdf.PageNo()),
			"", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	for i, section := range pack.Sections {
		if i > 0 && needsPageBreak(section) {
			pdf.AddPage()
		}
		renderSection(pdf, section, pack, regulator, in)
		pdf.Ln(4)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("reports.evidence.BuildRecordsAuditPDF: %w", err)
	}
	return &buf, sha256Hex(buf.Bytes()), nil
}

// BuildHIPAADisclosureLogPDF — US-specific PHI disclosure log per HIPAA
// Privacy Rule §164.528. Lists every subject_access_log entry in period
// — who accessed what, when, why. Universal builder; the registry just
// picks the section list.
func BuildHIPAADisclosureLogPDF(in EvidencePackInput) (*bytes.Buffer, string, error) {
	pack := EvidencePackContext{
		Title: "HIPAA Disclosure Log",
		Sections: []Section{
			SectionClinicOverview,
			SectionRegulatorQuotes,
			SectionDisclosureLog,
		},
	}
	return buildWithSections(pack, in, "HIPAA")
}

// BuildDEABiennialInventoryPDF — US-specific DEA biennial inventory per
// 21 CFR 1304.11. Snapshot of every controlled drug on the shelf with
// current balance + schedule. Reads the shelf at report-generation time.
func BuildDEABiennialInventoryPDF(in EvidencePackInput) (*bytes.Buffer, string, error) {
	pack := EvidencePackContext{
		Title: "DEA Biennial Inventory",
		Sections: []Section{
			SectionClinicOverview,
			SectionRegulatorQuotes,
			SectionDEAInventory,
			SectionRegulatorSignoff,
		},
	}
	return buildWithSections(pack, in, "DEA")
}

// BuildSentinelEventsLogPDF — universal sentinel-events log. Same data
// as the incidents log but filters to high-severity / regulator-relevant
// events: SIRS Priority 1 (AU), CQC notifiable (UK), unexpected deaths,
// abuse / neglect / restraint, hospitalisation outcomes. Aged-care ships
// this as the standard weekly artefact.
func BuildSentinelEventsLogPDF(in EvidencePackInput) (*bytes.Buffer, string, error) {
	// Filter incidents to "sentinel" — happens here so the section
	// renderer stays simple.
	filtered := make([]IncidentView, 0, len(in.Incidents))
	for _, inc := range in.Incidents {
		if isSentinel(inc) {
			filtered = append(filtered, inc)
		}
	}
	in.Incidents = filtered

	pack := EvidencePackContext{
		Title: "Sentinel Events Log",
		Sections: []Section{
			SectionClinicOverview,
			SectionRegulatorQuotes,
			SectionSentinelEvents,
		},
	}
	return buildWithSections(pack, in, "Sentinel")
}

// isSentinel — a row counts as a sentinel event when:
//   - SIRS Priority 1, OR
//   - CQC notifiable, OR
//   - severity == "critical", OR
//   - subject_outcome involves hospitalisation / death.
//
// The bar is intentionally inclusive — false positives are cheap; missing
// a regulator-touching event is catastrophic.
func isSentinel(inc IncidentView) bool {
	if inc.SIRSPriority == "priority_1" {
		return true
	}
	if inc.CQCNotifiable {
		return true
	}
	if inc.Severity == "critical" {
		return true
	}
	return false
}

// buildWithSections is the shared rendering loop used by the new
// builders. Title goes in pack.Title; the wrapper renders cover, sections
// in order, and ships the bytes + sha256.
func buildWithSections(pack EvidencePackContext, in EvidencePackInput, footerLabel string) (*bytes.Buffer, string, error) {
	regulator := LookupRegulatorContext(in.Clinic.Vertical, in.Clinic.Country)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdfCellFormat(pdf, 0, 5,
			fmt.Sprintf("%s — %s — Report %s — Page %d",
				in.Clinic.Name, footerLabel, shortID(in.ReportID), pdf.PageNo()),
			"", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	for i, section := range pack.Sections {
		if i > 0 && needsPageBreak(section) {
			pdf.AddPage()
		}
		renderSection(pdf, section, pack, regulator, in)
		pdf.Ln(4)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("reports.evidence.buildWithSections: %w", err)
	}
	return &buf, sha256Hex(buf.Bytes()), nil
}

// BuildIncidentsLogPDF — universal incidents log. Renders the cover, the
// regulator quotes (so the reader sees the scheme that applies), and the
// full incidents table for the period.
func BuildIncidentsLogPDF(in EvidencePackInput) (*bytes.Buffer, string, error) {
	pack := EvidencePackContext{
		Title: "Incidents Log",
		Sections: []Section{
			SectionClinicOverview,
			SectionRegulatorQuotes,
			SectionIncidentsLogTable,
		},
	}
	regulator := LookupRegulatorContext(in.Clinic.Vertical, in.Clinic.Country)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdfCellFormat(pdf, 0, 5,
			fmt.Sprintf("%s — Incidents Log — Report %s — Page %d",
				in.Clinic.Name, shortID(in.ReportID), pdf.PageNo()),
			"", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	for i, section := range pack.Sections {
		if i > 0 && needsPageBreak(section) {
			pdf.AddPage()
		}
		renderSection(pdf, section, pack, regulator, in)
		pdf.Ln(4)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("reports.evidence.BuildIncidentsLogPDF: %w", err)
	}
	return &buf, sha256Hex(buf.Bytes()), nil
}

// ── Section dispatch + renderers ────────────────────────────────────────────

func needsPageBreak(s Section) bool {
	// Heavy table sections start on a fresh page so the table header isn't
	// orphaned at the bottom of the previous page.
	switch s {
	case SectionDrugLedger, SectionReconciliations, SectionIncidentsLogTable,
		SectionSentinelEvents, SectionDisclosureLog, SectionDEAInventory,
		SectionRegulatorSignoff:
		return true
	}
	return false
}

func renderSection(pdf *fpdf.Fpdf, s Section, pack EvidencePackContext, regulator RegulatorContext, in EvidencePackInput) {
	switch s {
	case SectionClinicOverview:
		drawComplianceCover(pdf, pack.Title, in.Clinic, in.PeriodStart, in.PeriodEnd, in.ReportID)
	case SectionRegulatorQuotes:
		drawRegulatorQuotes(pdf, regulator, in.Clinic.Vertical, in.Clinic.Country)
	case SectionNotesSummary:
		drawSectionTitle(pdf, "Records activity")
		drawNoteCountsTable(pdf, in.NoteCounts)
	case SectionConsentSummary:
		drawSectionTitle(pdf, "Consent activity")
		drawConsentSummary(pdf, in.Consent)
	case SectionPainSummary:
		drawSectionTitle(pdf, "Pain assessments")
		drawPainSummary(pdf, in.Pain)
	case SectionDrugLedger:
		drawSectionTitle(pdf, "Controlled drug ledger")
		if len(in.DrugOps) == 0 {
			pdf.SetFont("Helvetica", "I", 11)
			pdf.SetTextColor(120, 120, 120)
			pdfMultiCell(pdf, 0, 6, "No controlled-drug operations in the selected period.", "", "L", false)
		} else {
			drawDrugOpsTable(pdf, in.DrugOps, 50)
		}
	case SectionReconciliations:
		if len(in.Recons) == 0 {
			return
		}
		drawSectionTitle(pdf, "Reconciliations")
		drawReconciliationsTable(pdf, in.Recons)
	case SectionIncidentsSummary:
		drawSectionTitle(pdf, "Incidents — summary")
		drawIncidentsSummary(pdf, in.Incidents)
	case SectionIncidentsLogTable:
		drawSectionTitle(pdf, "Incidents — full log")
		drawIncidentsTable(pdf, in.Incidents)
	case SectionSentinelEvents:
		drawSectionTitle(pdf, "Sentinel events")
		drawIncidentsTable(pdf, in.Incidents)
	case SectionDisclosureLog:
		drawSectionTitle(pdf, "PHI disclosures in period")
		drawDisclosureLogTable(pdf, in.AccessLog)
	case SectionDEAInventory:
		drawSectionTitle(pdf, "Controlled drug inventory snapshot")
		drawShelfSnapshotTable(pdf, in.ShelfSnapshot)
	case SectionRegulatorSignoff:
		drawDeclaration(pdf, in.Clinic, in.PeriodStart, in.PeriodEnd, regulator)
	}
}

// ── Section renderers ───────────────────────────────────────────────────────

func drawRegulatorQuotes(pdf *fpdf.Fpdf, regulator RegulatorContext, vertical, country string) {
	drawSectionTitle(pdf, "Regulator framework")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	pdfMultiCell(pdf, 0, 5,
		fmt.Sprintf("Regulator: %s (%s).", regulator.RegulatorName, regulator.RegulatorShort),
		"", "L", false)
	pdfMultiCell(pdf, 0, 5,
		"Code reference: "+regulator.CodeReference,
		"", "L", false)
	pdf.Ln(2)

	// ComplianceAreas comes from aigen.LookupRegulator — but importing it
	// here pulls aigen into the reports tree; instead we keep a small
	// per-(vertical, country) inline list. Source of truth is regulator
	// context registry; this is the user-facing rephrase.
	pdf.SetFont("Helvetica", "B", 10)
	pdfMultiCell(pdf, 0, 5,
		fmt.Sprintf("Standards in scope (%s · %s):", vertical, country),
		"", "L", false)
	pdf.SetFont("Helvetica", "", 10)
	for _, area := range regulatorComplianceAreas(vertical, country) {
		pdfMultiCell(pdf, 0, 5, "  • "+area, "", "L", false)
	}
}

// regulatorComplianceAreas — short per-(vertical, country) bullet list of
// standards a clinic is being measured against. Values mirror the lists
// in aigen/regulators.go without the import dependency. Add a row here
// when extending evidencePackContexts.
func regulatorComplianceAreas(vertical, country string) []string {
	switch vertical + ":" + country {
	case "vet:NZ":
		return []string{
			"Clear, complete clinical records — handover-grade (VCNZ).",
			"Documented informed consent (esp. invasive procedures, euthanasia).",
			"Controlled drugs — Misuse of Drugs Act 1975, monthly 2-person reconciliation.",
			"Continuity of care — discharge instructions, follow-up plan.",
		}
	case "vet:AU":
		return []string{
			"Detailed clinical records — sufficient for handover (AVA).",
			"Documented consent for procedures.",
			"Controlled-drug records per state poisons schedule.",
		}
	case "vet:UK":
		return []string{
			"Detailed clinical records (RCVS Practice Standards).",
			"Documented informed consent for invasive procedures.",
			"Controlled drug records (Misuse of Drugs Regulations 2001).",
			"AI use: transparency, recording policy, human review.",
		}
	case "vet:US":
		return []string{
			"State-specific clinical records (typical 3–7 years).",
			"DEA controlled-substance records.",
			"Documented consent.",
		}
	case "dental:UK":
		return []string{
			"Records 11 years adult / to age 25 child (GDC + FGDP).",
			"Tooth charting using FDI / Universal / Palmer notation.",
			"Periodontal scoring (BPE).",
			"Explicit, recorded consent.",
		}
	case "dental:NZ":
		return []string{
			"Patient records 10 years from last contact (DCNZ).",
			"Tooth charting; periodontal documentation.",
			"Documented consent for invasive treatment.",
		}
	case "dental:AU":
		return []string{
			"Records 7 years adult / to age 25 child (DBA / AHPRA).",
			"Tooth charting + treatment plan; item codes per ASDS.",
			"Periodontal records.",
		}
	case "dental:US":
		return []string{
			"HIPAA-compliant records (federal min 6 years).",
			"Tooth charting (Universal); CDT codes for treatment plans.",
			"Periodontal documentation; informed consent.",
		}
	case "general:NZ":
		return []string{
			"Records 10 years from last consultation (MCNZ).",
			"Continuity of care, consent, prescribing records.",
		}
	case "general:AU":
		return []string{
			"Records 7 years adult / to age 25 child (RACGP / Medical Board).",
			"Continuity of care, consent, S4/S8 prescribing.",
		}
	case "general:UK":
		return []string{
			"GP records — 10 years post-last-entry (NHS Records Mgmt CoP).",
			"Documented consent, prescribing, continuity.",
		}
	case "general:US":
		return []string{
			"HIPAA Privacy Rule — minimum 6 years.",
			"Consent + advance directives, E&M, DEA controlled substances.",
		}
	case "aged_care:NZ":
		return []string{
			"Care plans + review cadence (NZS 8134).",
			"Incident reporting (falls, medication, restraint).",
			"Medication administration records (MAR).",
			"Resident rights, informed consent, advance care planning.",
		}
	case "aged_care:AU":
		return []string{
			"Strengthened Aged Care Quality Standards (2026).",
			"SIRS — Priority 1 within 24h, Priority 2 within 30d.",
			"Medication management + reconciliation.",
			"Restraint use — documented authorization.",
			"Resident agreement + behaviour-support plans.",
		}
	case "aged_care:UK":
		return []string{
			"Fundamental Standards (safe, effective, caring, responsive, well-led).",
			"CQC notifications for relevant incidents.",
			"Medication administration records.",
			"DoLS / Mental Capacity records.",
		}
	case "aged_care:US":
		return []string{
			"MDS 3.0 assessments + care plans (CMS).",
			"F-tag compliance (CMS survey deficiencies).",
			"Incident reporting + investigations.",
			"Resident rights, advance directives.",
		}
	}
	return []string{
		"Detailed clinical records.",
		"Documented consent.",
		"Continuity of care.",
		"Drug administration records where applicable.",
	}
}

func drawConsentSummary(pdf *fpdf.Fpdf, c *ConsentSummary) {
	if c == nil || c.Total == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No consent records captured in the selected period.", "", "L", false)
		return
	}
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	pdfMultiCell(pdf, 0, 5, fmt.Sprintf(
		"%d consent records captured. %d withdrawn. %d expiring within 30 days. %d verbal-clinic captures with witness signoff.",
		c.Total, c.Withdrawn, c.ExpiringIn30d, c.VerbalWitnessed,
	), "", "L", false)
	if len(c.ByType) == 0 {
		return
	}
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	pdfCellFormat(pdf, 80, 7, "Consent type", "B", 0, "L", true, 0, "")
	pdfCellFormat(pdf, 30, 7, "Count", "B", 0, "L", true, 0, "")
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	keys := sortedKeys(c.ByType)
	for _, k := range keys {
		pdfCellFormat(pdf, 80, 6, k, "B", 0, "L", false, 0, "")
		pdfCellFormat(pdf, 30, 6, fmt.Sprintf("%d", c.ByType[k]), "B", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}
}

func drawPainSummary(pdf *fpdf.Fpdf, p *PainSummary) {
	if p == nil || p.Count == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No pain assessments recorded in the selected period.", "", "L", false)
		return
	}
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	pdfMultiCell(pdf, 0, 5, fmt.Sprintf(
		"%d pain assessments. Mean score %.1f / 10. Peak score %d / 10.",
		p.Count, p.AvgScore, p.HighestScore,
	), "", "L", false)
	if len(p.ScalesUsed) == 0 {
		return
	}
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	pdfCellFormat(pdf, 60, 7, "Scale", "B", 0, "L", true, 0, "")
	pdfCellFormat(pdf, 30, 7, "Count", "B", 0, "L", true, 0, "")
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	for _, k := range sortedKeys(p.ScalesUsed) {
		pdfCellFormat(pdf, 60, 6, k, "B", 0, "L", false, 0, "")
		pdfCellFormat(pdf, 30, 6, fmt.Sprintf("%d", p.ScalesUsed[k]), "B", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}
}

func drawIncidentsSummary(pdf *fpdf.Fpdf, incs []IncidentView) {
	if len(incs) == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No incidents recorded in the selected period.", "", "L", false)
		return
	}
	byType := map[string]int{}
	bySeverity := map[string]int{}
	sirsP1 := 0
	sirsP2 := 0
	cqcCount := 0
	overdue := 0
	now := time.Now()
	for _, inc := range incs {
		byType[inc.IncidentType]++
		bySeverity[inc.Severity]++
		switch inc.SIRSPriority {
		case "priority_1":
			sirsP1++
		case "priority_2":
			sirsP2++
		}
		if inc.CQCNotifiable {
			cqcCount++
		}
		if inc.NotificationDeadline != nil &&
			inc.RegulatorNotifiedAt == nil &&
			inc.NotificationDeadline.Before(now) {
			overdue++
		}
	}
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	pdfMultiCell(pdf, 0, 5, fmt.Sprintf(
		"%d incidents. SIRS Priority 1: %d · Priority 2: %d. CQC notifiable: %d. Overdue regulator deadlines: %d.",
		len(incs), sirsP1, sirsP2, cqcCount, overdue,
	), "", "L", false)
	pdf.Ln(2)

	// Type table.
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	pdfCellFormat(pdf, 80, 7, "Type", "B", 0, "L", true, 0, "")
	pdfCellFormat(pdf, 30, 7, "Count", "B", 0, "L", true, 0, "")
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	for _, k := range sortedKeys(byType) {
		pdfCellFormat(pdf, 80, 6, k, "B", 0, "L", false, 0, "")
		pdfCellFormat(pdf, 30, 6, fmt.Sprintf("%d", byType[k]), "B", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}
}

func drawIncidentsTable(pdf *fpdf.Fpdf, incs []IncidentView) {
	if len(incs) == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No incidents recorded in the selected period.", "", "L", false)
		return
	}
	header := []string{"Date", "Type", "Severity", "Status", "SIRS", "CQC", "Subject"}
	widths := []float64{22, 36, 18, 28, 14, 14, 50}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	for i, h := range header {
		pdfCellFormat(pdf, widths[i], 7, h, "B", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(40, 40, 40)
	pdf.SetFillColor(252, 252, 252)
	for i, inc := range incs {
		fill := i%2 == 1
		sirs := "—"
		switch inc.SIRSPriority {
		case "priority_1":
			sirs = "P1"
		case "priority_2":
			sirs = "P2"
		}
		cqc := "—"
		if inc.CQCNotifiable {
			cqc = "Yes"
		}
		subject := "—"
		if inc.SubjectName != nil && *inc.SubjectName != "" {
			subject = *inc.SubjectName
		}
		row := []string{
			inc.OccurredAt.UTC().Format("02 Jan 06"),
			truncate(inc.IncidentType, 30),
			inc.Severity,
			truncate(inc.Status, 24),
			sirs,
			cqc,
			truncate(subject, 40),
		}
		for j, v := range row {
			pdfCellFormat(pdf, widths[j], 6, v, "B", 0, "L", fill, 0, "")
		}
		pdf.Ln(-1)
	}
}

// drawDisclosureLogTable renders the HIPAA PHI access log. Each row:
// when, action, subject (UUID-truncated), staff name, purpose. The
// regulator-facing column order matches the §164.528 disclosure-log
// requirement.
func drawDisclosureLogTable(pdf *fpdf.Fpdf, rows []SubjectAccessView) {
	if len(rows) == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No subject-access events recorded in the selected period.", "", "L", false)
		return
	}
	header := []string{"At", "Action", "Subject", "Staff", "Purpose"}
	widths := []float64{34, 32, 28, 38, 50}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	for i, h := range header {
		pdfCellFormat(pdf, widths[i], 7, h, "B", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(40, 40, 40)
	pdf.SetFillColor(252, 252, 252)
	for i, r := range rows {
		fill := i%2 == 1
		purpose := "—"
		if r.Purpose != nil && *r.Purpose != "" {
			purpose = *r.Purpose
		}
		row := []string{
			r.At.UTC().Format("02 Jan 06 15:04"),
			truncate(r.Action, 28),
			shortID(r.SubjectID),
			truncate(r.StaffName, 32),
			truncate(purpose, 44),
		}
		for j, v := range row {
			pdfCellFormat(pdf, widths[j], 6, v, "B", 0, "L", fill, 0, "")
		}
		pdf.Ln(-1)
	}
}

// drawShelfSnapshotTable renders the DEA biennial-inventory rows. One
// row per shelf entry with schedule, balance, location, batch, expiry.
func drawShelfSnapshotTable(pdf *fpdf.Fpdf, rows []ShelfSnapshotView) {
	if len(rows) == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No controlled-drug shelf entries to inventory.", "", "L", false)
		return
	}
	header := []string{"Drug", "Schedule", "Location", "Balance", "Batch", "Expires"}
	widths := []float64{60, 18, 32, 22, 24, 24}

	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	for i, h := range header {
		pdfCellFormat(pdf, widths[i], 7, h, "B", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(40, 40, 40)
	pdf.SetFillColor(252, 252, 252)
	for i, r := range rows {
		fill := i%2 == 1
		batch := "—"
		if r.BatchNumber != nil && *r.BatchNumber != "" {
			batch = *r.BatchNumber
		}
		exp := "—"
		if r.ExpiryDate != nil && *r.ExpiryDate != "" {
			exp = *r.ExpiryDate
		}
		row := []string{
			truncate(r.DrugLabel, 50),
			r.Schedule,
			truncate(r.Location, 28),
			fmtQty(r.Balance) + " " + r.Unit,
			truncate(batch, 20),
			exp,
		}
		for j, v := range row {
			pdfCellFormat(pdf, widths[j], 6, v, "B", 0, "L", fill, 0, "")
		}
		pdf.Ln(-1)
	}
}

// sortedKeys returns the keys of a string-keyed map in alphabetical order.
// Used so the rendered tables are deterministic across runs (same data →
// same PDF bytes → same sha256 hash).
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
