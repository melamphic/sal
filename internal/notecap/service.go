// Package notecap enforces the per-period note allowance specified in
// pricing-model-v3 §7. Two enforcement axes:
//
//  1. Active subscriptions: every note created since
//     clinics.billing_period_start counts against plan.NoteCap. The
//     80%/110%/150% cascade fires emails (warning, CS notification) the
//     first time a clinic crosses each threshold in the period; 150%
//     hard-blocks further creates with domain.ErrForbidden.
//
//  2. Trial clinics: 100 notes total over the entire 21-day trial,
//     counted from clinics.created_at (no billing period yet).
//
// notecap is a leaf cross-domain coordinator — it imports nothing from
// other domains directly. Callers in app.go wire it via thin adapters.
package notecap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// trialNoteCap is the hard ceiling for a clinic still on its 21-day
// trial (pricing-model-v3 §7 "Trial period"). Independent of plan
// configuration since trial clinics haven't picked a plan yet.
const trialNoteCap = 100

// Threshold percentages — the 80/110/150 cascade in pricing-model-v3.
const (
	thresholdWarn  = 80  // email the clinic admin
	thresholdAlert = 110 // page CS / ops
	thresholdBlock = 150 // hard block on note create
	hundredPercent = 100
)

// ErrTrialNoteCapExceeded is returned by CheckCanCreate when a trial
// clinic has used all 100 notes. Wraps domain.ErrForbidden so the HTTP
// layer maps it to 403. The message is intentionally caller-friendly —
// it bubbles up to the FE which surfaces it directly to the clinician.
var ErrTrialNoteCapExceeded = fmt.Errorf("trial note cap reached: upgrade to continue: %w", domain.ErrForbidden)

// ErrNoteCapExceeded is returned at the 150% block threshold for active
// clinics. Same wrapping strategy as the trial variant.
var ErrNoteCapExceeded = fmt.Errorf("note cap reached: upgrade plan to continue: %w", domain.ErrForbidden)

// ClinicState is the read-only slice of clinic state notecap needs.
// The app-layer adapter loads it from clinic.Service.
type ClinicState struct {
	ID                 uuid.UUID
	Name               string
	AdminEmail         string // first admin staff email; empty when none
	Status             domain.ClinicStatus
	PlanCode           *domain.PlanCode
	BillingPeriodStart *time.Time // nil = trial fallback (use CreatedAt)
	CreatedAt          time.Time
	NoteCapWarnedAt    *time.Time // sticky flags for cascade idempotency
	NoteCapCSAlertedAt *time.Time
	NoteCapBlockedAt   *time.Time
}

// ClinicReader is the cross-domain port to the clinic module. The app
// layer wires a thin adapter that calls into clinic.Service.
type ClinicReader interface {
	LoadForCap(ctx context.Context, clinicID uuid.UUID) (ClinicState, error)
	MarkNoteCapWarned(ctx context.Context, clinicID uuid.UUID) (claimed bool, err error)
	MarkNoteCapCSAlerted(ctx context.Context, clinicID uuid.UUID) (claimed bool, err error)
	MarkNoteCapBlocked(ctx context.Context, clinicID uuid.UUID) (claimed bool, err error)
}

// NoteCounter is the cross-domain port to the notes module — counts
// non-archived notes created since `since`.
type NoteCounter interface {
	CountSinceForClinic(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error)
}

// Mailer sends the threshold cascade emails. CS alerts go to the ops
// inbox configured at startup (OPS_ALERT_EMAIL); the warning goes to
// the clinic admin. Plan is passed as a string (not domain.PlanCode)
// so the platform mailer interface can satisfy this without importing
// the domain package.
type Mailer interface {
	SendNoteCapWarning(ctx context.Context, to, clinicName string, current, capLimit int) error
	SendNoteCapCSAlert(ctx context.Context, opsEmail, clinicID, clinicName string, current, capLimit int, plan string) error
}

// Service is the public API of the notecap module — implements
// notes.NoteCapEnforcer. Construct via NewService.
type Service struct {
	clinics  ClinicReader
	notes    NoteCounter
	mail     Mailer
	opsEmail string
	log      *slog.Logger
}

// NewService constructs a Service. opsEmail is the destination for the
// 110% CS notification — leaving it empty silently disables the CS
// branch (warnings still fire). log is required.
func NewService(c ClinicReader, n NoteCounter, m Mailer, opsEmail string, log *slog.Logger) *Service {
	return &Service{clinics: c, notes: n, mail: m, opsEmail: opsEmail, log: log}
}

// CheckCanCreate is the gate called before notes.service.CreateNote
// commits a row. Returns nil for clinics under 150% (or trials under
// 100 notes); ErrTrialNoteCapExceeded / ErrNoteCapExceeded otherwise.
func (s *Service) CheckCanCreate(ctx context.Context, clinicID uuid.UUID) error {
	c, err := s.clinics.LoadForCap(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("notecap.service.CheckCanCreate: load: %w", err)
	}

	capLimit, since, isTrial := s.window(c)
	if capLimit <= 0 {
		// Unknown plan / no cap configured — fail open.
		return nil
	}

	count, err := s.notes.CountSinceForClinic(ctx, clinicID, since)
	if err != nil {
		return fmt.Errorf("notecap.service.CheckCanCreate: count: %w", err)
	}

	if isTrial {
		if count >= trialNoteCap {
			return ErrTrialNoteCapExceeded
		}
		return nil
	}

	if pct(count, capLimit) >= thresholdBlock {
		return ErrNoteCapExceeded
	}
	return nil
}

