package tiering

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Test fakes ────────────────────────────────────────────────────────────────

type fakeClinics struct {
	state   ClinicState
	loadErr error
}

func (f *fakeClinics) LoadTierState(_ context.Context, _ uuid.UUID) (ClinicState, error) {
	if f.loadErr != nil {
		return ClinicState{}, f.loadErr
	}
	return f.state, nil
}

type fakeStaff struct {
	count    int
	countErr error
}

func (f *fakeStaff) CountStandardActive(_ context.Context, _ uuid.UUID) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.count, nil
}

type fakeBilling struct {
	mu          sync.Mutex
	calls       int
	lastSubID   string
	lastPriceID string
	err         error
}

func (f *fakeBilling) UpdateSubscriptionPlan(_ context.Context, subID, priceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastSubID = subID
	f.lastPriceID = priceID
	return f.err
}

type fakePrices map[domain.PlanCode]string

func (f fakePrices) StripePriceIDForPlanCode(code domain.PlanCode) (string, bool) {
	id, ok := f[code]
	return id, ok
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func ptrPlan(c domain.PlanCode) *domain.PlanCode { return &c }
func ptrStr(s string) *string                    { return &s }

func newSvc(c *fakeClinics, st *fakeStaff, b *fakeBilling, p PriceLookup) *Service {
	return NewService(c, st, b, p, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// activeStateOnPaws returns a ClinicState for an active clinic on the
// given Paws plan with a Stripe sub id wired.
func activeStateOnPaws(code domain.PlanCode) ClinicState {
	return ClinicState{
		Status:               domain.ClinicStatusActive,
		PlanCode:             ptrPlan(code),
		StripeSubscriptionID: ptrStr("sub_test"),
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestService_Reconcile_NoOpWhenAlreadyOnExpectedTier(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{count: 2} // 1-3 → Practice
	billing := &fakeBilling{}
	prices := fakePrices{}

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("expected no Stripe update, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_PracticeToProOnGrowth(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{count: 4} // 4+ → Pro
	billing := &fakeBilling{}
	prices := fakePrices{domain.PlanPawsProMonthly: "price_pro_monthly"}

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 1 {
		t.Fatalf("expected 1 Stripe update, got %d", billing.calls)
	}
	if billing.lastSubID != "sub_test" {
		t.Fatalf("subID = %q, want %q", billing.lastSubID, "sub_test")
	}
	if billing.lastPriceID != "price_pro_monthly" {
		t.Fatalf("priceID = %q, want %q", billing.lastPriceID, "price_pro_monthly")
	}
}

func TestService_Reconcile_ProToPracticeOnShrink(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsProAnnual)}
	staff := &fakeStaff{count: 3} // shrunk back to Practice band
	billing := &fakeBilling{}
	prices := fakePrices{domain.PlanPawsPracticeAnnual: "price_practice_annual"}

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 1 {
		t.Fatalf("expected 1 Stripe update, got %d", billing.calls)
	}
	if billing.lastPriceID != "price_practice_annual" {
		t.Fatalf("priceID = %q, want price_practice_annual", billing.lastPriceID)
	}
}

func TestService_Reconcile_PreservesCycle(t *testing.T) {
	t.Parallel()
	// Annual Practice clinic grows past 3 — must move to Annual Pro,
	// not Monthly Pro.
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeAnnual)}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}
	prices := fakePrices{
		domain.PlanPawsProAnnual:  "price_pro_annual",
		domain.PlanPawsProMonthly: "price_pro_monthly",
	}

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.lastPriceID != "price_pro_annual" {
		t.Fatalf("priceID = %q, want price_pro_annual (cycle preserved)", billing.lastPriceID)
	}
}

func TestService_Reconcile_SkipsTrial(t *testing.T) {
	t.Parallel()
	state := activeStateOnPaws(domain.PlanPawsPracticeMonthly)
	state.Status = domain.ClinicStatusTrial
	clinics := &fakeClinics{state: state}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("trial clinics must skip reconcile, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_SkipsPastDue(t *testing.T) {
	t.Parallel()
	state := activeStateOnPaws(domain.PlanPawsPracticeMonthly)
	state.Status = domain.ClinicStatusPastDue
	clinics := &fakeClinics{state: state}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("past-due clinics must skip reconcile, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_NoOpWhenZeroClinicians(t *testing.T) {
	t.Parallel()
	// All standard staff deactivated — leave plan alone, sales handles it.
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsProMonthly)}
	staff := &fakeStaff{count: 0}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("zero-clinician clinic must not auto-downgrade, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_NoOpWhenNoSubscriptionID(t *testing.T) {
	t.Parallel()
	state := activeStateOnPaws(domain.PlanPawsPracticeMonthly)
	state.StripeSubscriptionID = nil
	clinics := &fakeClinics{state: state}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("no subscription id → no Stripe call, got %d", billing.calls)
	}
}

func TestService_Reconcile_NoOpWhenUnknownPlanCode(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: ClinicState{
		Status:               domain.ClinicStatusActive,
		PlanCode:             ptrPlan(domain.PlanCode("paws_legacy_monthly")),
		StripeSubscriptionID: ptrStr("sub_test"),
	}}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("unknown plan_code must skip, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_NoOpWhenPriceNotMapped(t *testing.T) {
	t.Parallel()
	// Tier change is needed but the new plan has no Stripe price wired.
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}
	prices := fakePrices{} // empty — no price for PawsProMonthly

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("missing price mapping must skip, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_StripeErrorSwallowed(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{err: errors.New("stripe blip")}
	prices := fakePrices{domain.PlanPawsProMonthly: "price_pro_monthly"}

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Stripe error must not propagate, got %v", err)
	}
	if billing.calls != 1 {
		t.Fatalf("expected 1 attempt, got %d", billing.calls)
	}
}

func TestService_Reconcile_LoadErrorPropagates(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{loadErr: errors.New("db down")}
	staff := &fakeStaff{count: 5}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err == nil {
		t.Fatalf("expected DB load error to propagate, got nil")
	}
}

func TestService_Reconcile_CountErrorPropagates(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{countErr: errors.New("db down")}
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err == nil {
		t.Fatalf("expected count error to propagate, got nil")
	}
}

func TestService_Reconcile_BoundaryAt3StaysPractice(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{count: 3} // exactly at upper Practice edge
	billing := &fakeBilling{}

	svc := newSvc(clinics, staff, billing, fakePrices{})
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 0 {
		t.Fatalf("count=3 must remain on Practice, got %d calls", billing.calls)
	}
}

func TestService_Reconcile_BoundaryAt4MovesToPro(t *testing.T) {
	t.Parallel()
	clinics := &fakeClinics{state: activeStateOnPaws(domain.PlanPawsPracticeMonthly)}
	staff := &fakeStaff{count: 4} // exactly at lower Pro edge
	billing := &fakeBilling{}
	prices := fakePrices{domain.PlanPawsProMonthly: "price_pro_monthly"}

	svc := newSvc(clinics, staff, billing, prices)
	if err := svc.Reconcile(context.Background(), uuid.New()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if billing.calls != 1 {
		t.Fatalf("count=4 must trigger Pro upgrade, got %d calls", billing.calls)
	}
}
