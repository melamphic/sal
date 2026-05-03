package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
)

// ── Adapter interfaces — narrow views over sister-domain services ─────────

// DrugOpView is the reports-local view of a drug operation row. The drugs
// service hands us its own response type; an adapter in app.go translates
// to this so reports doesn't depend on drugs internals.
type DrugOpView struct {
	ID             string
	ShelfID        string
	ShelfLabel     string // catalog/override drug name + strength
	Operation      string
	Quantity       float64
	Unit           string
	BalanceAfter   float64
	Dose           *string
	Route          *string
	Reason         *string
	Schedule       string // for the register PDF
	BatchNumber    *string
	Location       string
	SubjectID      *string
	SubjectName    *string
	AdministeredBy string // resolved staff name
	WitnessedBy    *string
	WitnessKind    *string // staff / pending / external / self — drives the witness cell
	WitnessStatus  *string // not_required / pending / approved / challenged
	CreatedAt      time.Time
}

// DrugReconciliationView — local view over a reconciliation row.
type DrugReconciliationView struct {
	ID                string
	ShelfLabel        string
	PeriodStart       time.Time
	PeriodEnd         time.Time
	PhysicalCount     float64
	LedgerCount       float64
	Discrepancy       float64
	Status            string
	PrimarySignedBy   string
	SecondarySignedBy *string
	Explanation       *string
}

// ClinicSnapshot — clinic info needed for the PDF header.
type ClinicSnapshot struct {
	Name      string
	LegalName string
	Vertical  string
	Country   string
	Address   *string
	Phone     *string
	Email     *string
	License   *string // regulator license number, if known
}

// IncidentView — local view over one incident row. Carries enough for
// summary tables (no full description body — the audit pack PDF cites
// counts + notifiable status, not paragraphs).
type IncidentView struct {
	ID                  string
	IncidentType        string
	Severity            string
	Status              string
	OccurredAt          time.Time
	SubjectName         *string
	SIRSPriority        string // priority_1 / priority_2 / "" — empty means non-SIRS
	CQCNotifiable       bool
	NotificationDeadline *time.Time
	RegulatorNotifiedAt *time.Time
}

// ConsentSummary — aggregate snapshot of consent activity in the period.
type ConsentSummary struct {
	Total           int
	ByType          map[string]int // consent_type -> count
	Withdrawn       int
	ExpiringIn30d   int
	VerbalWitnessed int // verbal_clinic captures with witness
}

// PainSummary — aggregate snapshot of pain assessments in the period.
type PainSummary struct {
	Count        int
	AvgScore     float64
	HighestScore int
	ScalesUsed   map[string]int // pain_scale_used -> count
}

// SubjectAccessView — local view of a subject_access_log row. Used by
// the HIPAA disclosure log report (US healthcare). Identifies the
// subject + actor + action without leaking encrypted PII.
type SubjectAccessView struct {
	SubjectID string
	StaffName string
	Action    string
	Purpose   *string
	At        time.Time
}

// ShelfSnapshotView — point-in-time snapshot of one shelf entry. Used
// by the DEA biennial-inventory report. Snapshot is the current balance
// at report-generation time; the regulator wants the count, not a
// historical reconstruction.
type ShelfSnapshotView struct {
	DrugLabel    string  // catalog name + strength
	Schedule     string  // CII / CIII / CIV / etc.
	Location     string
	BatchNumber  *string
	ExpiryDate   *string // YYYY-MM-DD
	Balance      float64
	Unit         string
	ParLevel     *float64
}

// ComplianceDataSource is the dependency the PDF builders need. Implementers
// (app.go adapters) wrap drugs.Service + clinic.Service + staff.Service and
// translate to reports-local view types.
type ComplianceDataSource interface {
	GetClinic(ctx context.Context, clinicID uuid.UUID) (*ClinicSnapshot, error)
	GetStaffName(ctx context.Context, clinicID, staffID uuid.UUID) (string, error)

	ListControlledDrugOps(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]DrugOpView, error)
	ListReconciliationsInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]DrugReconciliationView, error)
	CountNotesByStatus(ctx context.Context, clinicID uuid.UUID, from, to time.Time) (map[string]int, error)

	// Sources for the unified evidence-pack / records-audit / incidents-log
	// PDFs (B3 + C1 + C2 + universal Records Audit). Each adapter converts
	// the sister-domain response types into the reports-local view above.
	ListIncidentsInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]IncidentView, error)
	ConsentSummaryInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) (*ConsentSummary, error)
	PainSummaryInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) (*PainSummary, error)

	// Sources for Round-2 reports (hipaa_disclosure_log + dea_biennial_inventory).
	ListSubjectAccessInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]SubjectAccessView, error)
	ListControlledShelfSnapshot(ctx context.Context, clinicID uuid.UUID) ([]ShelfSnapshotView, error)
}

