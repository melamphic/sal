package auth

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"

	"github.com/melamphic/sal/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSignupCheckout is a minimal SignupCheckoutClient that records inputs
// for assertions and returns canned outputs. Individual tests can override
// fields to simulate failures.
type fakeSignupCheckout struct {
	mu sync.Mutex

	customerCalls   int
	lastEmail       string
	lastClinicName  string
	customerID      string
	customerErr     error

	checkoutCalls int
	lastCheckout  SignupCheckoutSessionInput
	checkoutURL   string
	checkoutErr   error

	priceID string
	priceOK bool
}

func (f *fakeSignupCheckout) CreateCustomer(email, clinicName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customerCalls++
	f.lastEmail = email
	f.lastClinicName = clinicName
	if f.customerErr != nil {
		return "", f.customerErr
	}
	return f.customerID, nil
}

func (f *fakeSignupCheckout) CreateCheckoutSession(p SignupCheckoutSessionInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkoutCalls++
	f.lastCheckout = p
	if f.checkoutErr != nil {
		return "", f.checkoutErr
	}
	return f.checkoutURL, nil
}

func (f *fakeSignupCheckout) PriceIDForPlanCode(domain.PlanCode) (string, bool) {
	return f.priceID, f.priceOK
}

func newSignupCheckoutService(t *testing.T) (*Service, *fakeSignupCheckout) {
	t.Helper()
	svc, _, _ := newTestService(t)
	svc.SetMelHandoff(testHandoffSecret, &fakeHandoffProvisioner{})

	fc := &fakeSignupCheckout{
		customerID:  "cus_test_123",
		checkoutURL: "https://checkout.stripe.test/session/cs_test_xyz",
		priceID:     "price_test_paws",
		priceOK:     true,
	}
	svc.EnableSignupCheckout(fc, "https://mel.test/signup?canceled=1")
	return svc, fc
}

// ── Happy path ────────────────────────────────────────────────────────────────

func TestService_StartSignupCheckout_Happy_ReturnsCheckoutURL(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)

	res, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "founder@newclinic.test",
		FullName:   "Jane Founder",
		ClinicName: "Riverside Vets",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "https://checkout.stripe.test/session/cs_test_xyz", res.CheckoutURL)
	assert.Contains(t, res.HandoffURL, "/auth/handoff?")
	assert.Contains(t, res.HandoffURL, "checkout=success")
	assert.Contains(t, res.HandoffURL, "token=")

	assert.Equal(t, 1, fc.customerCalls)
	assert.Equal(t, "founder@newclinic.test", fc.lastEmail)
	assert.Equal(t, "Riverside Vets", fc.lastClinicName)

	assert.Equal(t, 1, fc.checkoutCalls)
	assert.Equal(t, "cus_test_123", fc.lastCheckout.CustomerID)
	assert.Equal(t, "price_test_paws", fc.lastCheckout.PriceID)
	assert.Equal(t, res.HandoffURL, fc.lastCheckout.SuccessURL)
	assert.Equal(t, "https://mel.test/signup?canceled=1", fc.lastCheckout.CancelURL)
	assert.Equal(t, int64(signupCheckoutTrialDays), fc.lastCheckout.TrialDays)
}

func TestService_StartSignupCheckout_HandoffTokenCarriesStripeCustomerID(t *testing.T) {
	t.Parallel()
	svc, _ := newSignupCheckoutService(t)

	res, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "founder@newclinic.test",
		FullName:   "Jane Founder",
		ClinicName: "Riverside Vets",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})
	require.NoError(t, err)

	u, err := url.Parse(res.HandoffURL)
	require.NoError(t, err)
	rawJWT := u.Query().Get("token")
	require.NotEmpty(t, rawJWT)

	claims, err := svc.parseHandoffJWT(rawJWT)
	require.NoError(t, err)
	assert.Equal(t, "cus_test_123", claims.StripeCustomerID)
	assert.Equal(t, "founder@newclinic.test", claims.Email)
	assert.Equal(t, string(domain.PlanPawsPracticeMonthly), claims.PlanCode)
}

// ── Disabled gates ────────────────────────────────────────────────────────────

func TestService_StartSignupCheckout_DisabledWhenSecretUnset(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t) // no handoff secret
	svc.EnableSignupCheckout(&fakeSignupCheckout{}, "https://mel.test/signup?canceled=1")

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestService_StartSignupCheckout_DisabledWhenClientNil(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	svc.SetMelHandoff(testHandoffSecret, &fakeHandoffProvisioner{})
	// EnableSignupCheckout never called

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestService_StartSignupCheckout_UnsupportedVertical_Rejected(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalAgedCare,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
	assert.Equal(t, 0, fc.customerCalls, "customer must not be created on validation failure")
}

func TestService_StartSignupCheckout_EmptyPlanCode_Rejected(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   "",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
	assert.Equal(t, 0, fc.customerCalls)
}

func TestService_StartSignupCheckout_UnknownPlan_Rejected(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   "phantom_plan_yearly",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
	assert.Equal(t, 0, fc.customerCalls)
}

func TestService_StartSignupCheckout_PlanWithoutPriceMapping_Rejected(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)
	fc.priceOK = false // simulate "STRIPE_PRICE_MAP doesn't have an entry for this plan"

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenInvalid)
	assert.Equal(t, 0, fc.customerCalls)
}

// ── Adapter failure propagation ───────────────────────────────────────────────

func TestService_StartSignupCheckout_CustomerError_Propagates(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)
	fc.customerErr = errors.New("stripe customer create failed")

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stripe customer create failed")
	assert.Equal(t, 0, fc.checkoutCalls, "checkout must not be created when customer creation fails")
}

func TestService_StartSignupCheckout_CheckoutError_Propagates(t *testing.T) {
	t.Parallel()
	svc, fc := newSignupCheckoutService(t)
	fc.checkoutErr = errors.New("stripe checkout create failed")

	_, err := svc.StartSignupCheckout(context.Background(), StartSignupCheckoutInput{
		Email:      "x@example.test",
		FullName:   "X",
		ClinicName: "X",
		Vertical:   domain.VerticalVeterinary,
		PlanCode:   string(domain.PlanPawsPracticeMonthly),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stripe checkout create failed")
	assert.Equal(t, 1, fc.customerCalls, "customer should already have been created before checkout fails")
}
