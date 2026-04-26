package auth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// signupCheckoutHandoffTTL is how long the handoff JWT minted by the
// signup-checkout flow is valid. Longer than the trial-only signup TTL
// (10 min) because the user has to round-trip through Stripe Checkout
// — typing card details, possibly grabbing their wallet — before the
// success_url redirects back into Salvia.
const signupCheckoutHandoffTTL = 30 * time.Minute

// SignupCheckoutClient is implemented by an app.go adapter wrapping the
// billing module. Auth must not import billing directly (CLAUDE.md).
type SignupCheckoutClient interface {
	// CreateCustomer provisions a Stripe customer for a not-yet-created
	// clinic and returns its cus_… id. The customer carries email + name
	// so it surfaces in the Stripe dashboard the same as portal-created
	// customers; metadata.clinic_id is omitted (no row to point at yet).
	CreateCustomer(email, clinicName string) (string, error)
	// CreateCheckoutSession creates a Stripe Checkout session in
	// subscription mode with the configured trial period and returns
	// its hosted URL.
	CreateCheckoutSession(p SignupCheckoutSessionInput) (string, error)
	// PriceIDForPlanCode resolves a Salvia plan_code to its Stripe
	// price id — needed before we can build a Checkout line item.
	PriceIDForPlanCode(planCode domain.PlanCode) (string, bool)
}

// SignupCheckoutSessionInput is the input the auth service hands to the
// adapter. Mirrors the subset of billing.CheckoutParams the adapter uses
// without exposing billing's struct directly.
type SignupCheckoutSessionInput struct {
	CustomerID string
	PriceID    string
	SuccessURL string
	CancelURL  string
	TrialDays  int64
}

// EnableSignupCheckout wires the signup-checkout flow. Pass nil to keep
// it disabled (StartSignupCheckout then returns domain.ErrUnauthorized).
// cancelURL is the absolute /mel URL the user lands on if they abandon
// Stripe Checkout — typically `${MEL_BASE_URL}/signup?canceled=1`.
func (s *Service) EnableSignupCheckout(client SignupCheckoutClient, cancelURL string) {
	s.signupCheckout = client
	s.signupCancelURL = cancelURL
}

// StartSignupCheckoutInput mirrors StartSignupInput but plan_code is
// required — there is no "checkout without a plan" flow.
type StartSignupCheckoutInput struct {
	Email      string
	FullName   string
	ClinicName string
	Vertical   domain.Vertical
	PlanCode   string
}

// StartSignupCheckoutResult is what the /mel signup form uses to bounce
// the browser into Stripe Checkout. After Stripe success, the browser
// lands on HandoffURL (which is also the Checkout success_url) — the
// JWT carries the cus_… so handoff provisioning persists it on the
// clinic row before the subscription webhook arrives.
type StartSignupCheckoutResult struct {
	CheckoutURL string
	HandoffURL  string
	ExpiresAt   time.Time
}

// StartSignupCheckout pre-creates a Stripe customer + Checkout session
// for a /mel signup, mints a handoff JWT containing the new cus_…, and
// returns the Checkout URL (to redirect the browser to) plus the handoff
// URL (which Stripe sends the browser to on success).
//
// Returns:
//   - domain.ErrUnauthorized when the feature is disabled (no handoff
//     secret, no checkout client, or AppURL not configured).
//   - domain.ErrTokenInvalid for unsupported vertical / unknown plan_code
//     / empty plan_code.
func (s *Service) StartSignupCheckout(_ context.Context, in StartSignupCheckoutInput) (*StartSignupCheckoutResult, error) {
	if s.handoffSecret == nil || s.signupCheckout == nil {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: %w", domain.ErrUnauthorized)
	}
	if s.cfg.AppURL == "" {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: app URL not configured")
	}

	switch in.Vertical {
	case domain.VerticalVeterinary, domain.VerticalDental, domain.VerticalGeneralClinic:
	default:
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: %w: unsupported vertical", domain.ErrTokenInvalid)
	}

	if strings.TrimSpace(in.PlanCode) == "" {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: %w: plan_code required", domain.ErrTokenInvalid)
	}
	planCode := domain.PlanCode(in.PlanCode)
	if _, ok := domain.PlanFor(planCode); !ok {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: %w: unknown plan_code", domain.ErrTokenInvalid)
	}
	priceID, ok := s.signupCheckout.PriceIDForPlanCode(planCode)
	if !ok {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: %w: plan_code has no Stripe price mapping", domain.ErrTokenInvalid)
	}

	custID, err := s.signupCheckout.CreateCustomer(in.Email, in.ClinicName)
	if err != nil {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: create customer: %w", err)
	}

	now := domain.TimeNow()
	exp := now.Add(signupCheckoutHandoffTTL)
	claims := MelHandoffClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Email:            in.Email,
		FullName:         in.FullName,
		ClinicName:       in.ClinicName,
		Vertical:         in.Vertical,
		PlanCode:         in.PlanCode,
		StripeCustomerID: custID,
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.handoffSecret)
	if err != nil {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: sign: %w", err)
	}

	u, err := url.Parse(strings.TrimRight(s.cfg.AppURL, "/") + "/auth/handoff")
	if err != nil {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: build handoff url: %w", err)
	}
	q := u.Query()
	q.Set("token", raw)
	q.Set("checkout", "success")
	u.RawQuery = q.Encode()
	handoffURL := u.String()

	// Default cancel back to the marketing signup page if no override
	// was wired — keeps dev environments useful when MEL_BASE_URL is
	// missing.
	cancelURL := s.signupCancelURL
	if cancelURL == "" {
		cancelURL = strings.TrimRight(s.cfg.AppURL, "/") + "/auth/signup-canceled"
	}

	checkoutURL, err := s.signupCheckout.CreateCheckoutSession(SignupCheckoutSessionInput{
		CustomerID: custID,
		PriceID:    priceID,
		SuccessURL: handoffURL,
		CancelURL:  cancelURL,
		TrialDays:  signupCheckoutTrialDays,
	})
	if err != nil {
		return nil, fmt.Errorf("auth.service.StartSignupCheckout: create checkout: %w", err)
	}

	return &StartSignupCheckoutResult{
		CheckoutURL: checkoutURL,
		HandoffURL:  handoffURL,
		ExpiresAt:   exp,
	}, nil
}

// signupCheckoutTrialDays is the trial length granted to signup-checkout
// subscriptions. Pricing model v3 §9 mandates 14 days.
const signupCheckoutTrialDays = 14