// ── Regulator context ────────────────────────────────────────────────────────
//
// The controlled-drugs register is a regulator-mandated artefact, but every
// regulator names + frames it slightly differently. This context lets one
// universal PDF builder produce the correct title, declaration text, and
// statutory wording for any (vertical, country) we ship — without code paths
// per country. New combos just register an entry in the lookup map.

// RegulatorContext supplies the human-facing labels + declaration text for
// the controlled-drugs register PDF, scoped to one (vertical, country).
type RegulatorContext struct {
	// RegisterTitle — appears as the cover-page title.
	// e.g. "VCNZ Controlled Drugs Register"
	//      "AU Schedule 8 Drugs Register"
	//      "UK Controlled Drugs Register (MDR 2001)"
	RegisterTitle string

	// RegulatorName — full regulator name used in the declaration body.
	// e.g. "Veterinary Council of New Zealand"
	RegulatorName string

	// RegulatorShort — short form for headers / footers.
	// e.g. "VCNZ"
	RegulatorShort string

	// CodeReference — citation used in the declaration footer.
	// e.g. "VCNZ Code of Professional Conduct"
	//      "UK Misuse of Drugs Regulations 2001"
	CodeReference string

	// SignatoryRole — who signs the declaration.
	// e.g. "authorised veterinarian"
	//      "responsible pharmacist"
	//      "registered nurse manager"
	SignatoryRole string

	// LicenseLabel — what the practitioner number is called.
	// e.g. "Registration #"
	//      "DEA Number"
	//      "GMC #"
	LicenseLabel string
}

