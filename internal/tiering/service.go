// Package tiering keeps a clinic's Stripe subscription tier in sync
// with its `note_tier=standard` staff headcount per pricing-model-v3 §6:
//
//	1–3 standard clinicians → Practice
//	4+ standard clinicians  → Pro
//
// Triggered by staff.Service after every invite/create/deactivate that
// touches a standard seat, via the staff.TierReconciler port. The
// reconcile path issues a Stripe subscription-item swap; the resulting
// `customer.subscription.updated` webhook then persists the new
// plan_code on the clinic row through billing.HandleWebhook.
//
// tiering is a leaf cross-domain coordinator — it imports nothing from
// other domains directly. Callers in app.go wire it via thin adapters.
package tiering

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ClinicState is the read-only slice of clinic state tiering needs.
type ClinicState struct {
	Status               domain.ClinicStatus
	PlanCode             *domain.PlanCode
	StripeSubscriptionID *string
}

// ClinicReader is the cross-domain port to the clinic module.
type ClinicReader interface {
	LoadTierState(ctx context.Context, clinicID uuid.UUID) (ClinicState, error)
}

// StaffCounter is the cross-domain port to the staff module — counts
// active+invited staff with note_tier=standard.
type StaffCounter interface {
	CountStandardActive(ctx context.Context, clinicID uuid.UUID) (int, error)
}

// SubscriptionUpdater is the cross-domain port to the billing module —
// swaps the subscription's price item to a new price id.
type SubscriptionUpdater interface {
	UpdateSubscriptionPlan(ctx context.Context, subscriptionID, newPriceID string) error
}

// PriceLookup resolves a Salvia plan_code to its Stripe price id.
// Implemented by the static plan map wired in app.go.
type PriceLookup interface {
	StripePriceIDForPlanCode(planCode domain.PlanCode) (string, bool)
}

// enterpriseHeadcountThreshold mirrors pricing-model-v3 §4: 8+ standard
// clinicians falls into the Enterprise band, which is sold via Contact
// Sales rather than auto-derived. We log past this floor so CS picks
// up the upgrade conversation; we don't transition the plan
// automatically because Enterprise is custom-priced.
const enterpriseHeadcountThreshold = 8

// Service is the public API of the tiering module — implements
// staff.TierReconciler. Construct via NewService.
type Service struct {
	clinics ClinicReader
	staff   StaffCounter
	billing SubscriptionUpdater
	prices  PriceLookup
	log     *slog.Logger
}

// NewService constructs a Service. Any nil dependency is a wiring bug;
// the caller in app.go is responsible for providing all four.
func NewService(c ClinicReader, st StaffCounter, b SubscriptionUpdater, p PriceLookup, log *slog.Logger) *Service {
	return &Service{clinics: c, staff: st, billing: b, prices: p, log: log}
}

// Reconcile compares the clinic's current Stripe plan tier against the
// expected tier given its `note_tier=standard` headcount, and swaps the
// subscription's price item if they differ. Best-effort: Stripe API
// errors are logged but swallowed so a Stripe blip can't fail the staff
// mutation that triggered us. Only DB-load / count errors propagate —
// those should 500 the underlying request.
func (s *Service) Reconcile(ctx context.Context, clinicID uuid.UUID) error {
	state, err := s.clinics.LoadTierState(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("tiering.service.Reconcile: load: %w", err)
	}

	// Only reconcile actively-billed clinics. Trial / past-due / grace /
	// cancelled clinics are off-limits — sales/CS handles those manually,
	// and Stripe would reject item swaps on a non-active sub anyway.
	if state.Status != domain.ClinicStatusActive {
		return nil
	}
	if state.PlanCode == nil {
		return nil
	}
	if state.StripeSubscriptionID == nil || *state.StripeSubscriptionID == "" {
		return nil
	}

	plan, ok := domain.PlanFor(*state.PlanCode)
	if !ok {
		s.log.WarnContext(ctx, "tiering: skipping reconcile, unknown plan_code",
			slog.String("clinic_id", clinicID.String()),
			slog.String("plan_code", string(*state.PlanCode)),
		)
		return nil
	}

	count, err := s.staff.CountStandardActive(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("tiering.service.Reconcile: count staff: %w", err)
	}

	expectedTier, ok := domain.DeriveTierFromHeadcount(count)
	if !ok {
		// No standard clinicians at all — leave the plan alone. The
		// clinic might have been provisioned with no clinicians yet, or
		// just deactivated their last one; either way, sales handles
		// the conversation manually rather than auto-downgrading.
		return nil
	}

	// Enterprise-eligible signal — pricing-model-v3 §1 + §14: 8+
	// standard clinicians falls into Contact Sales, but
	// DeriveTierFromHeadcount silently returns Pro for any count of 4
	// or more. Without this hook the clinic could ride Pro forever
	// without a CS upgrade conversation. We emit a structured WARN log
	// rather than transition the plan automatically — Enterprise
	// pricing is custom (volume + SSO + dedicated onboarding) and
	// must come from a human-led conversation. CS dashboards filter
	// on this event name to surface the queue.
	if count >= enterpriseHeadcountThreshold {
		s.log.WarnContext(ctx, "tiering.enterprise_eligible",
			slog.String("clinic_id", clinicID.String()),
			slog.Int("standard_headcount", count),
			slog.String("current_plan_code", string(*state.PlanCode)),
			slog.String("current_tier", string(plan.Tier)),
			slog.String("product", string(plan.Product)),
		)
	}

	if expectedTier == plan.Tier {
		return nil
	}

	newCode, ok := domain.PlanCodeFor(plan.Product, expectedTier, plan.Cycle)
	if !ok {
		s.log.WarnContext(ctx, "tiering: no plan registered for derived tier",
			slog.String("clinic_id", clinicID.String()),
			slog.String("product", string(plan.Product)),
			slog.String("tier", string(expectedTier)),
			slog.String("cycle", string(plan.Cycle)),
		)
		return nil
	}

	newPriceID, ok := s.prices.StripePriceIDForPlanCode(newCode)
	if !ok {
		s.log.WarnContext(ctx, "tiering: no stripe price mapped for plan_code",
			slog.String("clinic_id", clinicID.String()),
			slog.String("plan_code", string(newCode)),
		)
		return nil
	}

	if err := s.billing.UpdateSubscriptionPlan(ctx, *state.StripeSubscriptionID, newPriceID); err != nil {
		// Best-effort: don't fail the staff mutation on a Stripe blip.
		// The next invite/create/deactivate will retry; CS dashboards
		// surface clinics whose plan_code lags their headcount.
		s.log.ErrorContext(ctx, "tiering: stripe update failed",
			slog.String("clinic_id", clinicID.String()),
			slog.String("subscription_id", *state.StripeSubscriptionID),
			slog.String("new_plan_code", string(newCode)),
			slog.String("err", err.Error()),
		)
	}
	return nil
}
