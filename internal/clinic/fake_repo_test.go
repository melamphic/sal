package clinic

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the clinic repo interface.
type fakeRepo struct {
	mu             sync.Mutex
	byID           map[uuid.UUID]*Clinic
	byEmail        map[string]*Clinic // keyed by emailHash
	byStripeCustID map[string]*Clinic // keyed by stripe customer id
	createFn       func(CreateParams) (*Clinic, error)
}

func newFakeClinicRepo() *fakeRepo {
	return &fakeRepo{
		byID:           make(map[uuid.UUID]*Clinic),
		byEmail:        make(map[string]*Clinic),
		byStripeCustID: make(map[string]*Clinic),
	}
}

func (f *fakeRepo) Create(_ context.Context, p CreateParams) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createFn != nil {
		return f.createFn(p)
	}
	c := &Clinic{
		ID:               p.ID,
		Name:             p.Name,
		Slug:             p.Slug,
		Email:            p.Email,
		EmailHash:        p.EmailHash,
		Phone:            p.Phone,
		Address:          p.Address,
		Vertical:         p.Vertical,
		Status:           domain.ClinicStatusTrial,
		TrialEndsAt:      p.TrialEndsAt,
		DataRegion:       p.DataRegion,
		PlanCode:         p.PlanCode,
		StripeCustomerID: p.StripeCustomerID,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	f.byID[c.ID] = c
	f.byEmail[c.EmailHash] = c
	if c.StripeCustomerID != nil {
		f.byStripeCustID[*c.StripeCustomerID] = c
	}
	return c, nil
}

func (f *fakeRepo) GetByStripeCustomerID(_ context.Context, stripeCustomerID string) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byStripeCustID[stripeCustomerID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) ApplySubscriptionState(_ context.Context, id uuid.UUID, p ApplySubscriptionStateParams) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c.Status = p.Status
	if p.PlanCode != nil {
		c.PlanCode = p.PlanCode
	}
	if p.StripeCustomerID != nil {
		c.StripeCustomerID = p.StripeCustomerID
		f.byStripeCustID[*p.StripeCustomerID] = c
	}
	if p.StripeSubscriptionID != nil {
		c.StripeSubscriptionID = p.StripeSubscriptionID
	}
	c.UpdatedAt = time.Now().UTC()
	return c, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) GetByEmailHash(_ context.Context, emailHash string) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byEmail[emailHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, p UpdateParams) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.Name != nil {
		c.Name = *p.Name
	}
	if p.Phone != nil {
		c.Phone = p.Phone
	}
	if p.Address != nil {
		c.Address = p.Address
	}
	if p.LogoKey != nil {
		c.LogoKey = p.LogoKey
	}
	if p.AccentColor != nil {
		c.AccentColor = p.AccentColor
	}
	if p.PDFHeaderText != nil {
		c.PDFHeaderText = p.PDFHeaderText
	}
	if p.PDFFooterText != nil {
		c.PDFFooterText = p.PDFFooterText
	}
	if p.PDFPrimaryColor != nil {
		c.PDFPrimaryColor = p.PDFPrimaryColor
	}
	if p.PDFFont != nil {
		c.PDFFont = p.PDFFont
	}
	if p.OnboardingStep != nil {
		c.OnboardingStep = *p.OnboardingStep
	}
	if p.OnboardingComplete != nil {
		c.OnboardingComplete = *p.OnboardingComplete
	}
	c.UpdatedAt = time.Now().UTC()
	return c, nil
}

func (f *fakeRepo) SubmitCompliance(_ context.Context, id uuid.UUID, p ComplianceParams) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.PrivacyOfficerName != nil {
		c.PrivacyOfficerName = p.PrivacyOfficerName
	}
	if p.PrivacyOfficerEmail != nil {
		c.PrivacyOfficerEmail = p.PrivacyOfficerEmail
	}
	if p.PrivacyOfficerPhone != nil {
		c.PrivacyOfficerPhone = p.PrivacyOfficerPhone
	}
	if p.POTrainingAttestedAt != nil {
		c.POTrainingAttestedAt = p.POTrainingAttestedAt
	}
	if p.CrossBorderAckAt != nil {
		c.CrossBorderAckAt = p.CrossBorderAckAt
	}
	if p.CrossBorderAckVersion != nil {
		c.CrossBorderAckVersion = p.CrossBorderAckVersion
	}
	if p.MHRRegistered != nil {
		c.MHRRegistered = p.MHRRegistered
	}
	if p.AIOversightAckAt != nil {
		c.AIOversightAckAt = p.AIOversightAckAt
	}
	if p.PatientConsentAckAt != nil {
		c.PatientConsentAckAt = p.PatientConsentAckAt
	}
	if p.DPAAcceptedAt != nil {
		c.DPAAcceptedAt = p.DPAAcceptedAt
	}
	if p.DPAVersion != nil {
		c.DPAVersion = p.DPAVersion
	}
	if p.CompletedAt != nil {
		c.ComplianceOnboardingCompletedAt = p.CompletedAt
	}
	if p.Version != nil {
		c.ComplianceOnboardingVersion = p.Version
	}
	if p.IP != nil {
		c.ComplianceOnboardingIP = p.IP
	}
	if p.UserID != nil {
		c.ComplianceOnboardingUserID = p.UserID
	}
	if p.AdvanceStep && c.OnboardingStep < 2 {
		c.OnboardingStep = 2
	}
	c.UpdatedAt = time.Now().UTC()
	return c, nil
}