// regulatorContexts is the (vertical, country) → RegulatorContext registry.
// Keys use the short catalog vertical (vet/dental/general/aged_care) — the
// service layer normalises before lookup, mirroring the catalog package.
var regulatorContexts = map[string]RegulatorContext{
	// Veterinary
	"vet:NZ": {
		RegisterTitle:  "VCNZ Controlled Drugs Register",
		RegulatorName:  "Veterinary Council of New Zealand",
		RegulatorShort: "VCNZ",
		CodeReference:  "VCNZ Code of Professional Conduct + Misuse of Drugs Act 1975",
		SignatoryRole:  "authorised veterinarian",
		LicenseLabel:   "VCNZ Registration #",
	},
	"vet:AU": {
		RegisterTitle:  "Schedule 8 Drugs Register (AU)",
		RegulatorName:  "Australian Veterinary Association / state Poisons Authority",
		RegulatorShort: "AVA",
		CodeReference:  "AU Poisons Standard + state Drugs, Poisons and Controlled Substances regulations",
		SignatoryRole:  "registered veterinarian",
		LicenseLabel:   "AVA / state board #",
	},
	"vet:UK": {
		RegisterTitle:  "Controlled Drugs Register (UK Vet)",
		RegulatorName:  "Royal College of Veterinary Surgeons + UK Home Office",
		RegulatorShort: "RCVS",
		CodeReference:  "UK Misuse of Drugs Regulations 2001 + RCVS Code of Conduct",
		SignatoryRole:  "registered veterinary surgeon",
		LicenseLabel:   "RCVS Registration #",
	},
	"vet:US": {
		RegisterTitle:  "DEA Controlled Substances Register (US Vet)",
		RegulatorName:  "US Drug Enforcement Administration",
		RegulatorShort: "DEA",
		CodeReference:  "21 CFR 1304 (DEA recordkeeping)",
		SignatoryRole:  "DEA-registered veterinarian",
		LicenseLabel:   "DEA #",
	},

	// Dental
	"dental:NZ": {
		RegisterTitle:  "Dental Council NZ Controlled Drugs Register",
		RegulatorName:  "Dental Council of New Zealand",
		RegulatorShort: "DCNZ",
		CodeReference:  "DCNZ Standards + Misuse of Drugs Act 1975",
		SignatoryRole:  "registered dentist",
		LicenseLabel:   "DCNZ Registration #",
	},
	"dental:AU": {
		RegisterTitle:  "Schedule 8 Drugs Register (AU Dental)",
		RegulatorName:  "Dental Board of Australia (AHPRA)",
		RegulatorShort: "DBA",
		CodeReference:  "AU Poisons Standard + state regulations",
		SignatoryRole:  "registered dentist",
		LicenseLabel:   "AHPRA #",
	},
	"dental:UK": {
		RegisterTitle:  "Controlled Drugs Register (UK Dental)",
		RegulatorName:  "General Dental Council + UK Home Office",
		RegulatorShort: "GDC",
		CodeReference:  "UK Misuse of Drugs Regulations 2001 + GDC Standards",
		SignatoryRole:  "registered dentist",
		LicenseLabel:   "GDC #",
	},
	"dental:US": {
		RegisterTitle:  "DEA Controlled Substances Register (US Dental)",
		RegulatorName:  "US Drug Enforcement Administration",
		RegulatorShort: "DEA",
		CodeReference:  "21 CFR 1304",
		SignatoryRole:  "DEA-registered dentist",
		LicenseLabel:   "DEA #",
	},

	// General clinical (medical / GP)
	"general:NZ": {
		RegisterTitle:  "MCNZ Controlled Drugs Register",
		RegulatorName:  "Medical Council of New Zealand",
		RegulatorShort: "MCNZ",
		CodeReference:  "MCNZ Good Medical Practice + Misuse of Drugs Act 1975",
		SignatoryRole:  "registered medical practitioner",
		LicenseLabel:   "MCNZ Registration #",
	},
	"general:AU": {
		RegisterTitle:  "Schedule 8 Drugs Register (AU Medical)",
		RegulatorName:  "Medical Board of Australia (AHPRA)",
		RegulatorShort: "MBA",
		CodeReference:  "AU Poisons Standard + state regulations",
		SignatoryRole:  "registered medical practitioner",
		LicenseLabel:   "AHPRA #",
	},
	"general:UK": {
		RegisterTitle:  "Controlled Drugs Register (UK Medical)",
		RegulatorName:  "General Medical Council + UK Home Office",
		RegulatorShort: "GMC",
		CodeReference:  "UK Misuse of Drugs Regulations 2001",
		SignatoryRole:  "registered medical practitioner",
		LicenseLabel:   "GMC #",
	},
	"general:US": {
		RegisterTitle:  "DEA Controlled Substances Register (US Medical)",
		RegulatorName:  "US Drug Enforcement Administration",
		RegulatorShort: "DEA",
		CodeReference:  "21 CFR 1304",
		SignatoryRole:  "DEA-registered prescriber",
		LicenseLabel:   "DEA #",
	},

	// Aged care
	"aged_care:NZ": {
		RegisterTitle:  "Aged Care Controlled Drugs Register (NZ)",
		RegulatorName:  "Health and Disability Services Standards (NZS 8134)",
		RegulatorShort: "HealthCertNZ",
		CodeReference:  "NZS 8134 Aged Residential Care + Misuse of Drugs Act 1975",
		SignatoryRole:  "registered nurse manager",
		LicenseLabel:   "Nursing Council #",
	},
	"aged_care:AU": {
		RegisterTitle:  "ACQSC Schedule 8 Register (AU Aged Care)",
		RegulatorName:  "Aged Care Quality and Safety Commission",
		RegulatorShort: "ACQSC",
		CodeReference:  "AU Aged Care Quality Standards + state Drugs and Poisons regulations",
		SignatoryRole:  "registered nurse / facility manager",
		LicenseLabel:   "AHPRA #",
	},
	"aged_care:UK": {
		RegisterTitle:  "CQC Controlled Drugs Register (UK Aged Care)",
		RegulatorName:  "Care Quality Commission + UK Home Office",
		RegulatorShort: "CQC",
		CodeReference:  "UK Misuse of Drugs Regulations 2001 + CQC fundamental standards",
		SignatoryRole:  "registered manager",
		LicenseLabel:   "CQC Provider ID",
	},
	"aged_care:US": {
		RegisterTitle:  "Long-Term Care Controlled Substances Register (US)",
		RegulatorName:  "US Drug Enforcement Administration + CMS",
		RegulatorShort: "DEA/CMS",
		CodeReference:  "21 CFR 1304 + 42 CFR 483 (long-term care facilities)",
		SignatoryRole:  "Director of Nursing",
		LicenseLabel:   "DEA / facility ID",
	},
}

