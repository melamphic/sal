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
	portalsession "github.com/stripe/stripe-go/v78/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v78/checkout/session"
	"github.com/stripe/stripe-go/v78/customer"
	"github.com/stripe/stripe-go/v78/webhook"
)

// trialPeriodDays is the trial length offered when a clinic upgrades from
// the in-app Checkout flow. Pricing model v3 requires 14 days.
const trialPeriodDays = 14

// ClinicUpdater is implemented by an app.go adapter bridging to
// clinic.Service. Billing never imports the clinic package directly.
type ClinicUpdater interface {
	// FindByStripeCustomerID returns the clinic id for a Stripe customer
	// or domain.ErrNotFound if the mapping hasn't been recorded yet.
	FindByStripeCustomerID(ctx context.Context, stripeCustomerID string) (uuid.UUID, error)
	// ApplySubscriptionState writes the authoritative billing state for
	// a clinic — status always, plan_code + stripe ids COALESCEd.
	ApplySubscriptionState(ctx context.Context, clinicID uuid.UUID, s SubscriptionState) error
	// GetStripeCustomerID returns the cus_… id for a clinic, or nil if
	// the clinic is still on trial and has no Stripe customer yet.
	GetStripeCustomerID(ctx context.Context, clinicID uuid.UUID) (*string, error)
	// GetClinicProfile returns the clinic's billing-relevant identity
	// (email + display name) so a Stripe customer can be provisioned the
	// first time the clinic enters Checkout.
	GetClinicProfile(ctx context.Context, clinicID uuid.UUID) (ClinicProfile, error)
}

// ClinicProfile is the minimal slice of clinic identity billing needs to
// create a Stripe customer.
type ClinicProfile struct {
	Email string
	Name  string
}

// SubscriptionState is the target state billing asks clinic to write.
type SubscriptionState struct {
	Status               domain.ClinicStatus // required
	PlanCode             *domain.PlanCode    // nil = leave unchanged
	StripeCustomerID     *string             // nil = leave unchanged
	StripeSubscriptionID *string             // nil = leave unchanged
}

// PlanLookup maps Stripe price ids ↔ Salvia PlanCode. Populated at
// startup from STRIPE_PRICE_MAP. The reverse direction is needed when
// the FE asks Checkout for "this plan code" — we resolve to the price.
type PlanLookup interface {
	PlanCodeForStripePriceID(priceID string) (domain.PlanCode, bool)
	StripePriceIDForPlanCode(planCode domain.PlanCode) (string, bool)
}

// PortalSessionCreator creates a Stripe billing portal session and returns
// its hosted URL. Abstracted so tests can swap in a fake without hitting
// Stripe. Production uses stripePortalClient.
type PortalSessionCreator interface {
	Create(customerID, returnURL string) (string, error)
}

// CheckoutSessionCreator creates a Stripe Checkout session in subscription
// mode and returns its hosted URL. Abstracted so tests can swap in a fake.
type CheckoutSessionCreator interface {
	Create(p CheckoutParams) (string, error)
}

// CheckoutParams is the input handed to a CheckoutSessionCreator. The
// service fills it in from the clinic + plan_code; the implementation
// only needs to forward to Stripe.
type CheckoutParams struct {
	CustomerID string
	PriceID    string
	SuccessURL string
	CancelURL  string
	TrialDays  int64
	ClinicID   uuid.UUID
}

// StripeCustomerCreator creates a brand-new Stripe customer for a clinic
// and returns its cus_… id. Called the first time a clinic enters Checkout
// — afterwards the cus_… is persisted on the clinic and reused.
type StripeCustomerCreator interface {
	Create(email, name, clinicID string) (string, error)
}

