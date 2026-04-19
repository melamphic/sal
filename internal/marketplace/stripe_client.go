package marketplace

import (
	"context"
	"encoding/json"
	"fmt"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/account"
	"github.com/stripe/stripe-go/v82/accountlink"
	"github.com/stripe/stripe-go/v82/paymentintent"
	"github.com/stripe/stripe-go/v82/webhook"
)

// StripeSDKClient implements StripeClient against the real Stripe API.
// Construct via NewStripeSDKClient; pass nil secret key for disabled mode
// (service methods will return ErrConflict when stripe is required).
type StripeSDKClient struct {
	secretKey     string
	webhookSecret string
}

// NewStripeSDKClient builds a live Stripe client. An empty secretKey returns
// nil so callers can disable Stripe cleanly in dev.
func NewStripeSDKClient(secretKey, webhookSecret string) StripeClient {
	if secretKey == "" {
		return nil
	}
	stripe.Key = secretKey
	return &StripeSDKClient{secretKey: secretKey, webhookSecret: webhookSecret}
}

// CreateConnectExpressAccount provisions an Express Connect account.
func (c *StripeSDKClient) CreateConnectExpressAccount(ctx context.Context, email, country string) (string, error) {
	params := &stripe.AccountParams{
		Type:    stripe.String(string(stripe.AccountTypeExpress)),
		Email:   stripe.String(email),
		Country: stripe.String(country),
		Capabilities: &stripe.AccountCapabilitiesParams{
			Transfers: &stripe.AccountCapabilitiesTransfersParams{
				Requested: stripe.Bool(true),
			},
		},
	}
	params.Context = ctx
	acc, err := account.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe account new: %w", err)
	}
	return acc.ID, nil
}

// CreateConnectAccountLink returns a hosted onboarding URL.
func (c *StripeSDKClient) CreateConnectAccountLink(ctx context.Context, accountID, refreshURL, returnURL string) (string, error) {
	params := &stripe.AccountLinkParams{
		Account:    stripe.String(accountID),
		RefreshURL: stripe.String(refreshURL),
		ReturnURL:  stripe.String(returnURL),
		Type:       stripe.String(string(stripe.AccountLinkTypeAccountOnboarding)),
	}
	params.Context = ctx
	link, err := accountlink.New(params)
	if err != nil {
		return "", fmt.Errorf("stripe accountlink new: %w", err)
	}
	return link.URL, nil
}

// CreatePaymentIntent creates a destination-charge PaymentIntent.
func (c *StripeSDKClient) CreatePaymentIntent(ctx context.Context, input StripePaymentIntentInput) (string, string, error) {
	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(int64(input.AmountCents)),
		Currency: stripe.String(input.Currency),
		TransferData: &stripe.PaymentIntentTransferDataParams{
			Destination: stripe.String(input.DestinationAccount),
		},
		ApplicationFeeAmount: stripe.Int64(int64(input.ApplicationFeeCents)),
	}
	params.Context = ctx
	for k, v := range input.Metadata {
		params.AddMetadata(k, v)
	}
	pi, err := paymentintent.New(params)
	if err != nil {
		return "", "", fmt.Errorf("stripe paymentintent new: %w", err)
	}
	return pi.ClientSecret, pi.ID, nil
}

// VerifyAndParseWebhook validates Stripe-Signature and returns a normalised event.
func (c *StripeSDKClient) VerifyAndParseWebhook(payload []byte, signature string) (*StripeEvent, error) {
	evt, err := webhook.ConstructEvent(payload, signature, c.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("stripe webhook construct: %w", err)
	}
	out := &StripeEvent{ID: evt.ID, Type: string(evt.Type), RawJSON: evt.Data.Raw}
	switch evt.Type {
	case "payment_intent.succeeded":
		var pi stripe.PaymentIntent
		if err := json.Unmarshal(evt.Data.Raw, &pi); err != nil {
			return nil, fmt.Errorf("unmarshal payment_intent: %w", err)
		}
		meta := map[string]string{}
		for k, v := range pi.Metadata {
			meta[k] = v
		}
		out.PayloadV = &StripePaymentIntent{
			ID:                pi.ID,
			AmountReceived:    int(pi.AmountReceived),
			ApplicationFeeAmt: int(pi.ApplicationFeeAmount),
			Currency:          string(pi.Currency),
			Metadata:          meta,
		}
	case "charge.refunded":
		var ch stripe.Charge
		if err := json.Unmarshal(evt.Data.Raw, &ch); err != nil {
			return nil, fmt.Errorf("unmarshal charge: %w", err)
		}
		piID := ""
		if ch.PaymentIntent != nil {
			piID = ch.PaymentIntent.ID
		}
		out.PayloadV = &StripeRefund{PaymentIntentID: piID}
	case "account.updated":
		var acc stripe.Account
		if err := json.Unmarshal(evt.Data.Raw, &acc); err != nil {
			return nil, fmt.Errorf("unmarshal account: %w", err)
		}
		out.PayloadV = &StripeAccount{ID: acc.ID, ChargesEnabled: acc.ChargesEnabled}
	}
	return out, nil
}