// LookupRegulatorContext returns the regulator labels for a (vertical, country)
// combo. Falls back to a generic context when the pair isn't registered yet.
// Vertical normalisation is the caller's responsibility — pass in catalog
// short forms (vet / general / dental / aged_care).
func LookupRegulatorContext(vertical, country string) RegulatorContext {
	if ctx, ok := regulatorContexts[vertical+":"+country]; ok {
		return ctx
	}
	return RegulatorContext{
		RegisterTitle:  fmt.Sprintf("Controlled Drugs Register (%s · %s)", vertical, country),
		RegulatorName:  "Local regulator",
		RegulatorShort: "Regulator",
		CodeReference:  "Local controlled-drugs regulations",
		SignatoryRole:  "authorised practitioner",
		LicenseLabel:   "Practitioner #",
	}
}

// ── Builders ──────────────────────────────────────────────────────────────

// BuildControlledDrugsRegisterPDF renders a controlled-drugs register PDF
// for any (vertical, country). The regulator-specific title, statutory
// declaration text, and signatory role come from the regulatorContexts
// registry — adding a new combo is a one-line registry entry, not a code
// path.
//
// Returns the PDF bytes + sha256 hash (hex). Hash is embedded in the report
// row's report_hash column for tamper detection.
func BuildControlledDrugsRegisterPDF(
	clinic *ClinicSnapshot,
	periodStart, periodEnd time.Time,
	ops []DrugOpView,
	recons []DrugReconciliationView,
	reportID string,
) (*bytes.Buffer, string, error) {
	regCtx := LookupRegulatorContext(clinic.Vertical, clinic.Country)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)

	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdfCellFormat(pdf, 0, 5,
			fmt.Sprintf("%s — %s — Report %s — Page %d",
				clinic.Name, regCtx.RegulatorShort, shortID(reportID), pdf.PageNo()),
			"", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	drawComplianceCover(pdf, regCtx.RegisterTitle, clinic, periodStart, periodEnd, reportID)

	// Group operations by shelf so each drug gets its own register section.
	type shelfGroup struct {
		label    string
		schedule string
		ops      []DrugOpView
	}
	groupsByShelf := map[string]*shelfGroup{}
	order := []string{}
	for _, op := range ops {
		g, ok := groupsByShelf[op.ShelfID]
		if !ok {
			g = &shelfGroup{label: op.ShelfLabel, schedule: op.Schedule}
			groupsByShelf[op.ShelfID] = g
			order = append(order, op.ShelfID)
		}
		g.ops = append(g.ops, op)
	}

	if len(order) == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6,
			"No controlled-drug operations in the selected period.",
			"", "L", false)
	}
	for _, shelfID := range order {
		g := groupsByShelf[shelfID]
		drawShelfSection(pdf, g.label, g.schedule, g.ops)
	}

	if len(recons) > 0 {
		pdf.AddPage()
		drawSectionTitle(pdf, "Reconciliations in period")
		drawReconciliationsTable(pdf, recons)
	}

	pendingOps := filterPendingWitness(ops)
	challengedOps := filterChallengedWitness(ops)
	selfOps := filterSelfWitness(ops)
	if len(pendingOps) > 0 || len(challengedOps) > 0 || len(selfOps) > 0 {
		pdf.AddPage()
		drawWitnessAppendix(pdf, pendingOps, challengedOps, selfOps)
	}

	pdf.AddPage()
	drawDeclaration(pdf, clinic, periodStart, periodEnd, regCtx)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("reports.pdf.BuildControlledDrugsRegisterPDF: %w", err)
	}
	return &buf, sha256Hex(buf.Bytes()), nil
}

// filterPendingWitness returns the ops that are still waiting on a
// second-pair-of-eyes signature. Drives the appendix at the end of the
// register PDF so a regulator scans straight to the open queue.
func filterPendingWitness(ops []DrugOpView) []DrugOpView {
	var out []DrugOpView
	for _, op := range ops {
		if op.WitnessStatus != nil && *op.WitnessStatus == "pending" {
			out = append(out, op)
		}
	}
	return out
}

