package reports

import (
	"bytes"
	"context"
	"strings"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/platform/storage"
	"github.com/riverqueue/river"
)

// GenerateCompliancePDFArgs is the River job payload for async compliance
// PDF generation. Distinct from GenerateReportArgs — that one builds CSV
// exports of audit events; this one builds the regulator-facing PDFs
// described in migration 00061's `reports` table.
type GenerateCompliancePDFArgs struct {
	ReportID uuid.UUID `json:"report_id"`
	ClinicID uuid.UUID `json:"clinic_id"`
}

// Kind returns the unique job type string used by River.
func (GenerateCompliancePDFArgs) Kind() string { return "generate_compliance_pdf" }

// GenerateCompliancePDFWorker dispatches to the right PDF builder by type
// (read off the report row), uploads the resulting bytes, and stamps the
// report row with file metadata + sha256.
//
// emailEnqueuer is optional — when set, scheduled reports with non-empty
// delivered_to_emails fan out to the SendReportEmail worker after a
// successful generation. Ad-hoc reports skip this fan-out.
type GenerateCompliancePDFWorker struct {
	river.WorkerDefaults[GenerateCompliancePDFArgs]
	repo          *Repository
	store         *storage.Store
	data          ComplianceDataSource
	emailEnqueuer jobEnqueuer
	// v2 is the HTML/Gotenberg-pipelined renderer that produces
	// doc-themed compliance PDFs. When non-nil the dispatch routes
	// supported types through it (audit_pack today, more migrating
	// behind it). When nil, falls back to the legacy fpdf builders.
	v2 V2ComplianceRenderer
}

// V2ComplianceRenderer is the cross-package port through which the
// worker calls into the v2 HTML pipeline. Implemented by a thin
// adapter in app.go around `*reportsv2.Renderer` so this package
// stays free of an import on its own subpackage.
type V2ComplianceRenderer interface {
	// RenderAuditPack returns PDF bytes for the period-wide
	// compliance audit pack. The clinic info / period / data lists
	// the worker has gathered get projected into the v2 input.
	RenderAuditPack(ctx context.Context, in V2ComplianceAuditPackInput) ([]byte, error)

	// RenderCDRegister returns PDF bytes for the controlled-drugs
	// register — flat drug-op list grouped per-drug + reconciliation
	// status. Mirrors v2.CDRegisterInput conceptually but kept as a
	// port-local type so reports doesn't import its own subpackage.
	RenderCDRegister(ctx context.Context, in V2CDRegisterInput) ([]byte, error)

	// RenderLog renders the generic compliance-log template — used
	// by all the list-shaped types (records audit, incidents log,
	// sentinel events, evidence pack, HIPAA disclosure log, DEA
	// biennial inventory). Each report supplies its own title +
	// section list; the template handles styled chrome.
	RenderLog(ctx context.Context, in V2ComplianceLogInput) ([]byte, error)

	// RenderPlaceholder returns a tiny styled "this report needs
	// more inputs" PDF — used by per-resident reports (MAR / Pain
	// Trend) when the preview path can't pick a sample resident.
	RenderPlaceholder(ctx context.Context, clinicID, title, message string, clinic V2ClinicInfo) ([]byte, error)
}

// V2ComplianceLogInput mirrors v2.ComplianceLogInput — port-local so
// reports doesn't import its own subpackage.
type V2ComplianceLogInput struct {
	ClinicID    string
	Clinic      V2ClinicInfo
	ReportID    string
	ReportTitle string
	Eyebrow     string
	Description string
	PeriodStart time.Time
	PeriodEnd   time.Time
	GeneratedAt time.Time
	Vertical    string
	Country     string
	Regulator   string
	Sections    []V2ComplianceLogSection
}

// V2ComplianceLogSection — one titled table on the report.
type V2ComplianceLogSection struct {
	Title    string
	Hint     string
	Columns  []V2ComplianceLogColumn
	Rows     []V2ComplianceLogRow
	EmptyMsg string
}

// V2ComplianceLogColumn — header + alignment.
type V2ComplianceLogColumn struct {
	Label string
	Width string
	Align string
}

// V2ComplianceLogRow — one row's cells.
type V2ComplianceLogRow struct {
	Cells      []V2ComplianceLogCell
	StatusTone string
}

// V2ComplianceLogCell — one cell; Pill replaces Value with a coloured pill.
type V2ComplianceLogCell struct {
	Value string
	Pill  string
}

// V2CDRegisterInput is the projected shape for the CD register.
// Built from clinic snapshot + flat DrugOpView list + reconciliations,
// then grouped per-drug in the adapter.
type V2CDRegisterInput struct {
	ClinicID         string
	Clinic           V2ClinicInfo
	PeriodLabel      string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	Drugs            []V2CDRegisterDrug
	ReconciliationOK bool
	ReconciledOn     string
	ReconciledByA    string
	ReconciledByB    string
	NextDueOn        string
	BundleHash       string
}

