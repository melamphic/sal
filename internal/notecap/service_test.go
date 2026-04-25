package notecap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Test fakes ────────────────────────────────────────────────────────────────

type fakeClinics struct {
	mu          sync.Mutex
	state       ClinicState
	loadErr     error
	warnedAt    *time.Time
	csAlertedAt *time.Time
	blockedAt   *time.Time
	warnCalls   int
	csCalls     int
	blockCalls  int
}

func (f *fakeClinics) LoadForCap(_ context.Context, _ uuid.UUID) (ClinicState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return ClinicState{}, f.loadErr
	}
	st := f.state
	st.NoteCapWarnedAt = f.warnedAt
	st.NoteCapCSAlertedAt = f.csAlertedAt
	st.NoteCapBlockedAt = f.blockedAt
	return st, nil
}

func (f *fakeClinics) MarkNoteCapWarned(_ context.Context, _ uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.warnCalls++
	if f.warnedAt != nil {
		return false, nil
	}
	now := time.Now().UTC()
	f.warnedAt = &now
	return true, nil
}

func (f *fakeClinics) MarkNoteCapCSAlerted(_ context.Context, _ uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.csCalls++
	if f.csAlertedAt != nil {
		return false, nil
	}
	now := time.Now().UTC()
	f.csAlertedAt = &now
	return true, nil
}

func (f *fakeClinics) MarkNoteCapBlocked(_ context.Context, _ uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockCalls++
	if f.blockedAt != nil {
		return false, nil
	}
	now := time.Now().UTC()
	f.blockedAt = &now
	return true, nil
}

type fakeNotes struct {
	count int
	err   error
}

func (f *fakeNotes) CountSinceForClinic(_ context.Context, _ uuid.UUID, _ time.Time) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

type fakeMail struct {
	mu       sync.Mutex
	warnSent int
	csSent   int
	warnErr  error
	csErr    error
}

func (f *fakeMail) SendNoteCapWarning(_ context.Context, _, _ string, _, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.warnSent++
	return f.warnErr
}

func (f *fakeMail) SendNoteCapCSAlert(_ context.Context, _, _, _ string, _, _ int, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.csSent++
	return f.csErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func activeClinicState(plan domain.PlanCode, periodStart time.Time) ClinicState {
	pc := plan
	return ClinicState{
		ID:                 uuid.New(),
		Name:               "Acme Vet",
		AdminEmail:         "admin@acme.test",
		Status:             domain.ClinicStatusActive,
		PlanCode:           &pc,
		BillingPeriodStart: &periodStart,
		CreatedAt:          periodStart.AddDate(0, -3, 0),
	}
}

func trialClinicState(createdAt time.Time) ClinicState {
	return ClinicState{
		ID:         uuid.New(),
		Name:       "Trial Vet",
		AdminEmail: "trial@acme.test",
		Status:     domain.ClinicStatusTrial,
		CreatedAt:  createdAt,
	}
}

// ── CheckCanCreate ───────────────────────────────────────────────────────────

func TestCheckCanCreate_ActiveBelowBlock_AllowsCreate(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	// Paws Practice cap is 1500; 2249 = 149.93% < 150% block
	notes := &fakeNotes{count: 2249}
	svc := NewService(clinics, notes, &fakeMail{}, "ops@example.com", discardLogger())

	if err := svc.CheckCanCreate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("expected nil err under block threshold, got: %v", err)
	}
}

func TestCheckCanCreate_ActiveAtBlock_ReturnsErrCapExceeded(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	// 1500 cap → 2250 = exactly 150%, must block
	notes := &fakeNotes{count: 2250}
	svc := NewService(clinics, notes, &fakeMail{}, "ops@example.com", discardLogger())

	err := svc.CheckCanCreate(context.Background(), uuid.New())
	if !errors.Is(err, ErrNoteCapExceeded) {
		t.Fatalf("expected ErrNoteCapExceeded, got: %v", err)
	}
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ErrNoteCapExceeded must wrap domain.ErrForbidden")
	}
}

func TestCheckCanCreate_TrialBelowCap_AllowsCreate(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: trialClinicState(time.Now().AddDate(0, 0, -3))}
	notes := &fakeNotes{count: 99}
	svc := NewService(clinics, notes, &fakeMail{}, "", discardLogger())

	if err := svc.CheckCanCreate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("trial 99 notes: expected nil, got %v", err)
	}
}

func TestCheckCanCreate_TrialAtCap_Blocks(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: trialClinicState(time.Now().AddDate(0, 0, -3))}
	notes := &fakeNotes{count: 100}
	svc := NewService(clinics, notes, &fakeMail{}, "", discardLogger())

	err := svc.CheckCanCreate(context.Background(), uuid.New())
	if !errors.Is(err, ErrTrialNoteCapExceeded) {
		t.Fatalf("expected ErrTrialNoteCapExceeded, got: %v", err)
	}
}