// Evaluate runs after a successful create and triggers the 80%/110%
// emails on first crossing. Mailer failures are logged and swallowed —
// only DB-level errors bubble up.
func (s *Service) Evaluate(ctx context.Context, clinicID uuid.UUID) error {
	c, err := s.clinics.LoadForCap(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("notecap.service.Evaluate: load: %w", err)
	}

	capLimit, since, isTrial := s.window(c)
	if capLimit <= 0 {
		s.log.WarnContext(ctx, "notecap: skipping evaluate, no cap resolved",
			slog.String("clinic_id", clinicID.String()),
			slog.String("status", string(c.Status)),
		)
		return nil
	}

	count, err := s.notes.CountSinceForClinic(ctx, clinicID, since)
	if err != nil {
		return fmt.Errorf("notecap.service.Evaluate: count: %w", err)
	}
	currentPct := pct(count, capLimit)

	// Trial clinics get a single "you're at the limit" warning at 80%.
	// 110/150 don't make sense for trials (no plan to upgrade-on, and
	// we hard-block at exactly 100, not 150% of 100).
	if isTrial {
		if currentPct >= thresholdWarn {
			s.fireWarn(ctx, c, count, capLimit)
		}
		return nil
	}

	if currentPct >= thresholdWarn {
		s.fireWarn(ctx, c, count, capLimit)
	}
	if currentPct >= thresholdAlert {
		s.fireCSAlert(ctx, c, count, capLimit)
	}
	if currentPct >= thresholdBlock {
		// Sticky-flag the block timestamp for analytics/CS dashboards.
		// The actual enforcement happens at CheckCanCreate time off the
		// live count, so this flag is informational only.
		if _, err := s.clinics.MarkNoteCapBlocked(ctx, c.ID); err != nil {
			s.log.ErrorContext(ctx, "notecap: mark blocked failed",
				slog.String("clinic_id", c.ID.String()),
				slog.String("err", err.Error()),
			)
		}
	}
	return nil
}

// window resolves (cap, since, isTrial). Trial clinics use the
// hard-coded 100 / clinics.created_at window. Active clinics use
// plan.NoteCap / billing_period_start, with a created_at fallback if
// the period start hasn't been written yet (webhook race).
func (s *Service) window(c ClinicState) (capLimit int, since time.Time, isTrial bool) {
	if c.Status == domain.ClinicStatusTrial {
		return trialNoteCap, c.CreatedAt, true
	}

	plan, ok := planNoteCap(c.PlanCode)
	if !ok {
		return 0, time.Time{}, false
	}
	start := c.CreatedAt
	if c.BillingPeriodStart != nil {
		start = *c.BillingPeriodStart
	}
	return plan, start, false
}

// fireWarn claims the warned-at flag and, on success, sends the 80%
// email. Claim-and-send pattern guarantees idempotency across
// concurrent note creates — only one process actually mails.
func (s *Service) fireWarn(ctx context.Context, c ClinicState, count, capLimit int) {
	if c.NoteCapWarnedAt != nil {
		return // already fired this period
	}
	claimed, err := s.clinics.MarkNoteCapWarned(ctx, c.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "notecap: mark warned failed",
			slog.String("clinic_id", c.ID.String()),
			slog.String("err", err.Error()),
		)
		return
	}
	if !claimed {
		return
	}
	if c.AdminEmail == "" {
		s.log.WarnContext(ctx, "notecap: cannot send warning, no admin email",
			slog.String("clinic_id", c.ID.String()),
		)
		return
	}
	if err := s.mail.SendNoteCapWarning(ctx, c.AdminEmail, c.Name, count, capLimit); err != nil {
		s.log.ErrorContext(ctx, "notecap: warn email failed",
			slog.String("clinic_id", c.ID.String()),
			slog.String("err", err.Error()),
		)
	}
}

// fireCSAlert is the same claim-and-send dance, targeted at the ops
// inbox. Disabled when opsEmail is empty.
func (s *Service) fireCSAlert(ctx context.Context, c ClinicState, count, capLimit int) {
	if c.NoteCapCSAlertedAt != nil {
		return
	}
	if s.opsEmail == "" {
		return
	}
	claimed, err := s.clinics.MarkNoteCapCSAlerted(ctx, c.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "notecap: mark cs alerted failed",
			slog.String("clinic_id", c.ID.String()),
			slog.String("err", err.Error()),
		)
		return
	}
	if !claimed {
		return
	}
	plan := ""
	if c.PlanCode != nil {
		plan = string(*c.PlanCode)
	}
	if err := s.mail.SendNoteCapCSAlert(ctx, s.opsEmail, c.ID.String(), c.Name, count, capLimit, plan); err != nil {
		s.log.ErrorContext(ctx, "notecap: cs alert email failed",
			slog.String("clinic_id", c.ID.String()),
			slog.String("err", err.Error()),
		)
	}
}

// planNoteCap resolves a *PlanCode → cap. Returns (0,false) for unknown
// / nil codes so the call site can fail open without further guards.
func planNoteCap(code *domain.PlanCode) (int, bool) {
	if code == nil {
		return 0, false
	}
	plan, ok := domain.PlanFor(*code)
	if !ok {
		return 0, false
	}
	if plan.NoteCap <= 0 {
		return 0, false
	}
	return plan.NoteCap, true
}

// pct returns count*100/cap with cap>0 guaranteed by the caller.
func pct(count, capLimit int) int {
	return count * hundredPercent / capLimit
}