// V2CDRegisterDrug is one drug page in the register.
type V2CDRegisterDrug struct {
	Class        string // "B" | "C"
	Name         string
	FormStrength string
	Storage      string
	CatalogID    string
	BatchExp     string
	Unit         string
	Opening      float64
	ClosingBal   float64
	InTotal      float64
	OutTotal     float64
	Operations   []V2CDOperation
}

// V2CDOperation is one row of the per-drug page table.
type V2CDOperation struct {
	WhenPretty   string
	OpKind       string
	OpTone       string
	Subject      string
	QtyDelta     string
	BalBefore    string
	BalAfter     string
	StaffShort   string
	WitnessShort string
}

// V2ComplianceAuditPackInput mirrors v2.ComplianceAuditPackInput
// without leaking the v2 types — the adapter in app.go does the
// final type translation. Field semantics follow v2.
type V2ComplianceAuditPackInput struct {
	ClinicID        string
	Clinic          V2ClinicInfo
	ReportID        string
	PeriodStart     time.Time
	PeriodEnd       time.Time
	GeneratedAt     time.Time
	Vertical        string
	Country         string
	NoteCounts      map[string]int
	DrugOps         []V2ComplianceDrugOp
	Reconciliations []V2ComplianceReconciliation
}

// V2ClinicInfo is the slim clinic projection v2 partials need.
type V2ClinicInfo struct {
	Name         string
	AddressLine1 string
	Meta         string
}

// V2ComplianceDrugOp is the styled-table row for the drug-ops
// section. Tone hints drive the pill colour in the template.
type V2ComplianceDrugOp struct {
	When           string
	Drug           string
	Operation      string
	OperationTone  string
	Quantity       string
	BalanceAfter   string
	Subject        string
	AdministeredBy string
	WitnessedBy    string
}

// V2ComplianceReconciliation is one row of the reconciliations table.
type V2ComplianceReconciliation struct {
	Drug              string
	Period            string
	Physical          string
	Ledger            string
	DiscrepancyDelta  string
	Status            string
	StatusTone        string
	PrimarySignedBy   string
	SecondarySignedBy string
	Explanation       string
}

// NewGenerateCompliancePDFWorker constructs the worker. emailEnqueuer can
// be nil; the post-generation fan-out is then skipped. v2 may also be nil
// for tests / local dev — the worker falls back to the fpdf builders.
func NewGenerateCompliancePDFWorker(
	repo *Repository,
	store *storage.Store,
	data ComplianceDataSource,
	emailEnqueuer jobEnqueuer,
	v2 V2ComplianceRenderer,
) *GenerateCompliancePDFWorker {
	return &GenerateCompliancePDFWorker{
		repo: repo, store: store, data: data,
		emailEnqueuer: emailEnqueuer,
		v2:            v2,
	}
}

// Work runs the compliance PDF generation pipeline:
//  1. Load the report row (no clinic scope — internal worker).
//  2. Mark running.
//  3. Resolve clinic snapshot, fetch domain data via the data source.
//  4. Dispatch to the right builder by type.
//  5. Upload PDF to storage.
//  6. Mark done with file_key + size + sha256.
func (w *GenerateCompliancePDFWorker) Work(ctx context.Context, job *river.Job[GenerateCompliancePDFArgs]) error {
	args := job.Args

	rec, err := w.repo.GetComplianceReportInternal(ctx, args.ReportID)
	if err != nil {
		return fmt.Errorf("generate_compliance_pdf: load: %w", err)
	}

	if err := w.repo.MarkComplianceReportRunning(ctx, args.ReportID); err != nil {
		return fmt.Errorf("generate_compliance_pdf: mark running: %w", err)
	}

	buf, hash, err := w.buildPDF(ctx, rec)
	if err != nil {
		errMsg := err.Error()
		_ = w.repo.MarkComplianceReportFailed(ctx, args.ReportID, errMsg)
		return fmt.Errorf("generate_compliance_pdf: build: %w", err)
	}

	key := fmt.Sprintf("compliance-reports/%s/%s.%s",
		rec.ClinicID, rec.ID, rec.FileFormat)
	// S3's PutObject hashes the payload before sending and requires a
	// seekable reader. *bytes.Buffer is not seekable; *bytes.Reader is.
	payload := bytes.NewReader(buf.Bytes())
	size := int64(payload.Len())
	if err := w.store.Upload(ctx, key, contentTypeFor(rec.FileFormat), payload, size); err != nil {
		errMsg := fmt.Sprintf("upload: %v", err)
		_ = w.repo.MarkComplianceReportFailed(ctx, args.ReportID, errMsg)
		return fmt.Errorf("generate_compliance_pdf: %w", err)
	}

	if err := w.repo.MarkComplianceReportDone(ctx, args.ReportID, key, size, hash); err != nil {
		return fmt.Errorf("generate_compliance_pdf: mark done: %w", err)
	}

	// If this report has scheduled recipients (set by the
	// FireDueReportSchedules worker before generation), enqueue the
	// email-delivery job. Ad-hoc reports leave delivered_to_emails NULL
	// and skip this branch.
	recipients, err := w.repo.GetReportRecipients(ctx, args.ReportID)
	if err == nil && len(recipients) > 0 && w.emailEnqueuer != nil {
		_, _ = w.emailEnqueuer.Insert(ctx, SendReportEmailArgs{ReportID: args.ReportID}, nil)
	}
	return nil
}

