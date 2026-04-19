package marketplace

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Stripe Connect publisher onboarding ──────────────────────────────────────

// StartPublisherOnboarding creates a Stripe Connect Express account for the
// caller's publisher and returns a hosted onboarding URL. Idempotent when the
// publisher already has a Stripe account — returns a fresh Account Link.
func (s *Service) StartPublisherOnboarding(ctx context.Context, input StripeConnectOnboardingInput) (string, error) {
	if s.stripe == nil {
		return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: stripe not configured: %w", domain.ErrConflict)
	}

	publisher, err := s.repo.GetPublisherByID(ctx, input.PublisherID)
	if err != nil {
		return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: %w", err)
	}
	if publisher.ClinicID != input.ClinicID {
		return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: not owner: %w", domain.ErrForbidden)
	}
	if err := s.checkCanPublish(ctx, input.ClinicID); err != nil {
		return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: %w", err)
	}

	accountID := ""
	if publisher.StripeConnectAccountID != nil {
		accountID = *publisher.StripeConnectAccountID
	} else {
		newID, err := s.stripe.CreateConnectExpressAccount(ctx, input.Email, input.Country)
		if err != nil {
			return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: create: %w", err)
		}
		accountID = newID
		if err := s.repo.UpdatePublisherStripeConnect(ctx, publisher.ID, accountID, false); err != nil {
			return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: persist: %w", err)
		}
	}

	url, err := s.stripe.CreateConnectAccountLink(ctx, accountID, input.RefreshURL, input.ReturnURL)
	if err != nil {
		return "", fmt.Errorf("marketplace.service.StartPublisherOnboarding: link: %w", err)
	}
	return url, nil
}

// ── Purchase flow ────────────────────────────────────────────────────────────

// Purchase creates a pending acquisition + Stripe Payment Intent. Returns the
// client_secret for the Flutter client to confirm payment. On webhook receipt
// the acquisition transitions to 'active'.
//
// Revenue: application_fee = price_cents * fee_pct where fee_pct is 0 for
// salvia/authority publishers, 30 (default) otherwise.
func (s *Service) Purchase(ctx context.Context, input PurchaseInput) (*PurchaseResponse, error) {
	if s.stripe == nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: stripe not configured: %w", domain.ErrConflict)
	}

	listing, err := s.repo.GetListingByID(ctx, input.ListingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: %w", err)
	}
	if listing.Status != "published" {
		return nil, fmt.Errorf("marketplace.service.Purchase: listing not published: %w", domain.ErrNotFound)
	}
	if listing.PricingType != "paid" {
		return nil, fmt.Errorf("marketplace.service.Purchase: not a paid listing: %w", domain.ErrConflict)
	}
	if listing.PriceCents == nil || *listing.PriceCents <= 0 {
		return nil, fmt.Errorf("marketplace.service.Purchase: missing price: %w", domain.ErrConflict)
	}

	if err := s.checkCanAcquirePaid(ctx, input.ClinicID); err != nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: %w", err)
	}

	publisher, err := s.repo.GetPublisherByID(ctx, listing.PublisherAccountID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: publisher: %w", err)
	}
	if publisher.StripeConnectAccountID == nil || !publisher.StripeOnboardingComplete {
		return nil, fmt.Errorf("marketplace.service.Purchase: publisher not onboarded: %w", domain.ErrConflict)
	}

	version, err := s.repo.GetLatestVersion(ctx, listing.ID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: version: %w", err)
	}

	feePct := s.platformFeePctForPublisher(publisher)
	feeCents := (*listing.PriceCents * feePct) / 100

	acquisitionID := domain.NewID()

	clientSecret, piID, err := s.stripe.CreatePaymentIntent(ctx, StripePaymentIntentInput{
		AmountCents:         *listing.PriceCents,
		Currency:            listing.Currency,
		ApplicationFeeCents: feeCents,
		DestinationAccount:  *publisher.StripeConnectAccountID,
		Metadata: map[string]string{
			"acquisition_id": acquisitionID.String(),
			"listing_id":     listing.ID.String(),
			"version_id":     version.ID.String(),
			"clinic_id":      input.ClinicID.String(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: payment intent: %w", err)
	}

	if _, err := s.repo.CreateAcquisition(ctx, CreateAcquisitionParams{
		ID:                    acquisitionID,
		ListingID:             listing.ID,
		MarketplaceVersionID:  version.ID,
		ClinicID:              input.ClinicID,
		AcquiredBy:            input.StaffID,
		AcquisitionType:       "purchase",
		Status:                "pending",
		StripePaymentIntentID: &piID,
	}); err != nil {
		return nil, fmt.Errorf("marketplace.service.Purchase: acquisition: %w", err)
	}

	return &PurchaseResponse{
		ClientSecret:    clientSecret,
		PaymentIntentID: piID,
		AcquisitionID:   acquisitionID.String(),
		AmountCents:     *listing.PriceCents,
		Currency:        listing.Currency,
	}, nil
}

// ── Stripe webhook routing ───────────────────────────────────────────────────

// HandleStripeWebhook verifies the signature, dedupes, and dispatches the
// event to the right service method.
func (s *Service) HandleStripeWebhook(ctx context.Context, payload []byte, signature string) error {
	if s.stripe == nil {
		return fmt.Errorf("marketplace.service.HandleStripeWebhook: stripe not configured: %w", domain.ErrConflict)
	}
	evt, err := s.stripe.VerifyAndParseWebhook(payload, signature)
	if err != nil {
		return fmt.Errorf("marketplace.service.HandleStripeWebhook: verify: %w", err)
	}

	first, err := s.repo.MarkStripeEventProcessed(ctx, evt.ID, evt.Type)
	if err != nil {
		return fmt.Errorf("marketplace.service.HandleStripeWebhook: dedupe: %w", err)
	}
	if !first {
		return nil // already processed
	}

	switch evt.Type {
	case "payment_intent.succeeded":
		pi, ok := evt.PayloadV.(*StripePaymentIntent)
		if !ok || pi == nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: malformed payment_intent")
		}
		_, _, err := s.repo.FulfillAcquisitionByPaymentIntent(ctx,
			pi.ID,
			pi.AmountReceived,
			pi.ApplicationFeeAmt,
			pi.Currency,
			domain.TimeNow(),
		)
		if err != nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: fulfill: %w", err)
		}
		return nil
	case "charge.refunded":
		rf, ok := evt.PayloadV.(*StripeRefund)
		if !ok || rf == nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: malformed refund")
		}
		if _, err := s.repo.RefundAcquisitionByPaymentIntent(ctx, rf.PaymentIntentID); err != nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: refund: %w", err)
		}
		return nil
	case "account.updated":
		acc, ok := evt.PayloadV.(*StripeAccount)
		if !ok || acc == nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: malformed account")
		}
		publisher, err := s.repo.GetPublisherByStripeConnectAccountID(ctx, acc.ID)
		if err != nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: lookup publisher: %w", err)
		}
		if err := s.repo.UpdatePublisherStripeConnect(ctx, publisher.ID, acc.ID, acc.ChargesEnabled); err != nil {
			return fmt.Errorf("marketplace.service.HandleStripeWebhook: update publisher: %w", err)
		}
		return nil
	default:
		return nil // ignore unhandled events
	}
}

// compile-time shim: uuid referenced elsewhere but lint removes unused imports.
var _ = uuid.Nil