// Service orchestrates Stripe webhook processing.
type Service struct {
	repo       repo
	clinics    ClinicUpdater
	plans      PlanLookup
	webhookSec []byte

	// Portal config — set via EnablePortal. Nil when STRIPE_API_KEY isn't
	// configured; CreatePortalSession returns domain.ErrValidation in that
	// case so callers know the feature is off.
	portal    PortalSessionCreator
	returnURL string

	// Checkout config — set via EnableCheckout. Both creators must be
	// non-nil for the feature to work; CreateCheckoutSession returns
	// domain.ErrValidation otherwise.
	checkout         CheckoutSessionCreator
	customerCreator  StripeCustomerCreator
	checkoutSuccess  string
	checkoutCancel   string
}

// NewService creates a new billing Service.
func NewService(repo repo, clinics ClinicUpdater, plans PlanLookup, webhookSecret []byte) *Service {
	return &Service{repo: repo, clinics: clinics, plans: plans, webhookSec: webhookSecret}
}

// EnablePortal wires the Stripe customer portal — pass nil / empty to
// leave it disabled.
func (s *Service) EnablePortal(portal PortalSessionCreator, returnURL string) {
	s.portal = portal
	s.returnURL = returnURL
}

// EnableCheckout wires Stripe Checkout (subscription mode). Pass nil
// creators to leave it disabled — CreateCheckoutSession returns
// domain.ErrValidation when off.
func (s *Service) EnableCheckout(
	checkout CheckoutSessionCreator,
	customers StripeCustomerCreator,
	successURL, cancelURL string,
) {
	s.checkout = checkout
	s.customerCreator = customers
	s.checkoutSuccess = successURL
	s.checkoutCancel = cancelURL
}

// CreatePortalSession returns a one-shot Stripe customer portal URL for the
// given clinic. The clinic must have a stripe_customer_id — trial clinics
// get domain.ErrValidation back so the handler can return 400.
func (s *Service) CreatePortalSession(ctx context.Context, clinicID uuid.UUID) (string, error) {
	if s.portal == nil {
		return "", fmt.Errorf("billing.service.CreatePortalSession: %w", domain.ErrValidation)
	}
	custID, err := s.clinics.GetStripeCustomerID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("billing.service.CreatePortalSession: %w", err)
	}
	if custID == nil || *custID == "" {
		return "", fmt.Errorf("billing.service.CreatePortalSession: no stripe customer: %w", domain.ErrValidation)
	}
	url, err := s.portal.Create(*custID, s.returnURL)
	if err != nil {
		return "", fmt.Errorf("billing.service.CreatePortalSession: %w", err)
	}
	return url, nil
}

// CreateCheckoutSession returns a one-shot Stripe Checkout URL for the
// given clinic + plan_code. The first time a clinic enters Checkout we
// pre-create a Stripe customer and persist the cus_… on the clinic so
// the eventual customer.subscription.created webhook resolves the clinic
// via the existing FindByStripeCustomerID path.
//
// Returns domain.ErrValidation when:
//   - the feature is disabled (STRIPE_API_KEY unset at boot), or
//   - the requested plan_code has no Stripe price mapping.
func (s *Service) CreateCheckoutSession(
	ctx context.Context,
	clinicID uuid.UUID,
	planCode domain.PlanCode,
) (string, error) {
	if s.checkout == nil || s.customerCreator == nil {
		return "", fmt.Errorf("billing.service.CreateCheckoutSession: %w", domain.ErrValidation)
	}
	priceID, ok := s.plans.StripePriceIDForPlanCode(planCode)
	if !ok {
		return "", fmt.Errorf("billing.service.CreateCheckoutSession: unknown plan_code %q: %w", planCode, domain.ErrValidation)
	}

	custID, err := s.clinics.GetStripeCustomerID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("billing.service.CreateCheckoutSession: %w", err)
	}
	if custID == nil || *custID == "" {
		profile, err := s.clinics.GetClinicProfile(ctx, clinicID)
		if err != nil {
			return "", fmt.Errorf("billing.service.CreateCheckoutSession: profile: %w", err)
		}
		newID, err := s.customerCreator.Create(profile.Email, profile.Name, clinicID.String())
		if err != nil {
			return "", fmt.Errorf("billing.service.CreateCheckoutSession: create customer: %w", err)
		}
		// Persist cus_… without touching status/plan — those stay whatever
		// the trial put them at until the subscription webhook lands.
		if err := s.clinics.ApplySubscriptionState(ctx, clinicID, SubscriptionState{
			Status:           domain.ClinicStatusTrial,
			StripeCustomerID: &newID,
		}); err != nil {
			return "", fmt.Errorf("billing.service.CreateCheckoutSession: persist customer id: %w", err)
		}
		custID = &newID
	}

	url, err := s.checkout.Create(CheckoutParams{
		CustomerID: *custID,
		PriceID:    priceID,
		SuccessURL: s.checkoutSuccess,
		CancelURL:  s.checkoutCancel,
		TrialDays:  trialPeriodDays,
		ClinicID:   clinicID,
	})
	if err != nil {
		return "", fmt.Errorf("billing.service.CreateCheckoutSession: %w", err)
	}
	return url, nil
}