// buildPDF dispatches by report type. New types: add a case here + register
// a builder in pdf.go. Vertical/country come off the row; the builder reads
// regulator labels from regulatorContexts inside.
func (w *GenerateCompliancePDFWorker) buildPDF(ctx context.Context, rec *ComplianceReportRecord) (*bytes.Buffer, string, error) {
	clinic, err := w.data.GetClinic(ctx, rec.ClinicID)
	if err != nil {
		return nil, "", fmt.Errorf("clinic: %w", err)
	}

	switch rec.Type {
	case "controlled_drugs_register":
		ops, err := w.data.ListControlledDrugOps(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("ops: %w", err)
		}
		recons, err := w.data.ListReconciliationsInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("recons: %w", err)
		}
		// Prefer v2 HTML pipeline — same doc-themed chrome the rest
		// of the app uses. Falls back to fpdf when no v2 wired.
		if w.v2 != nil {
			bytesOut, err := w.v2.RenderCDRegister(ctx, V2CDRegisterInput{
				ClinicID:    rec.ClinicID.String(),
				Clinic:      clinicSnapshotToV2(clinic),
				PeriodLabel: rec.PeriodStart.UTC().Format("Jan 2006") + " · " + rec.PeriodStart.UTC().Format("02") + "–" + rec.PeriodEnd.UTC().Format("02"),
				PeriodStart: rec.PeriodStart,
				PeriodEnd:   rec.PeriodEnd,
				Drugs:       drugOpsToCDRegisterDrugs(ops),
				ReconciliationOK: reconciliationsAllClean(recons),
				ReconciledOn:     reconciledOnLabel(recons),
				ReconciledByA:    reconciledByA(recons),
				ReconciledByB:    reconciledByB(recons),
				NextDueOn:        rec.PeriodEnd.UTC().AddDate(0, 1, 0).Format("2006-01-02"),
				BundleHash:       shortReportIDHelper(rec.ID.String()),
			})
			if err != nil {
				return nil, "", fmt.Errorf("v2 cd_register: %w", err)
			}
			buf := bytes.NewBuffer(bytesOut)
			return buf, sha256Hex(bytesOut), nil
		}
		return BuildControlledDrugsRegisterPDF(clinic, rec.PeriodStart, rec.PeriodEnd, ops, recons, rec.ID.String())

	case "audit_pack":
		ops, err := w.data.ListControlledDrugOps(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("ops: %w", err)
		}
		recons, err := w.data.ListReconciliationsInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("recons: %w", err)
		}
		counts, err := w.data.CountNotesByStatus(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("note counts: %w", err)
		}
		// Prefer the v2 HTML pipeline when the renderer is wired —
		// gives the regulator-facing PDF the same doc-themed chrome
		// the rest of the app uses (header logo, brand colors,
		// footer hash). Falls back to the legacy fpdf builder when
		// the v2 renderer is nil so tests/local dev keep working.
		if w.v2 != nil {
			bytesOut, err := w.v2.RenderAuditPack(ctx, V2ComplianceAuditPackInput{
				ClinicID:        rec.ClinicID.String(),
				Clinic:          clinicSnapshotToV2(clinic),
				ReportID:        rec.ID.String(),
				PeriodStart:     rec.PeriodStart,
				PeriodEnd:       rec.PeriodEnd,
				GeneratedAt:     time.Now().UTC(),
				Vertical:        clinic.Vertical,
				Country:         clinic.Country,
				NoteCounts:      counts,
				DrugOps:         drugOpsToV2(ops),
				Reconciliations: reconsToV2(recons),
			})
			if err != nil {
				return nil, "", fmt.Errorf("v2 audit_pack: %w", err)
			}
			buf := bytes.NewBuffer(bytesOut)
			return buf, sha256Hex(bytesOut), nil
		}
		return BuildAuditPackPDF(clinic, rec.PeriodStart, rec.PeriodEnd, ops, recons, counts, rec.ID.String())

	case "evidence_pack":
		in, err := w.fetchEvidencePackInput(ctx, rec, clinic)
		if err != nil {
			return nil, "", err
		}
		return BuildEvidencePackPDF(*in)

	case "records_audit":
		in, err := w.fetchEvidencePackInput(ctx, rec, clinic)
		if err != nil {
			return nil, "", err
		}
		return BuildRecordsAuditPDF(*in)

	case "incidents_log":
		in, err := w.fetchEvidencePackInput(ctx, rec, clinic)
		if err != nil {
			return nil, "", err
		}
		return BuildIncidentsLogPDF(*in)

	case "sentinel_events_log":
		in, err := w.fetchEvidencePackInput(ctx, rec, clinic)
		if err != nil {
			return nil, "", err
		}
		return BuildSentinelEventsLogPDF(*in)

	case "hipaa_disclosure_log":
		in, err := w.fetchEvidencePackInput(ctx, rec, clinic)
		if err != nil {
			return nil, "", err
		}
		access, err := w.data.ListSubjectAccessInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("access log: %w", err)
		}
		in.AccessLog = access
		return BuildHIPAADisclosureLogPDF(*in)

	case "dea_biennial_inventory":
		in, err := w.fetchEvidencePackInput(ctx, rec, clinic)
		if err != nil {
			return nil, "", err
		}
		snapshot, err := w.data.ListControlledShelfSnapshot(ctx, rec.ClinicID)
		if err != nil {
			return nil, "", fmt.Errorf("shelf snapshot: %w", err)
		}
		in.ShelfSnapshot = snapshot
		return BuildDEABiennialInventoryPDF(*in)

	default:
		return nil, "", fmt.Errorf("unknown compliance report type: %s", rec.Type)
	}
}

