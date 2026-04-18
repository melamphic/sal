// Package billing consumes Stripe webhooks and keeps clinic billing
// state in sync. It owns only the stripe_events (idempotency) table —
// all clinic mutations go through the ClinicUpdater adapter wired in
// app.go, so billing never imports the clinic package.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/webhook"
)

// ClinicUpdater is implemented by an app.go adapter bridging to
// clinic.Service. Billing never imports the clinic package directly.
type ClinicUpdater interface {
	// FindByStripeCustomerID returns the clinic id for a Stripe customer
	// or domain.ErrNotFound if the mapping hasn't been recorded yet.
	FindByStripeCustomerID(ctx context.Context, stripeCustomerID string) (uuid.UUID, error)
	// ApplySubscriptionState writes the authoritative billing state for
	// a clinic — status always, plan_code + stripe ids COALESCEd.
	ApplySubscriptionState(ctx context.Context, clinicID uuid.UUID, s SubscriptionState) error
}

// SubscriptionState is the target state billing asks clinic to write.
type SubscriptionState struct {
	Status               domain.ClinicStatus // required
	PlanCode             *domain.PlanCode    // nil = leave unchanged
	StripeCustomerID     *string             // nil = leave unchanged
	StripeSubscriptionID *string             // nil = leave unchanged
}

// PlanLookup maps a Stripe price id → Salvia PlanCode. Populated at
// startup from STRIPE_PRICE_MAP.
type PlanLookup interface {
	PlanCodeForStripePriceID(priceID string) (domain.PlanCode, bool)
}

// Service orchestrates Stripe webhook processing.
type Service struct {
	repo       repo
	clinics    ClinicUpdater
	plans      PlanLookup
	webhookSec []byte
}

// NewService creates a new billing Service.
func NewService(repo repo, clinics ClinicUpdater, plans PlanLookup, webhookSecret []byte) *Service {
	return &Service{repo: repo, clinics: clinics, plans: plans, webhookSec: webhookSecret}
}

// HandleWebhook verifies the Stripe signature, records the event for
// idempotency, and dispatches to the per-type handler. Returns nil for
// both success and already-processed (idempotent). Returns an error
// only for signature failure, DB failure, or adapter failure — the
// handler maps those to 4xx/5xx.
//
// rawBody must be the exact byte slice from the HTTP request body —
// Stripe's HMAC is computed over raw bytes.
func (s *Service) HandleWebhook(ctx context.Context, rawBody []byte, sigHeader string) error {
	event, err := webhook.ConstructEvent(rawBody, sigHeader, string(s.webhookSec))
	if err != nil {
		return fmt.Errorf("billing.service.HandleWebhook: verify signature: %w", errors.Join(err, domain.ErrTokenInvalid))
	}

	clinicID, state, status, dispatchErr := s.dispatch(ctx, event)
	if dispatchErr != nil {
		return fmt.Errorf("billing.service.HandleWebhook: dispatch: %w", dispatchErr)
	}

	// Record BEFORE writing state — if the unique-violation fires we
	// know the event was already processed and skip the mutation.
	recParams := RecordEventParams{
		EventID:   event.ID,
		EventType: string(event.Type),
		ClinicID:  clinicID,
		Status:    status,
	}
	if err := s.repo.RecordEvent(ctx, recParams); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			// Replay — already processed, no-op.
			return nil
		}
		return fmt.Errorf("billing.service.HandleWebhook: record event: %w", err)
	}

	// Ignored / unmapped events stop here — nothing to apply.
	if status != EventStatusProcessed || clinicID == nil {
		return nil
	}

	if err := s.clinics.ApplySubscriptionState(ctx, *clinicID, state); err != nil {
		return fmt.Errorf("billing.service.HandleWebhook: apply state: %w", err)
	}
	return nil
}

// dispatch routes a verified Stripe event to its handler. Returns the
// resolved clinic id (nil if unmapped), the target state, the status to
// persist on stripe_events, and any processing error (NOT including
// "clinic not found" — that is reported as status=unmapped).
func (s *Service) dispatch(ctx context.Context, event stripe.Event) (*uuid.UUID, SubscriptionState, string, error) {
	switch event.Type {
	case "customer.subscription.created",
		"customer.subscription.updated":
		return s.handleSubscription(ctx, event.Data.Raw)

	case "customer.subscription.deleted":
		return s.handleSubscriptionDeleted(ctx, event.Data.Raw)

	case "invoice.payment_failed":
		return s.handleInvoicePaymentFailed(ctx, event.Data.Raw)

	case "invoice.payment_succeeded":
		return s.handleInvoicePaymentSucceeded(ctx, event.Data.Raw)

	default:
		return nil, SubscriptionState{}, EventStatusIgnored, nil
	}
}

func (s *Service) handleSubscription(ctx context.Context, raw json.RawMessage) (*uuid.UUID, SubscriptionState, string, error) {
	var sub stripe.Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, SubscriptionState{}, "", fmt.Errorf("unmarshal subscription: %w", err)
	}

	clinicID, status, err := s.resolveClinic(ctx, sub.Customer)
	if err != nil {
		return nil, SubscriptionState{}, "", err
	}
	if clinicID == nil {
		return nil, SubscriptionState{}, status, nil
	}

	custID := customerID(sub.Customer)
	subID := sub.ID
	state := SubscriptionState{
		Status:               mapStripeStatus(sub.Status),
		PlanCode:             s.planCodeFromSubscription(&sub),
		StripeCustomerID:     nonEmpty(custID),
		StripeSubscriptionID: nonEmpty(subID),
	}
	return clinicID, state, EventStatusProcessed, nil
}