// filterChallengedWitness returns ops whose witness review came back
// challenged. These need follow-up and the regulator should see them
// listed alongside the pending queue.
func filterChallengedWitness(ops []DrugOpView) []DrugOpView {
	var out []DrugOpView
	for _, op := range ops {
		if op.WitnessStatus != nil && *op.WitnessStatus == "challenged" {
			out = append(out, op)
		}
	}
	return out
}

// filterSelfWitness returns ops that were self-witnessed. UK Schedule 2
// and AU S8 both restrict this to emergency cases — the appendix calls
// them out so a regulator can review the attestations.
func filterSelfWitness(ops []DrugOpView) []DrugOpView {
	var out []DrugOpView
	for _, op := range ops {
		if op.WitnessKind != nil && *op.WitnessKind == "self" {
			out = append(out, op)
		}
	}
	return out
}

// drawWitnessAppendix prints a regulator-facing appendix listing
// witness-pending, challenged, and self-witnessed rows. Each section
// only renders when non-empty so the appendix collapses for a clean
// register and grows visibly when something needs follow-up.
func drawWitnessAppendix(pdf *fpdf.Fpdf, pending, challenged, self []DrugOpView) {
	drawSectionTitle(pdf, "Witness exceptions (period)")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(80, 80, 80)
	pdfMultiCell(pdf, 0, 5,
		"Rows on this page require regulator-aware follow-up: pending "+
			"second-pair-of-eyes sign-off, challenged by a colleague, or "+
			"self-witnessed under emergency conditions.",
		"", "L", false)
	pdf.Ln(2)

	if len(pending) > 0 {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.SetTextColor(160, 100, 0)
		pdfMultiCell(pdf, 0, 6,
			fmt.Sprintf("Pending sign-off (%d)", len(pending)),
			"", "L", false)
		drawDrugOpsTable(pdf, pending, 0)
		pdf.Ln(2)
	}
	if len(challenged) > 0 {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.SetTextColor(160, 30, 30)
		pdfMultiCell(pdf, 0, 6,
			fmt.Sprintf("Challenged (%d)", len(challenged)),
			"", "L", false)
		drawDrugOpsTable(pdf, challenged, 0)
		pdf.Ln(2)
	}
	if len(self) > 0 {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.SetTextColor(160, 30, 30)
		pdfMultiCell(pdf, 0, 6,
			fmt.Sprintf("Self-witnessed (%d)", len(self)),
			"", "L", false)
		drawDrugOpsTable(pdf, self, 0)
	}
}

// BuildAuditPackPDF renders a comprehensive audit pack (single PDF for v1)
// that's vertical- and country-agnostic. Sections: cover, period summary
// (notes by status), drug ledger highlights, reconciliations.
func BuildAuditPackPDF(
	clinic *ClinicSnapshot,
	periodStart, periodEnd time.Time,
	ops []DrugOpView,
	recons []DrugReconciliationView,
	noteCounts map[string]int,
	reportID string,
) (*bytes.Buffer, string, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)

	pdf.SetFooterFunc(func() {
		pdf.SetY(-15)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdfCellFormat(pdf, 0, 5,
			fmt.Sprintf("%s — Audit Pack — Report %s — Page %d",
				clinic.Name, shortID(reportID), pdf.PageNo()),
			"", 0, "C", false, 0, "")
	})

	pdf.AddPage()
	drawComplianceCover(pdf, "Compliance Audit Pack", clinic, periodStart, periodEnd, reportID)

	drawSectionTitle(pdf, "Records activity")
	drawNoteCountsTable(pdf, noteCounts)

	pdf.AddPage()
	drawSectionTitle(pdf, "Controlled drug ledger — highlights")
	if len(ops) == 0 {
		pdf.SetFont("Helvetica", "I", 11)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No controlled-drug operations in the selected period.", "", "L", false)
	} else {
		drawDrugOpsTable(pdf, ops, 50)
	}

	if len(recons) > 0 {
		pdf.AddPage()
		drawSectionTitle(pdf, "Reconciliations")
		drawReconciliationsTable(pdf, recons)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("reports.pdf.BuildAuditPackPDF: %w", err)
	}
	return &buf, sha256Hex(buf.Bytes()), nil
}

// ── Shared chrome helpers ─────────────────────────────────────────────────