// fetchEvidencePackInput hydrates the universal EvidencePackInput from
// every dataset the evidence-pack / records-audit / incidents-log builders
// can read. Each section renderer pulls only what it needs from the same
// struct; missing data turns into an "empty" section in the PDF.
func (w *GenerateCompliancePDFWorker) fetchEvidencePackInput(ctx context.Context, rec *ComplianceReportRecord, clinic *ClinicSnapshot) (*EvidencePackInput, error) {
	ops, err := w.data.ListControlledDrugOps(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("ops: %w", err)
	}
	recons, err := w.data.ListReconciliationsInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("recons: %w", err)
	}
	counts, err := w.data.CountNotesByStatus(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("note counts: %w", err)
	}
	incidents, err := w.data.ListIncidentsInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("incidents: %w", err)
	}
	consent, err := w.data.ConsentSummaryInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("consent: %w", err)
	}
	pain, err := w.data.PainSummaryInPeriod(ctx, rec.ClinicID, rec.PeriodStart, rec.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("pain: %w", err)
	}
	return &EvidencePackInput{
		Clinic:      clinic,
		PeriodStart: rec.PeriodStart,
		PeriodEnd:   rec.PeriodEnd,
		ReportID:    rec.ID.String(),
		NoteCounts:  counts,
		DrugOps:     ops,
		Recons:      recons,
		Incidents:   incidents,
		Consent:     consent,
		Pain:        pain,
	}, nil
}

func contentTypeFor(format string) string {
	switch format {
	case "pdf":
		return "application/pdf"
	case "zip":
		return "application/zip"
	case "csv":
		return "text/csv"
	default:
		return "application/octet-stream"
	}
}

// FireDueReportSchedulesArgs is the River job payload for the periodic
// schedule-firing loop. The worker scans report_schedules for rows whose
// next_run_at is past, creates a queued report row + enqueues a
// GenerateCompliancePDFArgs job for each, and bumps next_run_at.
//
// Periodic-cadence: hourly, configured via river.PeriodicJobs in app.go.
type FireDueReportSchedulesArgs struct{}

func (FireDueReportSchedulesArgs) Kind() string { return "fire_due_report_schedules" }

// FireDueReportSchedulesWorker runs the schedule fire loop.
type FireDueReportSchedulesWorker struct {
	river.WorkerDefaults[FireDueReportSchedulesArgs]
	repo  *Repository
	queue jobEnqueuer
}

func NewFireDueReportSchedulesWorker(repo *Repository, queue jobEnqueuer) *FireDueReportSchedulesWorker {
	return &FireDueReportSchedulesWorker{repo: repo, queue: queue}
}

func (w *FireDueReportSchedulesWorker) Work(ctx context.Context, _ *river.Job[FireDueReportSchedulesArgs]) error {
	now := time.Now().UTC()
	due, err := w.repo.ListDueReportSchedules(ctx, now)
	if err != nil {
		return fmt.Errorf("fire_due_report_schedules: list: %w", err)
	}
	for _, sched := range due {
		periodStart, periodEnd := PeriodForFire(sched.Frequency, sched.NextRunAt)

		// Insert a queued compliance report row with the schedule's
		// recipients copied as delivered_to_emails so the post-completion
		// email worker knows where to deliver. We set delivered_to_emails
		// at insert time (before generation) so recipients are guaranteed
		// even if the schedule edits between fire and finish.
		reportID, err := w.queueReportForSchedule(ctx, sched, periodStart, periodEnd)
		if err != nil {
			return fmt.Errorf("fire_due_report_schedules: queue: %w", err)
		}

		// Bump next_run_at past the current fire so we don't re-fire on
		// the next sweep. For monthly/quarterly we recompute from the
		// fire timestamp, not "now" — keeps the cadence on the period
		// boundary regardless of jitter in the cron.
		next := nextFireFromNow(sched.NextRunAt, sched.Frequency)
		if err := w.repo.MarkScheduleFired(ctx, sched.ID, reportID, next); err != nil {
			return fmt.Errorf("fire_due_report_schedules: mark: %w", err)
		}
	}
	return nil
}