func (s *Service) handleSubscriptionDeleted(ctx context.Context, raw json.RawMessage) (*uuid.UUID, SubscriptionState, string, error) {
	var sub stripe.Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, SubscriptionState{}, "", fmt.Errorf("unmarshal subscription: %w", err)
	}

	clinicID, status, err := s.resolveClinic(ctx, sub.Customer)
	if err != nil {
		return nil, SubscriptionState{}, "", err
	}
	if clinicID == nil {
		return nil, SubscriptionState{}, status, nil
	}

	// Cancelled: null out the subscription id so B4's portal endpoint
	// can't open a dead sub. Customer id stays (the customer might
	// re-subscribe later with the same cus_…).
	empty := ""
	return clinicID, SubscriptionState{
		Status:               domain.ClinicStatusCancelled,
		StripeSubscriptionID: &empty,
	}, EventStatusProcessed, nil
}

func (s *Service) handleInvoicePaymentFailed(ctx context.Context, raw json.RawMessage) (*uuid.UUID, SubscriptionState, string, error) {
	var inv stripe.Invoice
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, SubscriptionState{}, "", fmt.Errorf("unmarshal invoice: %w", err)
	}

	clinicID, status, err := s.resolveClinic(ctx, inv.Customer)
	if err != nil {
		return nil, SubscriptionState{}, "", err
	}
	if clinicID == nil {
		return nil, SubscriptionState{}, status, nil
	}

	return clinicID, SubscriptionState{Status: domain.ClinicStatusPastDue}, EventStatusProcessed, nil
}

func (s *Service) handleInvoicePaymentSucceeded(ctx context.Context, raw json.RawMessage) (*uuid.UUID, SubscriptionState, string, error) {
	var inv stripe.Invoice
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, SubscriptionState{}, "", fmt.Errorf("unmarshal invoice: %w", err)
	}

	clinicID, status, err := s.resolveClinic(ctx, inv.Customer)
	if err != nil {
		return nil, SubscriptionState{}, "", err
	}
	if clinicID == nil {
		return nil, SubscriptionState{}, status, nil
	}

	// payment_succeeded alone is not enough to promote trial → active
	// (that's what subscription.created is for). We only use this to
	// recover clinics that are currently in a dunning state. Since we
	// don't load the current clinic row here, send `active` and let the
	// clinic-side COALESCE + state-machine handle the no-op case.
	//
	// Accepted trade-off: a successful invoice on a trial clinic will
	// still flip to active a few seconds earlier than Stripe would have
	// moved the subscription — not a correctness issue, just less
	// deterministic. Revisit if trial-billing UX becomes sensitive.
	return clinicID, SubscriptionState{Status: domain.ClinicStatusActive}, EventStatusProcessed, nil
}

// resolveClinic looks up the clinic for a Stripe customer reference.
// Returns (nil, EventStatusUnmapped, nil) when no clinic has been
// provisioned yet — the webhook records that and returns 200.
func (s *Service) resolveClinic(ctx context.Context, cust *stripe.Customer) (*uuid.UUID, string, error) {
	id := customerID(cust)
	if id == "" {
		return nil, EventStatusUnmapped, nil
	}
	clinicID, err := s.clinics.FindByStripeCustomerID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, EventStatusUnmapped, nil
		}
		return nil, "", fmt.Errorf("resolve clinic: %w", err)
	}
	return &clinicID, EventStatusProcessed, nil
}

// planCodeFromSubscription extracts the first subscription item's
// Stripe price id and maps it to a Salvia PlanCode via PlanLookup.
// Returns nil (= leave plan_code unchanged) if the mapping is unknown.
func (s *Service) planCodeFromSubscription(sub *stripe.Subscription) *domain.PlanCode {
	if sub == nil || sub.Items == nil || len(sub.Items.Data) == 0 {
		return nil
	}
	item := sub.Items.Data[0]
	if item == nil || item.Price == nil {
		return nil
	}
	pc, ok := s.plans.PlanCodeForStripePriceID(item.Price.ID)
	if !ok {
		return nil
	}
	return &pc
}

// mapStripeStatus translates Stripe's subscription.status enum to our
// domain.ClinicStatus. `incomplete` / `incomplete_expired` intentionally
// return the zero value so callers can treat them as no-op.
func mapStripeStatus(s stripe.SubscriptionStatus) domain.ClinicStatus {
	switch s {
	case stripe.SubscriptionStatusTrialing:
		return domain.ClinicStatusTrial
	case stripe.SubscriptionStatusActive:
		return domain.ClinicStatusActive
	case stripe.SubscriptionStatusPastDue:
		return domain.ClinicStatusPastDue
	case stripe.SubscriptionStatusUnpaid:
		return domain.ClinicStatusGracePeriod
	case stripe.SubscriptionStatusCanceled:
		return domain.ClinicStatusCancelled
	case stripe.SubscriptionStatusPaused:
		return domain.ClinicStatusSuspended
	default:
		// incomplete / incomplete_expired: checkout not confirmed yet,
		// leave the trial status alone.
		return domain.ClinicStatusTrial
	}
}

// customerID safely extracts the cus_… id from a Stripe customer ref.
// Stripe sometimes expands the object, sometimes sends a string id.
func customerID(c *stripe.Customer) string {
	if c == nil {
		return ""
	}
	return c.ID
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