func drawComplianceCover(pdf *fpdf.Fpdf, title string, clinic *ClinicSnapshot, periodStart, periodEnd time.Time, reportID string) {
	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(20, 20, 20)
	pdfMultiCell(pdf, 0, 10, title, "", "L", false)
	pdf.Ln(2)

	pdf.SetFont("Helvetica", "", 11)
	pdf.SetTextColor(80, 80, 80)
	pdfMultiCell(pdf, 0, 6,
		fmt.Sprintf("Period: %s — %s",
			periodStart.UTC().Format("2 Jan 2006"),
			periodEnd.UTC().Format("2 Jan 2006")),
		"", "L", false)
	pdfMultiCell(pdf, 0, 6,
		fmt.Sprintf("Generated: %s UTC", time.Now().UTC().Format("2 Jan 2006 15:04")),
		"", "L", false)
	pdfMultiCell(pdf, 0, 6, fmt.Sprintf("Report ID: %s", reportID), "", "L", false)
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(20, 20, 20)
	pdfMultiCell(pdf, 0, 6, clinic.Name, "", "L", false)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(80, 80, 80)
	if clinic.LegalName != "" && clinic.LegalName != clinic.Name {
		pdfMultiCell(pdf, 0, 5, clinic.LegalName, "", "L", false)
	}
	if clinic.Address != nil {
		pdfMultiCell(pdf, 0, 5, *clinic.Address, "", "L", false)
	}
	contact := []string{}
	if clinic.Phone != nil {
		contact = append(contact, *clinic.Phone)
	}
	if clinic.Email != nil {
		contact = append(contact, *clinic.Email)
	}
	if len(contact) > 0 {
		pdfMultiCell(pdf, 0, 5, strings.Join(contact, "  ·  "), "", "L", false)
	}
	if clinic.License != nil {
		pdfMultiCell(pdf, 0, 5, "License: "+*clinic.License, "", "L", false)
	}
	pdfMultiCell(pdf, 0, 5,
		fmt.Sprintf("Vertical: %s · Country: %s", clinic.Vertical, clinic.Country),
		"", "L", false)
	pdf.Ln(6)

	pdf.SetDrawColor(220, 220, 220)
	x, y := pdf.GetX(), pdf.GetY()
	pdf.Line(x, y, x+180, y)
	pdf.Ln(6)
}

func drawSectionTitle(pdf *fpdf.Fpdf, title string) {
	pdf.SetFont("Helvetica", "B", 14)
	pdf.SetTextColor(20, 20, 20)
	pdfMultiCell(pdf, 0, 8, title, "", "L", false)
	pdf.Ln(2)
}

func drawShelfSection(pdf *fpdf.Fpdf, label, schedule string, ops []DrugOpView) {
	_, h := pdf.GetPageSize()
	if pdf.GetY() > h-60 {
		pdf.AddPage()
	}

	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetTextColor(20, 20, 20)
	pdfMultiCell(pdf, 0, 7, label, "", "L", false)
	if schedule != "" {
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(160, 30, 30)
		pdfMultiCell(pdf, 0, 5, "Schedule: "+schedule, "", "L", false)
	}
	pdf.Ln(2)

	drawDrugOpsTable(pdf, ops, 0)
	pdf.Ln(4)
}

func drawDrugOpsTable(pdf *fpdf.Fpdf, ops []DrugOpView, maxRows int) {
	header := []string{"Date", "Op", "Qty", "Balance", "Patient", "By", "Witness", "Status", "Batch"}
	widths := []float64{22, 16, 14, 17, 28, 22, 24, 22, 14}

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
	count := 0
	for _, op := range ops {
		if maxRows > 0 && count >= maxRows {
			pdf.SetFont("Helvetica", "I", 8)
			pdf.SetTextColor(120, 120, 120)
			pdfCellFormat(pdf, 0, 6, fmt.Sprintf("…and %d more rows. Full ledger in CSV export.", len(ops)-count),
				"", 0, "L", false, 0, "")
			pdf.Ln(-1)
			break
		}
		patient := "—"
		if op.SubjectName != nil && *op.SubjectName != "" {
			patient = *op.SubjectName
		}
		witness := witnessCellLabel(op)
		statusCell := witnessStatusLabel(op)
		batch := "—"
		if op.BatchNumber != nil && *op.BatchNumber != "" {
			batch = *op.BatchNumber
		}
		fill := count%2 == 1
		row := []string{
			op.CreatedAt.UTC().Format("02 Jan 06"),
			op.Operation,
			fmtQty(op.Quantity) + " " + op.Unit,
			fmtQty(op.BalanceAfter) + " " + op.Unit,
			truncate(patient, 18),
			truncate(op.AdministeredBy, 14),
			truncate(witness, 16),
			truncate(statusCell, 14),
			truncate(batch, 10),
		}
		for i, v := range row {
			pdfCellFormat(pdf, widths[i], 6, v, "B", 0, "L", fill, 0, "")
		}
		pdf.Ln(-1)
		count++
	}
}