func (w *FireDueReportSchedulesWorker) queueReportForSchedule(ctx context.Context, sched *ReportScheduleRecord, periodStart, periodEnd time.Time) (uuid.UUID, error) {
	// Insert a "queued" compliance report row + stamp delivered_to_emails
	// (it stays NULL on regular ad-hoc reports; the email worker uses
	// non-null as the trigger).
	id := uuid.New()
	rec, err := w.repo.CreateComplianceReport(ctx, CreateComplianceReportParams{
		ID:          id,
		ClinicID:    sched.ClinicID,
		Type:        sched.ReportType,
		// Vertical / country come from the clinic at adapter time during
		// generation; we don't have a clinic lookup in the worker. Leave
		// them empty here — the worker re-resolves via ComplianceDataSource.
		Vertical:    "",
		Country:     "",
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		FileFormat:  fileFormatFor(sched.ReportType),
		RequestedBy: sched.CreatedBy,
	})
	if err != nil {
		return uuid.Nil, err
	}

	// Stamp recipients on the report row so the email worker delivers
	// to the right address list when generation completes.
	if err := w.repo.SetReportRecipients(ctx, id, sched.Recipients); err != nil {
		return uuid.Nil, err
	}

	if _, err := w.queue.Insert(ctx, GenerateCompliancePDFArgs{
		ReportID: rec.ID,
		ClinicID: rec.ClinicID,
	}, nil); err != nil {
		return uuid.Nil, fmt.Errorf("enqueue: %w", err)
	}
	return rec.ID, nil
}

// SendReportEmailArgs — fired after MarkComplianceReportDone for reports
// whose delivered_to_emails is non-empty. The worker mints a fresh
// 1-hour presigned URL per recipient (so one user resharing the email
// doesn't expose the link beyond its TTL) and sends the email via mailer.
type SendReportEmailArgs struct {
	ReportID uuid.UUID `json:"report_id"`
}

func (SendReportEmailArgs) Kind() string { return "send_report_email" }

// SendReportEmailWorker delivers a completed compliance report to its
// scheduled recipients.
type SendReportEmailWorker struct {
	river.WorkerDefaults[SendReportEmailArgs]
	repo   *Repository
	store  EmailWorkerStorage
	mailer EmailWorkerMailer
	clinic EmailWorkerClinicLookup
}

