package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/webhook"
)

// ── Fakes ───────────────────────────────────────────────────────────────

type fakeRepo struct {
	mu       sync.Mutex
	recorded []RecordEventParams
	// conflictOnEventID causes RecordEvent to return ErrConflict for a
	// given event id — simulates idempotent replay.
	conflictOnEventID string
}

func (r *fakeRepo) RecordEvent(_ context.Context, p RecordEventParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.EventID == r.conflictOnEventID {
		return domain.ErrConflict
	}
	r.recorded = append(r.recorded, p)
	return nil
}

type fakeClinics struct {
	byCustID       map[string]uuid.UUID
	customerIDByID map[uuid.UUID]*string
	applied        []appliedState
}

type appliedState struct {
	ClinicID uuid.UUID
	State    SubscriptionState
}

func (c *fakeClinics) FindByStripeCustomerID(_ context.Context, id string) (uuid.UUID, error) {
	if v, ok := c.byCustID[id]; ok {
		return v, nil
	}
	return uuid.Nil, domain.ErrNotFound
}

func (c *fakeClinics) ApplySubscriptionState(_ context.Context, clinicID uuid.UUID, s SubscriptionState) error {
	c.applied = append(c.applied, appliedState{ClinicID: clinicID, State: s})
	return nil
}

func (c *fakeClinics) GetStripeCustomerID(_ context.Context, clinicID uuid.UUID) (*string, error) {
	if v, ok := c.customerIDByID[clinicID]; ok {
		return v, nil
	}
	return nil, domain.ErrNotFound
}

type fakePlans map[string]domain.PlanCode

func (p fakePlans) PlanCodeForStripePriceID(id string) (domain.PlanCode, bool) {
	pc, ok := p[id]
	return pc, ok
}

type fakePortal struct {
	lastCustomerID string
	lastReturnURL  string
	url            string
	err            error
}

func (p *fakePortal) Create(customerID, returnURL string) (string, error) {
	p.lastCustomerID = customerID
	p.lastReturnURL = returnURL
	return p.url, p.err
}

// ── Portal tests ────────────────────────────────────────────────────────

func TestService_CreatePortalSession_Disabled(t *testing.T) {
	t.Parallel()
	svc := NewService(&fakeRepo{}, &fakeClinics{}, fakePlans{}, []byte("whsec"))

	_, err := svc.CreatePortalSession(context.Background(), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation when portal disabled, got %v", err)
	}
}

func TestService_CreatePortalSession_NoCustomer(t *testing.T) {
	t.Parallel()
	clinicID := uuid.Must(uuid.NewV7())
	clinics := &fakeClinics{customerIDByID: map[uuid.UUID]*string{clinicID: nil}}
	svc := NewService(&fakeRepo{}, clinics, fakePlans{}, []byte("whsec"))
	svc.EnablePortal(&fakePortal{url: "https://should-not-be-called"}, "https://app/ret")

	_, err := svc.CreatePortalSession(context.Background(), clinicID)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation when clinic has no customer id, got %v", err)
	}
}