// witnessCellLabel formats the Witness column for a drug op row. A staff
// witness shows the staff name; external shows the captured name; self
// shows "Self" (regulator-flagged); pending shows "—" and is paired with
// "Pending" in the status column.
func witnessCellLabel(op DrugOpView) string {
	kind := ""
	if op.WitnessKind != nil {
		kind = *op.WitnessKind
	}
	switch kind {
	case "external":
		if op.WitnessedBy != nil && *op.WitnessedBy != "" {
			return *op.WitnessedBy
		}
		return "External"
	case "self":
		return "Self (no second witness)"
	case "pending":
		return "—"
	default:
		if op.WitnessedBy != nil && *op.WitnessedBy != "" {
			return *op.WitnessedBy
		}
		return "—"
	}
}

// witnessStatusLabel translates the snapshot column into the regulator-
// facing label rendered in the Status cell.
func witnessStatusLabel(op DrugOpView) string {
	status := ""
	if op.WitnessStatus != nil {
		status = *op.WitnessStatus
	}
	switch status {
	case "pending":
		return "Pending sign-off"
	case "approved":
		return "Witnessed"
	case "challenged":
		return "Challenged"
	case "not_required":
		return "—"
	default:
		return "—"
	}
}

func drawReconciliationsTable(pdf *fpdf.Fpdf, recs []DrugReconciliationView) {
	header := []string{"Period", "Drug", "Physical", "Ledger", "Diff", "Status", "Primary", "Secondary"}
	widths := []float64{32, 30, 18, 18, 14, 26, 24, 18}

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
	for i, r := range recs {
		fill := i%2 == 1
		secondary := "(unsigned)"
		if r.SecondarySignedBy != nil && *r.SecondarySignedBy != "" {
			secondary = *r.SecondarySignedBy
		}
		row := []string{
			r.PeriodStart.UTC().Format("02 Jan") + "–" + r.PeriodEnd.UTC().Format("02 Jan 06"),
			truncate(r.ShelfLabel, 22),
			fmtQty(r.PhysicalCount),
			fmtQty(r.LedgerCount),
			fmtQty(r.Discrepancy),
			r.Status,
			truncate(r.PrimarySignedBy, 16),
			truncate(secondary, 12),
		}
		for j, v := range row {
			pdfCellFormat(pdf, widths[j], 6, v, "B", 0, "L", fill, 0, "")
		}
		pdf.Ln(-1)
	}
}

func drawNoteCountsTable(pdf *fpdf.Fpdf, counts map[string]int) {
	if len(counts) == 0 {
		pdf.SetFont("Helvetica", "I", 10)
		pdf.SetTextColor(120, 120, 120)
		pdfMultiCell(pdf, 0, 6, "No clinical notes recorded in this period.", "", "L", false)
		return
	}
	statuses := []string{"draft", "submitted", "extracting", "failed"}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(245, 245, 245)
	pdf.SetTextColor(60, 60, 60)
	pdfCellFormat(pdf, 60, 7, "Status", "B", 0, "L", true, 0, "")
	pdfCellFormat(pdf, 40, 7, "Count", "B", 0, "L", true, 0, "")
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	for _, s := range statuses {
		c := counts[s]
		pdfCellFormat(pdf, 60, 6, s, "B", 0, "L", false, 0, "")
		pdfCellFormat(pdf, 40, 6, fmt.Sprintf("%d", c), "B", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}
}

// drawDeclaration adapts the statutory declaration to the regulator context.
// One template; the role + regulator name + code reference + license label
// come from the registry, so a new (vertical, country) pair gets the right
// wording for free.
func drawDeclaration(pdf *fpdf.Fpdf, clinic *ClinicSnapshot, periodStart, periodEnd time.Time, regCtx RegulatorContext) {
	drawSectionTitle(pdf, "Statutory declaration")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(40, 40, 40)
	pdfMultiCell(pdf, 0, 5, fmt.Sprintf(
		"I, the undersigned %s of %s, declare that the above register is a "+
			"true and complete record of all controlled-drug transactions for "+
			"the period %s to %s, and that all entries have been verified "+
			"against the clinic's physical stock under %s.",
		regCtx.SignatoryRole,
		clinic.Name,
		periodStart.UTC().Format("2 Jan 2006"),
		periodEnd.UTC().Format("2 Jan 2006"),
		regCtx.CodeReference,
	), "", "L", false)
	pdf.Ln(12)
	pdf.SetFont("Helvetica", "", 10)
	pdfMultiCell(pdf, 0, 6, "Signed: ____________________________      Date: ___________", "", "L", false)
	pdf.Ln(6)
	pdfMultiCell(pdf, 0, 6, "Name (printed): ____________________________", "", "L", false)
	pdfMultiCell(pdf, 0, 6, regCtx.LicenseLabel+": ____________________________", "", "L", false)
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "I", 9)
	pdf.SetTextColor(120, 120, 120)
	pdfMultiCell(pdf, 0, 5,
		fmt.Sprintf("Filed under %s — %s.", regCtx.RegulatorName, regCtx.CodeReference),
		"", "L", false)
}