// EmailWorkerStorage = the subset of storage.Store the email worker uses.
type EmailWorkerStorage interface {
	PresignDownload(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// EmailWorkerMailer = the subset of mailer.Mailer the email worker uses.
type EmailWorkerMailer interface {
	SendComplianceReportReady(ctx context.Context, to, clinicName, reportType, periodStart, periodEnd, downloadURL string) error
}

// EmailWorkerClinicLookup resolves the clinic display name. Same shape
// pattern as elsewhere — the email worker doesn't import clinic types.
type EmailWorkerClinicLookup interface {
	GetClinicNameForEmail(ctx context.Context, clinicID uuid.UUID) (string, error)
}

func NewSendReportEmailWorker(repo *Repository, store EmailWorkerStorage, mailer EmailWorkerMailer, clinic EmailWorkerClinicLookup) *SendReportEmailWorker {
	return &SendReportEmailWorker{repo: repo, store: store, mailer: mailer, clinic: clinic}
}

func (w *SendReportEmailWorker) Work(ctx context.Context, job *river.Job[SendReportEmailArgs]) error {
	rec, err := w.repo.GetComplianceReportInternal(ctx, job.Args.ReportID)
	if err != nil {
		return fmt.Errorf("send_report_email: load: %w", err)
	}
	if rec.Status != "done" || rec.FileKey == nil {
		return fmt.Errorf("send_report_email: report not done")
	}
	recipients, err := w.repo.GetReportRecipients(ctx, rec.ID)
	if err != nil {
		return fmt.Errorf("send_report_email: recipients: %w", err)
	}
	if len(recipients) == 0 {
		return nil // ad-hoc report, no scheduled delivery
	}

	clinicName, err := w.clinic.GetClinicNameForEmail(ctx, rec.ClinicID)
	if err != nil {
		clinicName = "your clinic"
	}

	periodStart := rec.PeriodStart.UTC().Format("2 Jan 2006")
	periodEnd := rec.PeriodEnd.UTC().Format("2 Jan 2006")

	for _, to := range recipients {
		url, err := w.store.PresignDownload(ctx, *rec.FileKey, time.Hour)
		if err != nil {
			return fmt.Errorf("send_report_email: presign for %s: %w", to, err)
		}
		if err := w.mailer.SendComplianceReportReady(ctx, to, clinicName, rec.Type, periodStart, periodEnd, url); err != nil {
			// Soft-fail per recipient — log + continue rather than blocking
			// the rest of the list on a single bad address.
			continue
		}
	}
	if err := w.repo.MarkReportDelivered(ctx, rec.ID); err != nil {
		return fmt.Errorf("send_report_email: mark delivered: %w", err)
	}
	return nil
}

// GenerateReportArgs is the River job payload for async report export.
type GenerateReportArgs struct {
	JobID      uuid.UUID     `json:"job_id"`
	ClinicID   uuid.UUID     `json:"clinic_id"`
	ReportType string        `json:"report_type"`
	Format     string        `json:"format"`
	Filters    ReportFilters `json:"filters"`
}

// Kind returns the unique job type string used by River.
func (GenerateReportArgs) Kind() string { return "generate_report" }

// GenerateReportWorker produces the report file, uploads it to S3, and marks the job complete.
type GenerateReportWorker struct {
	river.WorkerDefaults[GenerateReportArgs]
	repo  *Repository
	store *storage.Store
}

// NewGenerateReportWorker constructs a GenerateReportWorker.
func NewGenerateReportWorker(repo *Repository, store *storage.Store) *GenerateReportWorker {
	return &GenerateReportWorker{repo: repo, store: store}
}

// Work executes the report generation job.
func (w *GenerateReportWorker) Work(ctx context.Context, job *river.Job[GenerateReportArgs]) error {
	args := job.Args

	switch args.Format {
	case "csv", "":
	default:
		errMsg := fmt.Sprintf("unsupported format: %s", args.Format)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %s", errMsg)
	}

	// Build the CSV in memory using paginated fetches to avoid loading tens of
	// thousands of rows at once.
	var buf bytes.Buffer
	if err := w.buildCSV(ctx, args, &buf); err != nil {
		errMsg := fmt.Sprintf("build csv: %v", err)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %w", err)
	}

	// Compute SHA-256 of the CSV content for integrity verification.
	hash := sha256.Sum256(buf.Bytes())
	contentHash := "sha256:" + hex.EncodeToString(hash[:])

	// Upload to S3. PutObject hashes the payload before sending and needs
	// a seekable reader; *bytes.Buffer is not seekable, *bytes.Reader is.
	key := fmt.Sprintf("reports/%s/%s.csv", args.ClinicID, args.JobID)
	payload := bytes.NewReader(buf.Bytes())
	size := int64(payload.Len())
	if err := w.store.Upload(ctx, key, "text/csv", payload, size); err != nil {
		errMsg := fmt.Sprintf("upload: %v", err)
		_ = w.repo.MarkFailed(ctx, args.JobID, errMsg)
		return fmt.Errorf("generate_report: %w", err)
	}

	// Mark complete — handler generates presigned URL on demand from the key.
	if err := w.repo.MarkComplete(ctx, args.JobID, key, contentHash); err != nil {
		return fmt.Errorf("generate_report: mark complete: %w", err)
	}

	return nil
}

const pageSize = 1_000

// buildCSV writes the full report as CSV to buf, fetching rows in pages of
// pageSize to avoid loading the entire result set into memory at once.
func (w *GenerateReportWorker) buildCSV(ctx context.Context, args GenerateReportArgs, buf *bytes.Buffer) error {
	cw := csv.NewWriter(buf)
	if err := cw.Write(csvHeaders); err != nil {
		return fmt.Errorf("header: %w", err)
	}

	// note_history is bounded and does not use the pagination path.
	if args.ReportType == "note_history" {
		if args.Filters.NoteID == nil {
			return fmt.Errorf("note_id required for note_history report")
		}
		events, err := w.repo.QueryNoteHistory(ctx, *args.Filters.NoteID, args.ClinicID)
		if err != nil {
			return err
		}
		return writeRows(cw, events)
	}

	for offset := 0; ; offset += pageSize {
		p := ListParams{Limit: pageSize, Offset: offset}
		var (
			events []*AuditEventRecord
			err    error
		)
		switch args.ReportType {
		case "clinical_audit":
			events, _, err = w.repo.QueryClinicalAudit(ctx, args.ClinicID, args.Filters, p)
		case "staff_actions":
			if args.Filters.StaffID == nil {
				return fmt.Errorf("staff_id required for staff_actions report")
			}
			events, _, err = w.repo.QueryStaffActions(ctx, args.ClinicID, *args.Filters.StaffID, args.Filters, p)
		case "consent_log":
			events, _, err = w.repo.QueryConsentLog(ctx, args.ClinicID, args.Filters, p)
		default:
			return fmt.Errorf("unknown report_type: %s", args.ReportType)
		}
		if err != nil {
			return err
		}
		if err := writeRows(cw, events); err != nil {
			return err
		}
		if len(events) < pageSize {
			break
		}
	}

	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("csv flush: %w", err)
	}
	return nil
}

// ── CSV renderer ──────────────────────────────────────────────────────────────

var csvHeaders = []string{
	"occurred_at", "event_type", "note_id", "subject_id",
	"actor_id", "actor_role", "field_id", "old_value", "new_value", "reason",
}

func writeRows(w *csv.Writer, events []*AuditEventRecord) error {
	for _, e := range events {
		row := []string{
			e.OccurredAt.UTC().Format(time.RFC3339),
			e.EventType,
			e.NoteID.String(),
			nilUUID(e.SubjectID),
			e.ActorID.String(),
			e.ActorRole,
			nilUUID(e.FieldID),
			nilStr(e.OldValue),
			nilStr(e.NewValue),
			nilStr(e.Reason),
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writeRows: %w", err)
		}
	}
	return nil
}

func nilUUID(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func nilStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ── v2 projection helpers ───────────────────────────────────────────

// clinicSnapshotToV2 projects the worker-side clinic snapshot into the
// v2 brand-mark struct. Concatenates address + phone + email into the
// AddressLine1 field — the partial renders one line under the clinic
// name and that's the most-useful thing to put there.
func clinicSnapshotToV2(c *ClinicSnapshot) V2ClinicInfo {
	out := V2ClinicInfo{Name: c.Name}
	parts := []string{}
	if c.Address != nil && *c.Address != "" {
		parts = append(parts, *c.Address)
	}
	if c.Phone != nil && *c.Phone != "" {
		parts = append(parts, *c.Phone)
	}
	if c.Email != nil && *c.Email != "" {
		parts = append(parts, *c.Email)
	}
	out.AddressLine1 = strings.Join(parts, " · ")
	if c.License != nil && *c.License != "" {
		out.Meta = "License " + *c.License
	}
	return out
}

// drugOpsToV2 projects the drug-op view rows into the styled
// table-row shape the v2 template expects. Operation tone drives the
// pill colour; we map the existing op vocab to the same tone palette
// the v2.cd_register template uses (DISCARD = danger, RECEIVE = info,
// everything else = ok).
func drugOpsToV2(ops []DrugOpView) []V2ComplianceDrugOp {
	out := make([]V2ComplianceDrugOp, 0, len(ops))
	for _, o := range ops {
		out = append(out, V2ComplianceDrugOp{
			When:           o.CreatedAt.UTC().Format("02 Jan 15:04"),
			Drug:           o.ShelfLabel,
			Operation:      strings.ToUpper(o.Operation),
			OperationTone:  toneForOpKind(o.Operation),
			Quantity:       fmt.Sprintf("%.1f %s", o.Quantity, o.Unit),
			BalanceAfter:   fmt.Sprintf("%.1f %s", o.BalanceAfter, o.Unit),
			Subject:        derefSubject(o.SubjectName),
			AdministeredBy: o.AdministeredBy,
			WitnessedBy:    derefSubject(o.WitnessedBy),
		})
	}
	return out
}

// reconsToV2 projects reconciliation rows into the v2 table shape.
// Status tone follows: clean=ok, explained=warn, anything else=danger.
func reconsToV2(recs []DrugReconciliationView) []V2ComplianceReconciliation {
	out := make([]V2ComplianceReconciliation, 0, len(recs))
	for _, r := range recs {
		delta := fmt.Sprintf("%+.1f", r.Discrepancy)
		secondary := ""
		if r.SecondarySignedBy != nil {
			secondary = *r.SecondarySignedBy
		}
		expl := ""
		if r.Explanation != nil {
			expl = *r.Explanation
		}
		out = append(out, V2ComplianceReconciliation{
			Drug:              r.ShelfLabel,
			Period:            r.PeriodStart.UTC().Format("02 Jan") + " → " + r.PeriodEnd.UTC().Format("02 Jan"),
			Physical:          fmt.Sprintf("%.1f", r.PhysicalCount),
			Ledger:            fmt.Sprintf("%.1f", r.LedgerCount),
			DiscrepancyDelta:  delta,
			Status:            r.Status,
			StatusTone:        toneForReconStatus(r.Status),
			PrimarySignedBy:   r.PrimarySignedBy,
			SecondarySignedBy: secondary,
			Explanation:       expl,
		})
	}
	return out
}

func toneForOpKind(op string) string {
	switch op {
	case "discard":
		return "danger"
	case "receive":
		return "info"
	case "transfer":
		return "warn"
	default:
		return "ok"
	}
}

func toneForReconStatus(s string) string {
	switch strings.ToLower(s) {
	case "clean":
		return "ok"
	case "explained":
		return "warn"
	default:
		return "danger"
	}
}

func derefSubject(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// drugOpsToCDRegisterDrugs groups a flat ops list by shelf label →
// per-drug pages with running totals. Used by both the request-flow
// worker and the inline-preview path so the CD register always
// renders identically regardless of how it was triggered.
//
// One drug "page" per unique shelf label; class is stamped from the
// op's Schedule field; opening balance is derived as
// `BalanceAfter - QuantitySigned` of the FIRST op in the period.
// Closing is the BalanceAfter of the LAST op. In/Out totals fan out
// across the operation kind.
func drugOpsToCDRegisterDrugs(ops []DrugOpView) []V2CDRegisterDrug {
	groups := map[string]*V2CDRegisterDrug{}
	order := []string{}
	for _, o := range ops {
		key := o.ShelfLabel
		if _, ok := groups[key]; !ok {
			groups[key] = &V2CDRegisterDrug{
				Class:        cdClassFor(o.Schedule),
				Name:         o.ShelfLabel,
				FormStrength: "",
				Storage:      o.Location,
				CatalogID:    o.ShelfID,
				BatchExp:     derefSubject(o.BatchNumber),
				Unit:         o.Unit,
				Operations:   []V2CDOperation{},
			}
			order = append(order, key)
		}
		g := groups[key]
		// Quantity sign: receive/transfer-in are positive; everything
		// else is treated as negative. Reflects the running balance
		// already maintained on the row, so we just label.
		signed := o.Quantity
		isOut := true
		switch o.Operation {
		case "receive":
			isOut = false
		}
		if isOut {
			g.OutTotal += signed
		} else {
			g.InTotal += signed
		}
		// Opening = before-balance of the first op (which is the
		// LAST one in the slice if the repo returns DESC; we assume
		// ASC here per ListControlledDrugOps contract).
		if len(g.Operations) == 0 {
			g.Opening = o.BalanceAfter - signedDelta(o.Operation, o.Quantity)
		}
		g.ClosingBal = o.BalanceAfter
		g.Operations = append(g.Operations, V2CDOperation{
			WhenPretty:   o.CreatedAt.UTC().Format("02 Jan 15:04"),
			OpKind:       strings.ToUpper(o.Operation),
			OpTone:       toneForOpKind(o.Operation),
			Subject:      derefSubject(o.SubjectName),
			QtyDelta:     signedQtyLabel(o.Operation, o.Quantity),
			BalBefore:    fmt.Sprintf("%.1f", o.BalanceAfter-signedDelta(o.Operation, o.Quantity)),
			BalAfter:     fmt.Sprintf("%.1f", o.BalanceAfter),
			StaffShort:   shortName(o.AdministeredBy),
			WitnessShort: derefSubject(o.WitnessedBy),
		})
	}
	out := make([]V2CDRegisterDrug, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	return out
}

func cdClassFor(schedule string) string {
	s := strings.ToUpper(strings.TrimSpace(schedule))
	switch s {
	case "S1", "B", "C2", "CD2":
		return "B"
	case "S2", "S3", "C3", "CD3", "CIV", "CV":
		return "C"
	default:
		if s == "" {
			return "B"
		}
		return s
	}
}

// signedDelta returns the signed quantity delta the operation
// applied to the shelf balance. receive = +qty, everything else = -qty.
// Used to back-compute "balance before" from the stored "balance after".
func signedDelta(op string, qty float64) float64 {
	if op == "receive" {
		return qty
	}
	return -qty
}

func signedQtyLabel(op string, qty float64) string {
	if op == "receive" {
		return fmt.Sprintf("+%.1f", qty)
	}
	return fmt.Sprintf("−%.1f", qty)
}

func shortName(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return "—"
	}
	parts := strings.Fields(full)
	if len(parts) == 1 {
		return parts[0]
	}
	return string([]rune(parts[0])[0]) + ". " + parts[len(parts)-1]
}

// reconciliationsAllClean returns true when every reconciliation in
// the period has the "clean" status. Drives the green callout on the
// CD-register cover page.
func reconciliationsAllClean(recs []DrugReconciliationView) bool {
	if len(recs) == 0 {
		return true
	}
	for _, r := range recs {
		if !strings.EqualFold(r.Status, "clean") {
			return false
		}
	}
	return true
}

// reconciledOnLabel returns the most-recent reconciliation timestamp
// for the cover page. Empty when no reconciliation in period.
func reconciledOnLabel(recs []DrugReconciliationView) string {
	if len(recs) == 0 {
		return "—"
	}
	latest := recs[0].PeriodEnd
	for _, r := range recs[1:] {
		if r.PeriodEnd.After(latest) {
			latest = r.PeriodEnd
		}
	}
	return latest.UTC().Format("2006-01-02")
}

// reconciledByA returns the primary signatory of the most-recent
// reconciliation. Empty when no recon.
func reconciledByA(recs []DrugReconciliationView) string {
	if len(recs) == 0 {
		return "—"
	}
	return recs[len(recs)-1].PrimarySignedBy
}

// reconciledByB returns the secondary signatory of the most-recent
// reconciliation. Falls back to "—" when none signed off.
func reconciledByB(recs []DrugReconciliationView) string {
	if len(recs) == 0 {
		return "—"
	}
	r := recs[len(recs)-1]
	if r.SecondarySignedBy != nil && *r.SecondarySignedBy != "" {
		return *r.SecondarySignedBy
	}
	return "—"
}

// shortReportIDHelper trims to 12 chars for the footer hash chip.
// Local helper to avoid colliding with v2.shortReportID; same intent.
func shortReportIDHelper(id string) string {
	if len(id) >= 12 {
		return id[:12]
	}
	return id
}