func TestCheckCanCreate_UnknownPlan_FailsOpen(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	state := activeClinicState(domain.PlanCode("nonsense_plan"), periodStart)
	clinics := &fakeClinics{state: state}
	notes := &fakeNotes{count: 999_999}
	svc := NewService(clinics, notes, &fakeMail{}, "ops@example.com", discardLogger())

	if err := svc.CheckCanCreate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("unknown plan should fail open, got: %v", err)
	}
}

func TestCheckCanCreate_LoadError_BubblesUp(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{loadErr: errors.New("db boom")}
	svc := NewService(clinics, &fakeNotes{}, &fakeMail{}, "", discardLogger())

	if err := svc.CheckCanCreate(context.Background(), uuid.New()); err == nil {
		t.Fatalf("expected error from load failure, got nil")
	}
}

// ── Evaluate cascade ─────────────────────────────────────────────────────────

func TestEvaluate_BelowWarn_NoEmails(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{count: 1199} // 79% of 1500
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if mail.warnSent != 0 || mail.csSent != 0 {
		t.Fatalf("no emails expected at 79%%, got warn=%d cs=%d", mail.warnSent, mail.csSent)
	}
}

func TestEvaluate_AtWarn_FiresWarningOnce(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{count: 1200} // exactly 80% of 1500
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	for i := 0; i < 5; i++ {
		if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
			t.Fatalf("evaluate iter %d: %v", i, err)
		}
	}
	if mail.warnSent != 1 {
		t.Fatalf("expected 1 warn email across 5 evaluations, got %d", mail.warnSent)
	}
	if mail.csSent != 0 {
		t.Fatalf("expected 0 cs alerts at 80%%, got %d", mail.csSent)
	}
	if clinics.warnCalls < 1 {
		t.Fatalf("expected MarkNoteCapWarned to be called at least once, got %d", clinics.warnCalls)
	}
}

func TestEvaluate_AtAlert_FiresWarningAndCSAlert(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{count: 1650} // exactly 110% of 1500
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if mail.warnSent != 1 {
		t.Fatalf("expected 1 warn email at 110%%, got %d", mail.warnSent)
	}
	if mail.csSent != 1 {
		t.Fatalf("expected 1 cs alert at 110%%, got %d", mail.csSent)
	}
}

func TestEvaluate_AtBlock_StampsBlockedFlag(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{count: 2400} // 160% of 1500
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if clinics.blockCalls != 1 {
		t.Fatalf("expected MarkNoteCapBlocked called once, got %d", clinics.blockCalls)
	}
}

func TestEvaluate_AlreadyWarned_DoesNotResend(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	already := time.Now().Add(-1 * time.Hour)
	clinics := &fakeClinics{
		state:    activeClinicState(domain.PlanPawsPracticeMonthly, periodStart),
		warnedAt: &already,
	}
	notes := &fakeNotes{count: 1300} // 86%
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if mail.warnSent != 0 {
		t.Fatalf("expected no resend when already warned, got %d", mail.warnSent)
	}
	if clinics.warnCalls != 0 {
		t.Fatalf("expected no MarkNoteCapWarned call when already warned, got %d", clinics.warnCalls)
	}
}

func TestEvaluate_TrialAt80Pct_FiresWarningOnly(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: trialClinicState(time.Now().AddDate(0, 0, -3))}
	notes := &fakeNotes{count: 80}
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if mail.warnSent != 1 {
		t.Fatalf("trial 80%%: expected 1 warning, got %d", mail.warnSent)
	}
	if mail.csSent != 0 {
		t.Fatalf("trial 80%%: expected no CS alert, got %d", mail.csSent)
	}
}

func TestEvaluate_NoOpsEmail_SkipsCSAlert(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{count: 1650} // 110%
	mail := &fakeMail{}
	svc := NewService(clinics, notes, mail, "" /* opsEmail off */, discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if mail.warnSent != 1 {
		t.Fatalf("warn should still fire without opsEmail, got %d", mail.warnSent)
	}
	if mail.csSent != 0 {
		t.Fatalf("cs alert must not fire without opsEmail, got %d", mail.csSent)
	}
	if clinics.csCalls != 0 {
		t.Fatalf("MarkNoteCapCSAlerted must not be called without opsEmail, got %d", clinics.csCalls)
	}
}

func TestEvaluate_MailerError_DoesNotPropagate(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{count: 1200}
	mail := &fakeMail{warnErr: errors.New("smtp down")}
	svc := NewService(clinics, notes, mail, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err != nil {
		t.Fatalf("mailer errors must be swallowed, got: %v", err)
	}
}

func TestEvaluate_CountError_PropagatesUp(t *testing.T) {
	t.Parallel()
	periodStart := time.Now().Add(-72 * time.Hour)
	clinics := &fakeClinics{state: activeClinicState(domain.PlanPawsPracticeMonthly, periodStart)}
	notes := &fakeNotes{err: errors.New("db boom")}
	svc := NewService(clinics, notes, &fakeMail{}, "ops@example.com", discardLogger())

	if err := svc.Evaluate(context.Background(), uuid.New()); err == nil {
		t.Fatalf("count errors must propagate, got nil")
	}
}