// ── Encoding ──────────────────────────────────────────────────────────────
//
// fpdf's built-in fonts (Helvetica / Times / Courier) are encoded in
// Windows-1252. Passing UTF-8 multi-byte runes (em-dash, middle-dot,
// ellipsis, en-dash, NBSP) produces mojibake like "â€"" or "Â·". The
// shipping-perfect fix is registering a TTF with full Unicode coverage —
// out of scope for v1. As a robust fallback we sanitise every string that
// hits the PDF: known typographic runes get ASCII equivalents, anything
// else outside CP1252 falls back to '?'. Numbers, dates, names, and
// regulator wording all stay perfectly legible.

var asciiReplacements = map[rune]string{
	'—':  "-",  // em dash
	'–':  "-",  // en dash
	'−':  "-",  // minus sign
	'·':  ".",  // middle dot
	'•':  "*",  // bullet
	'…':  "...",
	'“':  "\"",
	'”':  "\"",
	'„':  "\"",
	'‘':  "'",
	'’':  "'",
	' ': " ", // non-breaking space
}

// pdfMultiCell + pdfCellFormat sanitise text via ascii() before delegating
// to fpdf. Every text-emission site goes through one of these so a stray
// em-dash never escapes into the PDF as mojibake.

// pdfMultiCell wraps fpdf.MultiCell with ascii sanitisation. The full-width
// (w=0), no-border, left-align, and no-fill flags are baked in — every call
// site in this package wants those defaults. Add a second variant if a
// different combo is ever needed.
func pdfMultiCell(pdf *fpdf.Fpdf, _, h float64, txtStr, _, _ string, _ bool) {
	pdf.MultiCell(0, h, ascii(txtStr), "", "L", false)
}

// pdfCellFormat wraps fpdf.CellFormat with ascii sanitisation. ln/link/linkStr
// are always (0, 0, "") in this package; baked into the helper to keep call
// sites short. Add a separate variant if a different combo is ever needed.
func pdfCellFormat(pdf *fpdf.Fpdf, w, h float64, txtStr, borderStr string, _ int, alignStr string, fill bool, _ int, _ string) {
	pdf.CellFormat(w, h, ascii(txtStr), borderStr, 0, alignStr, fill, 0, "")
}

// ascii returns a CP1252-safe variant of s. Runes that already fit in
// CP1252 (Latin-1 + a few extras) survive; common typographic runes get
// ASCII analogues; anything else collapses to '?'.
func ascii(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, ok := asciiReplacements[r]; ok {
			b.WriteString(rep)
			continue
		}
		// CP1252 covers U+0000–U+00FF plus a handful of points in
		// U+0152–U+017E; reduce to plain Latin-1 to be safe with fpdf.
		if r < 0x100 {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('?')
	}
	return b.String()
}

// ── Misc utils ─────────────────────────────────────────────────────────────

func fmtQty(n float64) string {
	if n == float64(int64(n)) {
		return fmt.Sprintf("%d", int64(n))
	}
	return fmt.Sprintf("%.2f", n)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
