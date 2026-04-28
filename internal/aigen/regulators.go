package aigen

import "fmt"

// Regulator is the (country, vertical)-keyed metadata used in prompt rendering.
// Add a new combo by appending to the regulators map below — no code paths.
//
// IMPORTANT: do not encode regulator-specific *behavior* here. This struct is
// only data fed into prompts. Logic stays in the prompt template + AI; we are
// not building a rules engine.
type Regulator struct {
	Acronym         string   // e.g. VCNZ
	Name            string   // e.g. Veterinary Council of New Zealand
	Country         string   // ISO-3166-1 alpha-2
	Vertical        string   // vet | dental | general | aged_care | …
	PrivacyRegime   string   // single human-readable line for the prompt
	RetentionYears  int      // baseline; 0 = unspecified
	KeyDocuments    []string // human-readable references (no URLs)
	ComplianceAreas []string // bullet list rendered into the prompt
}

// regulators is the data table. Key format: "<COUNTRY>:<vertical>".
//
// Adding a new (country, vertical):
//  1. Append a row here.
//  2. Drop a fewshot pack at fewshot/policy/<vertical>_<country>.json.
//  3. No code changes anywhere else.
var regulators = map[string]Regulator{
	// ── Veterinary ────────────────────────────────────────────────────────
	"NZ:vet": {
		Acronym:        "VCNZ",
		Name:           "Veterinary Council of New Zealand",
		Country:        "NZ",
		Vertical:       "vet",
		PrivacyRegime:  "Privacy Act 2020 (NZ); Health Information Privacy Code",
		RetentionYears: 7,
		KeyDocuments: []string{
			"Code of Professional Conduct (VCNZ)",
			"NZVA Recommended Best Practice",
		},
		ComplianceAreas: []string{
			"Clear, accurate clinical records sufficient for handover to another vet",
			"Record retention for 7 years from last consultation",
			"Documented informed consent (esp. invasive procedures, euthanasia, telemedicine)",
			"Controlled drugs register — Schedule 1/2/3 (Misuse of Drugs Act 1975), monthly 2-person reconciliation, 4-year retention",
			"Continuity of care — discharge instructions and follow-up plan",
			"Veterinarian sign-off identity recorded on every note",
		},
	},
	"AU:vet": {
		Acronym:        "AVA / AVBC",
		Name:           "Australian Veterinary Association / Australasian Veterinary Boards Council",
		Country:        "AU",
		Vertical:       "vet",
		PrivacyRegime:  "Privacy Act 1988 (Commonwealth); state privacy and poisons acts",
		RetentionYears: 7,
		KeyDocuments: []string{
			"AVA Code of Professional Conduct",
			"AVBC Accreditation Standards (current)",
		},
		ComplianceAreas: []string{
			"Records property of veterinarian/practice; jurisdiction-specific retention (commonly 7 years)",
			"Detailed clinical records — sufficient for handover",
			"Controlled drug records per state poisons schedules",
			"Documented consent for procedures",
			"Continuity of care — follow-up and discharge instructions",
		},
	},
	"UK:vet": {
		Acronym:        "RCVS",
		Name:           "Royal College of Veterinary Surgeons",
		Country:        "UK",
		Vertical:       "vet",
		PrivacyRegime:  "UK GDPR; Data Protection Act 2018",
		RetentionYears: 6,
		KeyDocuments: []string{
			"RCVS Code of Professional Conduct for Veterinary Surgeons",
			"RCVS Practice Standards Manual",
			"RCVS AI Roundtable Report (evolving guidance)",
		},
		ComplianceAreas: []string{
			"Detailed clinical records — sufficient for handover",
			"Record retention minimum 6 years",
			"Documented informed consent for invasive procedures",
			"Controlled drugs records",
			"AI use: transparency, recording policy, human review and sign-off",
		},
	},
	"US:vet": {
		Acronym:        "AVMA / state boards",
		Name:           "American Veterinary Medical Association (state boards govern record-keeping)",
		Country:        "US",
		Vertical:       "vet",
		PrivacyRegime:  "State-specific (no federal HIPAA equivalent for vet); some states have privacy statutes",
		RetentionYears: 5,
		KeyDocuments: []string{
			"AVMA Principles of Veterinary Medical Ethics",
			"State board record-keeping rules (vary 3–7 years)",
		},
		ComplianceAreas: []string{
			"Records meet state-specific retention (3–7 years typical)",
			"Controlled drug records per DEA + state rules",
			"Documented consent",
			"Veterinarian sign-off identity",
		},
	},

	// ── Dental ────────────────────────────────────────────────────────────
	"NZ:dental": {
		Acronym:        "DCNZ",
		Name:           "Dental Council of New Zealand",
		Country:        "NZ",
		Vertical:       "dental",
		PrivacyRegime:  "Privacy Act 2020 (NZ); Health Information Privacy Code",
		RetentionYears: 10,
		KeyDocuments: []string{
			"DCNZ Standards Framework",
			"DCNZ Practice Standards",
		},
		ComplianceAreas: []string{
			"Patient records 10 years from last contact",
			"Tooth charting using accepted notation system",
			"Periodontal documentation (e.g. pocket depths)",
			"Documented consent (especially for invasive treatment)",
			"Treatment plan records",
		},
	},
	"AU:dental": {
		Acronym:        "DBA (AHPRA)",
		Name:           "Dental Board of Australia (AHPRA)",
		Country:        "AU",
		Vertical:       "dental",
		PrivacyRegime:  "Privacy Act 1988; state health records acts",
		RetentionYears: 7,
		KeyDocuments: []string{
			"DBA Code of Conduct",
			"DBA Guidelines on dental records",
		},
		ComplianceAreas: []string{
			"Records 7 years (adult), to age-25 (child)",
			"Tooth charting and treatment plan",
			"Periodontal records",
			"Consent documented",
			"Item codes per Australian Schedule of Dental Services",
		},
	},
	"UK:dental": {
		Acronym:        "GDC",
		Name:           "General Dental Council (UK)",
		Country:        "UK",
		Vertical:       "dental",
		PrivacyRegime:  "UK GDPR; Data Protection Act 2018",
		RetentionYears: 11,
		KeyDocuments: []string{
			"GDC Standards for the Dental Team",
			"FGDP Clinical Examination & Record-Keeping",
		},
		ComplianceAreas: []string{
			"Records 11 years (adult) / to age-25 (child)",
			"Robust documentation core to defense against GDC complaints",
			"Tooth charting with explicit notation system (FDI/Universal/Palmer)",
			"Periodontal scoring (BPE)",
			"Explicit, recorded consent",
		},
	},
	"US:dental": {
		Acronym:        "ADA / state boards",
		Name:           "American Dental Association (state boards govern record-keeping)",
		Country:        "US",
		Vertical:       "dental",
		PrivacyRegime:  "HIPAA; state retention rules",
		RetentionYears: 6,
		KeyDocuments: []string{
			"ADA Principles of Ethics and Code of Professional Conduct",
			"CDT (current year) procedure codes",
		},
		ComplianceAreas: []string{
			"HIPAA-compliant record-keeping (federal minimum 6 years)",
			"Tooth charting (Universal numbering common in US)",
			"Treatment planning with CDT codes",
			"Periodontal documentation",
			"Informed consent records",
		},
	},

	// ── General clinical / GP ────────────────────────────────────────────
	"NZ:general": {
		Acronym:        "MCNZ",
		Name:           "Medical Council of New Zealand",
		Country:        "NZ",
		Vertical:       "general",
		PrivacyRegime:  "Privacy Act 2020 (NZ); Health Information Privacy Code",
		RetentionYears: 10,
		KeyDocuments: []string{
			"MCNZ Statement on the maintenance and retention of patient records",
		},
		ComplianceAreas: []string{
			"Records 10 years from last consultation",
			"Continuity of care — discharge and follow-up",
			"Consent documentation",
			"Prescribing records (scheduled medicines)",
		},
	},
	"AU:general": {
		Acronym:        "AHPRA / Medical Board",
		Name:           "AHPRA — Medical Board of Australia",
		Country:        "AU",
		Vertical:       "general",
		PrivacyRegime:  "Privacy Act 1988; state health records acts",
		RetentionYears: 7,
		KeyDocuments: []string{
			"Medical Board Good medical practice",
			"RACGP Standards for general practice",
		},
		ComplianceAreas: []string{
			"Records 7 years (adult), to age-25 (child)",
			"Continuity of care",
			"Consent documentation",
			"Prescribing records (Schedule 4 / 8)",
		},
	},
	"UK:general": {
		Acronym:        "GMC / CQC",
		Name:           "General Medical Council / Care Quality Commission",
		Country:        "UK",
		Vertical:       "general",
		PrivacyRegime:  "UK GDPR; Data Protection Act 2018; Common Law Duty of Confidentiality",
		RetentionYears: 10,
		KeyDocuments: []string{
			"GMC Good medical practice",
			"NHS Records Management Code of Practice",
		},
		ComplianceAreas: []string{
			"GP records — 10 years after last entry (longer in some scenarios)",
			"Documented consent",
			"Prescribing records",
			"Continuity of care",
		},
	},
	"US:general": {
		Acronym:        "CMS / state boards",
		Name:           "Centers for Medicare & Medicaid Services / state medical boards",
		Country:        "US",
		Vertical:       "general",
		PrivacyRegime:  "HIPAA",
		RetentionYears: 6,
		KeyDocuments: []string{
			"HIPAA Privacy Rule",
			"State medical board record-keeping rules",
		},
		ComplianceAreas: []string{
			"HIPAA Privacy Rule — minimum 6 years",
			"State retention varies",
			"Consent + advance directives",
			"E&M documentation, prescribing, controlled substances (DEA)",
		},
	},

	// ── Aged care ────────────────────────────────────────────────────────
	"NZ:aged_care": {
		Acronym:        "HQSC",
		Name:           "Health Quality & Safety Commission NZ + Health & Disability Services Standards",
		Country:        "NZ",
		Vertical:       "aged_care",
		PrivacyRegime:  "Privacy Act 2020 (NZ); Health Information Privacy Code",
		RetentionYears: 10,
		KeyDocuments: []string{
			"NZS 8134 (Health & Disability Services Standards)",
		},
		ComplianceAreas: []string{
			"Care plans — review cadence, individualized to resident",
			"Incident reporting (falls, medication, restraint)",
			"Medication administration records (MAR)",
			"Resident rights, informed consent, advance care planning",
			"Behavior monitoring (esp. for residents with dementia)",
		},
	},
	"AU:aged_care": {
		Acronym:        "ACQSC",
		Name:           "Aged Care Quality and Safety Commission",
		Country:        "AU",
		Vertical:       "aged_care",
		PrivacyRegime:  "Privacy Act 1988; state health records acts",
		RetentionYears: 7,
		KeyDocuments: []string{
			"Strengthened Aged Care Quality Standards (2026)",
			"Aged Care Act 2024 resources",
		},
		ComplianceAreas: []string{
			"Care plans + reassessment cadence",
			"Incident management — Serious Incident Response Scheme (SIRS)",
			"Medication management + reconciliation",
			"Restraint use — documented authorization",
			"Resident agreement, behavior support plans",
			"Documentation supporting ACQSC site assessments",
		},
	},
	"UK:aged_care": {
		Acronym:        "CQC",
		Name:           "Care Quality Commission",
		Country:        "UK",
		Vertical:       "aged_care",
		PrivacyRegime:  "UK GDPR; Data Protection Act 2018",
		RetentionYears: 6,
		KeyDocuments: []string{
			"CQC Fundamental Standards",
			"CQC single assessment framework",
		},
		ComplianceAreas: []string{
			"Care plans aligned to fundamental standards (safe, effective, caring, responsive, well-led)",
			"Notifications to CQC for relevant incidents",
			"Medication administration records",
			"DoLS / mental capacity records",
			"Documentation supporting CQC inspections",
		},
	},
	"US:aged_care": {
		Acronym:        "CMS",
		Name:           "Centers for Medicare & Medicaid Services (Skilled Nursing Facilities)",
		Country:        "US",
		Vertical:       "aged_care",
		PrivacyRegime:  "HIPAA",
		RetentionYears: 5,
		KeyDocuments: []string{
			"CMS Conditions of Participation",
			"MDS 3.0 RAI Manual",
		},
		ComplianceAreas: []string{
			"MDS 3.0 assessments + care plans",
			"F-tag compliance (CMS survey deficiencies)",
			"Incident reporting + investigations",
			"Medication administration + reconciliation",
			"Resident rights, advance directives",
		},
	},
}

// LookupRegulator returns the regulator metadata for the given (country, vertical).
// If the combo is not yet configured, returns a generic best-practice stub so
// generation does not hard-fail. Callers SHOULD log unknown combos so the
// regulators table can be extended.
func LookupRegulator(country, vertical string) Regulator {
	if r, ok := regulators[country+":"+vertical]; ok {
		return r
	}
	return Regulator{
		Acronym:        fmt.Sprintf("%s/%s", country, vertical),
		Name:           "regulator metadata not yet configured — generic best-practice rules apply",
		Country:        country,
		Vertical:       vertical,
		PrivacyRegime:  "general clinical privacy best practice",
		RetentionYears: 7,
		ComplianceAreas: []string{
			"Detailed clinical records",
			"Documented consent",
			"Continuity of care",
			"Drug administration records where applicable",
		},
	}
}

// AllRegulatorKeys returns the explicitly-configured (country, vertical) keys.
// Useful for ops dashboards / startup health checks.
func AllRegulatorKeys() []string {
	keys := make([]string, 0, len(regulators))
	for k := range regulators {
		keys = append(keys, k)
	}
	return keys
}