// stripePortalClient is the production PortalSessionCreator backed by
// stripe-go's /v1/billing_portal/sessions endpoint.
type stripePortalClient struct {
	key string
}

// NewStripePortalClient builds a PortalSessionCreator using the given secret
// key. Safe to construct at startup with cfg.StripeAPIKey.
func NewStripePortalClient(stripeSecretKey string) PortalSessionCreator {
	return &stripePortalClient{key: stripeSecretKey}
}

func (c *stripePortalClient) Create(customerID, returnURL string) (string, error) {
	client := portalsession.Client{B: stripe.GetBackend(stripe.APIBackend), Key: c.key}
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	}
	sess, err := client.New(params)
	if err != nil {
		return "", fmt.Errorf("billing.stripePortalClient.Create: %w", err)
	}
	return sess.URL, nil
}

// stripeCheckoutClient is the production CheckoutSessionCreator.
type stripeCheckoutClient struct {
	key string
}

// NewStripeCheckoutClient builds a CheckoutSessionCreator using the given
// secret key. Safe to construct at startup with cfg.StripeAPIKey.
func NewStripeCheckoutClient(stripeSecretKey string) CheckoutSessionCreator {
	return &stripeCheckoutClient{key: stripeSecretKey}
}

func (c *stripeCheckoutClient) Create(p CheckoutParams) (string, error) {
	client := checkoutsession.Client{B: stripe.GetBackend(stripe.APIBackend), Key: c.key}
	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:   stripe.String(p.CustomerID),
		SuccessURL: stripe.String(p.SuccessURL),
		CancelURL:  stripe.String(p.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(p.PriceID), Quantity: stripe.Int64(1)},
		},
	}
	if p.TrialDays > 0 {
		params.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{
			TrialPeriodDays: stripe.Int64(p.TrialDays),
		}
	}
	params.AddMetadata("clinic_id", p.ClinicID.String())
	sess, err := client.New(params)
	if err != nil {
		return "", fmt.Errorf("billing.stripeCheckoutClient.Create: %w", err)
	}
	return sess.URL, nil
}

// stripeCustomerClient is the production StripeCustomerCreator. Backed
// by stripe-go's /v1/customers endpoint.
type stripeCustomerClient struct {
	key string
}

// NewStripeCustomerClient builds a StripeCustomerCreator using the given
// secret key.
func NewStripeCustomerClient(stripeSecretKey string) StripeCustomerCreator {
	return &stripeCustomerClient{key: stripeSecretKey}
}

func (c *stripeCustomerClient) Create(email, name, clinicID string) (string, error) {
	client := customer.Client{B: stripe.GetBackend(stripe.APIBackend), Key: c.key}
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	}
	params.AddMetadata("clinic_id", clinicID)
	cust, err := client.New(params)
	if err != nil {
		return "", fmt.Errorf("billing.stripeCustomerClient.Create: %w", err)
	}
	return cust.ID, nil
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