func TestService_CreatePortalSession_Success(t *testing.T) {
	t.Parallel()
	clinicID := uuid.Must(uuid.NewV7())
	custID := "cus_12345"
	clinics := &fakeClinics{customerIDByID: map[uuid.UUID]*string{clinicID: &custID}}
	portal := &fakePortal{url: "https://billing.stripe.com/session/abc"}
	svc := NewService(&fakeRepo{}, clinics, fakePlans{}, []byte("whsec"))
	svc.EnablePortal(portal, "https://app.salvia.nz/settings/billing")

	url, err := svc.CreatePortalSession(context.Background(), clinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != portal.url {
		t.Fatalf("url mismatch: got %q want %q", url, portal.url)
	}
	if portal.lastCustomerID != custID {
		t.Fatalf("customer id not forwarded: got %q", portal.lastCustomerID)
	}
	if portal.lastReturnURL != "https://app.salvia.nz/settings/billing" {
		t.Fatalf("return url not forwarded: got %q", portal.lastReturnURL)
	}
}

// ── Webhook tests ───────────────────────────────────────────────────────

func TestService_HandleWebhook_InvalidSignature(t *testing.T) {
	t.Parallel()
	svc := NewService(&fakeRepo{}, &fakeClinics{}, fakePlans{}, []byte("whsec_correct"))

	err := svc.HandleWebhook(context.Background(), []byte(`{}`), "t=0,v1=deadbeef")
	if !errors.Is(err, domain.ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestService_HandleWebhook_SubscriptionCreated_AppliesState(t *testing.T) {
	t.Parallel()
	secret := "whsec_test_123"
	clinicID := uuid.Must(uuid.NewV7())
	clinics := &fakeClinics{byCustID: map[string]uuid.UUID{"cus_abc": clinicID}}
	plans := fakePlans{"price_pro_annual": domain.PlanCode("paws_pro_annual")}
	repo := &fakeRepo{}
	svc := NewService(repo, clinics, plans, []byte(secret))

	raw := subscriptionEvent("evt_1", "cus_abc", "sub_1", "price_pro_annual", "active")
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: raw,
		Secret:  secret,
	})

	if err := svc.HandleWebhook(context.Background(), raw, signed.Header); err != nil {
		t.Fatalf("handle webhook: %v", err)
	}

	if len(clinics.applied) != 1 {
		t.Fatalf("expected 1 applied state, got %d", len(clinics.applied))
	}
	got := clinics.applied[0]
	if got.ClinicID != clinicID {
		t.Fatalf("clinic id mismatch: got %s want %s", got.ClinicID, clinicID)
	}
	if got.State.Status != domain.ClinicStatusActive {
		t.Fatalf("status: got %q want %q", got.State.Status, domain.ClinicStatusActive)
	}
	if got.State.PlanCode == nil || *got.State.PlanCode != domain.PlanCode("paws_pro_annual") {
		t.Fatalf("plan code: got %v want paws_pro_annual", got.State.PlanCode)
	}
}

func TestService_HandleWebhook_Replay_IsIdempotent(t *testing.T) {
	t.Parallel()
	secret := "whsec_replay"
	clinicID := uuid.Must(uuid.NewV7())
	clinics := &fakeClinics{byCustID: map[string]uuid.UUID{"cus_abc": clinicID}}
	repo := &fakeRepo{conflictOnEventID: "evt_dup"}
	svc := NewService(repo, clinics, fakePlans{}, []byte(secret))

	raw := subscriptionEvent("evt_dup", "cus_abc", "sub_1", "price_unknown", "active")
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: raw,
		Secret:  secret,
	})

	if err := svc.HandleWebhook(context.Background(), raw, signed.Header); err != nil {
		t.Fatalf("handle webhook (replay): %v", err)
	}

	if len(clinics.applied) != 0 {
		t.Fatalf("replay should not apply state, got %d", len(clinics.applied))
	}
}

func TestService_HandleWebhook_UnmappedCustomer(t *testing.T) {
	t.Parallel()
	secret := "whsec_unmapped"
	repo := &fakeRepo{}
	svc := NewService(repo, &fakeClinics{}, fakePlans{}, []byte(secret))

	raw := subscriptionEvent("evt_unmapped", "cus_unknown", "sub_1", "price_x", "active")
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: raw,
		Secret:  secret,
	})

	if err := svc.HandleWebhook(context.Background(), raw, signed.Header); err != nil {
		t.Fatalf("handle webhook: %v", err)
	}
	if len(repo.recorded) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(repo.recorded))
	}
	if repo.recorded[0].Status != EventStatusUnmapped {
		t.Fatalf("expected status unmapped, got %q", repo.recorded[0].Status)
	}
}

// subscriptionEvent builds a minimal Stripe event JSON payload for
// customer.subscription.created — used to drive HandleWebhook in tests.
func subscriptionEvent(eventID, customerID, subID, priceID, status string) []byte {
	sub := stripe.Subscription{
		ID: subID,
		Customer: &stripe.Customer{
			ID: customerID,
		},
		Status: stripe.SubscriptionStatus(status),
		Items: &stripe.SubscriptionItemList{
			Data: []*stripe.SubscriptionItem{
				{Price: &stripe.Price{ID: priceID}},
			},
		},
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		panic(err)
	}
	evt := map[string]any{
		"id":          eventID,
		"type":        "customer.subscription.created",
		"api_version": "2024-04-10",
		"data": map[string]any{
			"object": json.RawMessage(raw),
		},
	}
	out, err := json.Marshal(evt)
	if err != nil {
		panic(fmt.Errorf("marshal event: %w", err))
	}
	return out
}
