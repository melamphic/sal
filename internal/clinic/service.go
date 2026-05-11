package clinic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/crypto"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// AdminBootstrapper creates the first super admin for a newly registered clinic
// and sends them a magic link so they can log in immediately.
// Implemented by an adapter in app.go that bridges to staff.Service + auth.Service.
type AdminBootstrapper interface {
	Bootstrap(ctx context.Context, clinicID uuid.UUID, email, name string) error
}

// SalviaContentMaterialiser installs the per-(vertical, country) Salvia v1
// content library into a freshly-onboarded clinic. Called once after
// SubmitCompliance succeeds — at which point vertical (clinic-create),
// country (Update), and an authenticated staffID (the submitter) are all
// available. Best-effort: the materialiser logs per-template failures and
// returns; clinic onboarding does not abort if a single template fails to
// install. Implemented by an adapter in app.go bridging to
// salvia_content.Materialiser.
type SalviaContentMaterialiser interface {
	MaterialiseFor(
		ctx context.Context,
		clinicID uuid.UUID,
		vertical domain.Vertical,
		country string,
		staffID uuid.UUID,
	)
}

// LogoSigner produces a short-lived signed GET URL for a logo object key.
// Implemented by platform/storage in app.go.
type LogoSigner interface {
	SignLogoURL(ctx context.Context, key string) (string, error)
}

// LogoUploader uploads bytes to object storage and returns the persisted key.
// Implemented by platform/storage in app.go.
type LogoUploader interface {
	UploadLogo(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (string, error)
}

// Service handles all clinic business logic.
type Service struct {
	repo                repo // interface — see repo.go
	cipher              *crypto.Cipher
	bootstrapper        AdminBootstrapper         // nil = skip (for tests that don't need the full flow)
	logoSigner          LogoSigner                // nil = skip URL signing (logo_url omitted)
	logoUploader        LogoUploader              // nil = logo upload disabled
	salviaMaterialiser  SalviaContentMaterialiser // nil = skip (e.g. tests, dev with content disabled)
}

// NewService creates a new clinic Service.
func NewService(repo repo, cipher *crypto.Cipher, bootstrapper AdminBootstrapper, signer LogoSigner, uploader LogoUploader) *Service {
	return &Service{repo: repo, cipher: cipher, bootstrapper: bootstrapper, logoSigner: signer, logoUploader: uploader}
}

// SetSalviaContentMaterialiser wires the post-onboarding content installer.
// Optional — if absent, SubmitCompliance succeeds without installing any
// Salvia v1 templates. Called from app.go after both clinic and
// salvia_content services are constructed.
func (s *Service) SetSalviaContentMaterialiser(m SalviaContentMaterialiser) {
	s.salviaMaterialiser = m
}

// ClinicResponse is the decrypted, service-layer representation of a clinic.
// This is what handlers receive and what API responses are built from.
// The encrypted DB row is never exposed outside the service layer.
type ClinicResponse struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Slug               string              `json:"slug"`
	Email              string              `json:"email"`
	Phone              *string             `json:"phone,omitempty"`
	Address            *string             `json:"address,omitempty"`
	Vertical           domain.Vertical     `json:"vertical"`
	Status             domain.ClinicStatus `json:"status"`
	TrialEndsAt        time.Time           `json:"trial_ends_at"`
	PlanCode           *domain.PlanCode    `json:"plan_code,omitempty"`
	NoteCount          int                 `json:"note_count"`
	NoteCap            *int                `json:"note_cap,omitempty"`
	DataRegion         string              `json:"data_region"`
	LogoURL            *string             `json:"logo_url,omitempty"`
	AccentColor        *string             `json:"accent_color,omitempty"`
	PDFHeaderText      *string             `json:"pdf_header_text,omitempty"`
	PDFFooterText      *string             `json:"pdf_footer_text,omitempty"`
	PDFPrimaryColor    *string             `json:"pdf_primary_color,omitempty"`
	PDFFont            *string             `json:"pdf_font,omitempty"`
	OnboardingStep     int16               `json:"onboarding_step"`
	OnboardingComplete bool                `json:"onboarding_complete"`
	LegalName          *string             `json:"legal_name,omitempty"`
	Country            string              `json:"country"`
	Timezone           string              `json:"timezone"`
	BusinessRegNo      *string             `json:"business_reg_no,omitempty"`
	TermsAcceptedAt    *time.Time          `json:"terms_accepted_at,omitempty"`
	// Compliance onboarding state. Booleans are derived from the
	// timestamp's nullability (e.g. ai_oversight_acknowledged is true iff
	// ai_oversight_ack_at is non-null) — clients render badges/banners off
	// these fields without needing to know the underlying schema.
	PrivacyOfficerName              *string    `json:"privacy_officer_name,omitempty"`
	PrivacyOfficerEmail             *string    `json:"privacy_officer_email,omitempty"`
	PrivacyOfficerPhone             *string    `json:"privacy_officer_phone,omitempty"`
	POTrainingAttestedAt            *time.Time `json:"po_training_attested_at,omitempty"`
	CrossBorderAckAt                *time.Time `json:"cross_border_ack_at,omitempty"`
	CrossBorderAckVersion           *string    `json:"cross_border_ack_version,omitempty"`
	MHRRegistered                   *bool      `json:"mhr_registered,omitempty"`
	AIOversightAckAt                *time.Time `json:"ai_oversight_ack_at,omitempty"`
	PatientConsentAckAt             *time.Time `json:"patient_consent_ack_at,omitempty"`
	DPAAcceptedAt                   *time.Time `json:"dpa_accepted_at,omitempty"`
	DPAVersion                      *string    `json:"dpa_version,omitempty"`
	ComplianceOnboardingCompletedAt *time.Time `json:"compliance_onboarding_completed_at,omitempty"`
	ComplianceOnboardingVersion     *string    `json:"compliance_onboarding_version,omitempty"`
	// RegulatoryIDs is the per-jurisdiction identifier blob — keys
	// like "nzbn", "cqc_location_id", "dea_id" surfaced verbatim. The
	// FE picks which keys to render based on (vertical, country).
	// Empty map → field absent from response.
	RegulatoryIDs map[string]string `json:"regulatory_ids,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// RegisterInput holds the data required to register a new clinic.
type RegisterInput struct {
	Name       string
	Email      string
	Phone      *string
	Address    *string
	Vertical   domain.Vertical
	DataRegion string
	// First super admin — created and emailed a magic link after the clinic row is inserted.
	AdminEmail string
	AdminName  string
}

// Register creates a new clinic in trial status.
// Returns domain.ErrConflict if a clinic with the same email already exists.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*ClinicResponse, error) {
	emailHash := s.cipher.Hash(in.Email)

	// Check for duplicate email.
	_, err := s.repo.GetByEmailHash(ctx, emailHash)
	if err == nil {
		return nil, domain.ErrConflict
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("clinic.service.Register: check duplicate: %w", err)
	}

	encEmail, err := s.cipher.Encrypt(in.Email)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.Register: encrypt email: %w", err)
	}

	var encPhone, encAddress *string
	if in.Phone != nil {
		ep, err := s.cipher.Encrypt(*in.Phone)
		if err != nil {
			return nil, fmt.Errorf("clinic.service.Register: encrypt phone: %w", err)
		}
		encPhone = &ep
	}
	if in.Address != nil {
		ea, err := s.cipher.Encrypt(*in.Address)
		if err != nil {
			return nil, fmt.Errorf("clinic.service.Register: encrypt address: %w", err)
		}
		encAddress = &ea
	}

	vertical := in.Vertical
	if vertical == "" {
		vertical = domain.VerticalVeterinary
	}
	dataRegion := in.DataRegion
	if dataRegion == "" {
		dataRegion = "ap-southeast-2"
	}

	slug := generateSlug(in.Name)

	var row *Clinic
	for attempt := 0; attempt < 3; attempt++ {
		p := CreateParams{
			ID:          domain.NewID(),
			Name:        in.Name,
			Slug:        slug,
			Email:       encEmail,
			EmailHash:   emailHash,
			Phone:       encPhone,
			Address:     encAddress,
			Vertical:    vertical,
			TrialEndsAt: domain.TimeNow().Add(21 * 24 * time.Hour),
			DataRegion:  dataRegion,
		}
		row, err = s.repo.Create(ctx, p)
		if err == nil {
			break
		}
		if !domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("clinic.service.Register: create: %w", err)
		}
		// Slug collision — append a random suffix and retry.
		slug = generateSlug(in.Name) + "-" + domain.NewID().String()[:4]
	}
	if row == nil {
		return nil, fmt.Errorf("clinic.service.Register: slug collision after retries")
	}

	// Create the first super admin and send them a magic link.
	if s.bootstrapper != nil && in.AdminEmail != "" {
		if err := s.bootstrapper.Bootstrap(ctx, row.ID, in.AdminEmail, in.AdminName); err != nil {
			return nil, fmt.Errorf("clinic.service.Register: bootstrap admin: %w", err)
		}
	}

	return s.toDTO(row, in.Email, in.Phone, in.Address), nil
}

// HandoffProvisionInput holds the post-JWT-verify payload from /mel handoff.
// The handoff path skips the AdminBootstrapper flow: staff creation + session
// issuance are handled by auth.Service.HandoffFromMel once this returns.
type HandoffProvisionInput struct {
	Email            string // plaintext
	ClinicName       string
	Vertical         domain.Vertical
	PlanCode         *domain.PlanCode // nil = trial signup
	StripeCustomerID *string          // cus_… from /mel Checkout; nil on trial
}

// HandoffProvision finds-or-creates a clinic for a /mel handoff. Idempotent
// on email_hash so replaying the same email (with a fresh jti) returns the
// existing clinic. Plan code is only set on initial create — subsequent
// updates come from the Stripe webhook, the canonical source of truth.
func (s *Service) HandoffProvision(ctx context.Context, in HandoffProvisionInput) (*ClinicResponse, error) {
	emailHash := s.cipher.Hash(in.Email)

	existing, err := s.repo.GetByEmailHash(ctx, emailHash)
	if err == nil {
		return s.decryptAndBuild(ctx, existing)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("clinic.service.HandoffProvision: lookup: %w", err)
	}

	encEmail, err := s.cipher.Encrypt(in.Email)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.HandoffProvision: encrypt email: %w", err)
	}

	vertical := in.Vertical
	if vertical == "" {
		vertical = domain.VerticalVeterinary
	}

	slug := generateSlug(in.ClinicName)

	var row *Clinic
	for attempt := 0; attempt < 3; attempt++ {
		p := CreateParams{
			ID:               domain.NewID(),
			Name:             in.ClinicName,
			Slug:             slug,
			Email:            encEmail,
			EmailHash:        emailHash,
			Vertical:         vertical,
			TrialEndsAt:      domain.TimeNow().Add(21 * 24 * time.Hour),
			DataRegion:       "ap-southeast-2",
			PlanCode:         in.PlanCode,
			StripeCustomerID: in.StripeCustomerID,
		}
		row, err = s.repo.Create(ctx, p)
		if err == nil {
			break
		}
		if !domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("clinic.service.HandoffProvision: create: %w", err)
		}
		slug = generateSlug(in.ClinicName) + "-" + domain.NewID().String()[:4]
	}
	if row == nil {
		return nil, fmt.Errorf("clinic.service.HandoffProvision: slug collision after retries")
	}

	return s.toDTO(row, in.Email, nil, nil), nil
}

// BillingState is the authoritative subscription state written by the
// billing module in response to a Stripe webhook. Status always writes;
// the pointer fields are COALESCEd at the repo layer — nil means leave
// unchanged.
type BillingState struct {
	Status               domain.ClinicStatus
	PlanCode             *domain.PlanCode
	StripeCustomerID     *string
	StripeSubscriptionID *string
	BillingPeriodStart   *time.Time
	BillingPeriodEnd     *time.Time
}

// GetIDByStripeCustomer returns the clinic id for a Stripe customer id.
// Used by the billing webhook adapter. Returns domain.ErrNotFound when
// no clinic has been provisioned for this customer yet — the adapter
// records the event as `unmapped` and returns 200.
func (s *Service) GetIDByStripeCustomer(ctx context.Context, stripeCustomerID string) (uuid.UUID, error) {
	row, err := s.repo.GetByStripeCustomerID(ctx, stripeCustomerID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("clinic.service.GetIDByStripeCustomer: %w", err)
	}
	return row.ID, nil
}

// GetStripeCustomerID returns the cus_… id for a clinic, or nil when the
// clinic has none yet (still on trial). Used by billing to open a Stripe
// customer portal session.
func (s *Service) GetStripeCustomerID(ctx context.Context, clinicID uuid.UUID) (*string, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.GetStripeCustomerID: %w", err)
	}
	return row.StripeCustomerID, nil
}

// ApplyBillingState writes the authoritative subscription state for a
// clinic. Billing-module-only entrypoint; never called from HTTP handlers.
func (s *Service) ApplyBillingState(ctx context.Context, clinicID uuid.UUID, p BillingState) error {
	if _, err := s.repo.ApplySubscriptionState(ctx, clinicID, ApplySubscriptionStateParams(p)); err != nil {
		return fmt.Errorf("clinic.service.ApplyBillingState: %w", err)
	}
	return nil
}

// NoteCapState is the read-only slice of clinic state notecap needs to
// resolve cap, period window, and cascade flags. Email is decrypted —
// notecap uses it as the "to" for the 80% warning.
type NoteCapState struct {
	ID                 uuid.UUID
	Name               string
	Email              string
	Status             domain.ClinicStatus
	PlanCode           *domain.PlanCode
	BillingPeriodStart *time.Time
	CreatedAt          time.Time
	NoteCapWarnedAt    *time.Time
	NoteCapCSAlertedAt *time.Time
	NoteCapBlockedAt   *time.Time
}

// LoadNoteCapState fetches and decrypts the slice of clinic fields the
// note-cap module needs. Cross-domain entrypoint — never called from
// HTTP handlers.
func (s *Service) LoadNoteCapState(ctx context.Context, clinicID uuid.UUID) (NoteCapState, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return NoteCapState{}, fmt.Errorf("clinic.service.LoadNoteCapState: %w", err)
	}
	email, err := s.cipher.Decrypt(row.Email)
	if err != nil {
		return NoteCapState{}, fmt.Errorf("clinic.service.LoadNoteCapState: decrypt email: %w", err)
	}
	return NoteCapState{
		ID:                 row.ID,
		Name:               row.Name,
		Email:              email,
		Status:             row.Status,
		PlanCode:           row.PlanCode,
		BillingPeriodStart: row.BillingPeriodStart,
		CreatedAt:          row.CreatedAt,
		NoteCapWarnedAt:    row.NoteCapWarnedAt,
		NoteCapCSAlertedAt: row.NoteCapCSAlertedAt,
		NoteCapBlockedAt:   row.NoteCapBlockedAt,
	}, nil
}

// MarkNoteCapWarned stamps the warned-at flag iff currently NULL. The
// idempotent claim contract is documented on Repository.MarkNoteCapWarned.
func (s *Service) MarkNoteCapWarned(ctx context.Context, clinicID uuid.UUID) (bool, error) {
	claimed, err := s.repo.MarkNoteCapWarned(ctx, clinicID, domain.TimeNow())
	if err != nil {
		return false, fmt.Errorf("clinic.service.MarkNoteCapWarned: %w", err)
	}
	return claimed, nil
}

// MarkNoteCapCSAlerted stamps the cs-alerted flag iff currently NULL.
func (s *Service) MarkNoteCapCSAlerted(ctx context.Context, clinicID uuid.UUID) (bool, error) {
	claimed, err := s.repo.MarkNoteCapCSAlerted(ctx, clinicID, domain.TimeNow())
	if err != nil {
		return false, fmt.Errorf("clinic.service.MarkNoteCapCSAlerted: %w", err)
	}
	return claimed, nil
}

// MarkNoteCapBlocked stamps the blocked-at flag iff currently NULL.
func (s *Service) MarkNoteCapBlocked(ctx context.Context, clinicID uuid.UUID) (bool, error) {
	claimed, err := s.repo.MarkNoteCapBlocked(ctx, clinicID, domain.TimeNow())
	if err != nil {
		return false, fmt.Errorf("clinic.service.MarkNoteCapBlocked: %w", err)
	}
	return claimed, nil
}

// TierState is the read-only slice of clinic state the tiering module
// needs to reconcile a Practice/Pro plan against the current standard
// staff headcount. Cross-domain port — never called from HTTP handlers.
type TierState struct {
	Status               domain.ClinicStatus
	PlanCode             *domain.PlanCode
	StripeSubscriptionID *string
}

// DashboardState is the slim slice of clinic fields the dashboard
// service needs to render watchcards (note cap progress, trial
// countdown, onboarding-incomplete pill). One row read; no
// decryption — none of the fields here hold PII/PHI.
type DashboardState struct {
	NoteCap            *int
	NoteCount          int
	TrialEndsAt        time.Time
	OnboardingComplete bool
}

// LoadDashboardState fetches the per-clinic dashboard inputs in a
// single GetByID call. Cross-domain port — never called from HTTP
// handlers.
func (s *Service) LoadDashboardState(ctx context.Context, clinicID uuid.UUID) (DashboardState, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return DashboardState{}, fmt.Errorf("clinic.service.LoadDashboardState: %w", err)
	}
	return DashboardState{
		NoteCap:            row.NoteCap,
		NoteCount:          row.NoteCount,
		TrialEndsAt:        row.TrialEndsAt,
		OnboardingComplete: row.OnboardingComplete,
	}, nil
}

// LoadTierState fetches the slice of clinic fields the tiering module
// needs to decide whether to swap Stripe subscription items.
func (s *Service) LoadTierState(ctx context.Context, clinicID uuid.UUID) (TierState, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return TierState{}, fmt.Errorf("clinic.service.LoadTierState: %w", err)
	}
	return TierState{
		Status:               row.Status,
		PlanCode:             row.PlanCode,
		StripeSubscriptionID: row.StripeSubscriptionID,
	}, nil
}

// GetByID returns decrypted clinic details for the authenticated clinic.
func (s *Service) GetByID(ctx context.Context, clinicID uuid.UUID) (*ClinicResponse, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.GetByID: %w", err)
	}

	return s.decryptAndBuild(ctx, row)
}

// GetStatus returns the subscription lifecycle status for a clinic.
// Cheap (single column read, no decrypt) — safe to call on every
// authenticated request from the grace-period middleware.
func (s *Service) GetStatus(ctx context.Context, clinicID uuid.UUID) (domain.ClinicStatus, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("clinic.service.GetStatus: %w", err)
	}
	return row.Status, nil
}

// GetVertical returns the configured vertical for a clinic. It avoids the
// decrypt step used by GetByID so callers on the hot path (e.g. patient
// create) can resolve the vertical cheaply.
func (s *Service) GetVertical(ctx context.Context, clinicID uuid.UUID) (domain.Vertical, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("clinic.service.GetVertical: %w", err)
	}
	return row.Vertical, nil
}

// UpdateInput holds optional update fields. Nil = leave unchanged.
type UpdateInput struct {
	Name               *string
	Phone              *string
	Address            *string
	AccentColor        *string
	PDFHeaderText      *string
	PDFFooterText      *string
	PDFPrimaryColor    *string
	PDFFont            *string
	OnboardingStep     *int16
	OnboardingComplete *bool
	LegalName          *string
	Country            *string
	Timezone           *string
	BusinessRegNo      *string
	// AcceptTerms, when true, stamps terms_accepted_at with the current
	// time. Passing false (or leaving nil) does not clear an existing
	// acceptance — terms can only be accepted, not unaccepted.
	AcceptTerms *bool
	// RegulatoryIDs is the per-jurisdiction identifier map. Pass nil
	// to leave unchanged; pass an empty map to clear all IDs;
	// pass a populated map to replace.
	RegulatoryIDs map[string]string
}

// Update applies a partial update to the clinic settings.
func (s *Service) Update(ctx context.Context, clinicID uuid.UUID, in UpdateInput) (*ClinicResponse, error) {
	p := UpdateParams{
		Name:               in.Name,
		AccentColor:        in.AccentColor,
		PDFHeaderText:      in.PDFHeaderText,
		PDFFooterText:      in.PDFFooterText,
		PDFPrimaryColor:    in.PDFPrimaryColor,
		PDFFont:            in.PDFFont,
		OnboardingStep:     in.OnboardingStep,
		OnboardingComplete: in.OnboardingComplete,
		LegalName:          in.LegalName,
		Country:            in.Country,
		Timezone:           in.Timezone,
		BusinessRegNo:      in.BusinessRegNo,
	}
	if in.AcceptTerms != nil && *in.AcceptTerms {
		now := domain.TimeNow()
		p.TermsAcceptedAt = &now
	}
	if in.RegulatoryIDs != nil {
		raw, err := json.Marshal(in.RegulatoryIDs)
		if err != nil {
			return nil, fmt.Errorf("clinic.service.Update: marshal regulatory_ids: %w", err)
		}
		p.RegulatoryIDs = raw
	}

	if in.Phone != nil {
		ep, err := s.cipher.Encrypt(*in.Phone)
		if err != nil {
			return nil, fmt.Errorf("clinic.service.Update: encrypt phone: %w", err)
		}
		p.Phone = &ep
	}
	if in.Address != nil {
		ea, err := s.cipher.Encrypt(*in.Address)
		if err != nil {
			return nil, fmt.Errorf("clinic.service.Update: encrypt address: %w", err)
		}
		p.Address = &ea
	}

	row, err := s.repo.Update(ctx, clinicID, p)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.Update: %w", err)
	}

	return s.decryptAndBuild(ctx, row)
}

// complianceSchemaVersion identifies the disclosure copy + attestation
// shape that a tenant signed off on. Bumped whenever the wizard changes
// the set of acks or the wording of a disclosure (cross-border, DPA),
// which forces clinics to re-affirm on next visit.
const complianceSchemaVersion = "v1"

// SubmitComplianceInput holds the compliance attestation submitted from
// the onboarding wizard. Boolean acks are converted to timestamps in the
// service layer (only stamped when the ack is true). MHRRegistered is a
// nullable tri-state for AU clinics — null outside AU, true/false inside.
type SubmitComplianceInput struct {
	PrivacyOfficerName     string
	PrivacyOfficerEmail    string
	PrivacyOfficerPhone    string // optional but recommended
	POTrainingAttested     bool
	CrossBorderAcknowledged bool
	MHRRegistered          *bool
	AIOversightAcknowledged bool
	PatientConsentAcknowledged bool
	DPAAccepted            bool
	// IP of the submitting client, captured from X-Forwarded-For /
	// X-Real-Ip in the handler. Empty string if not derivable.
	IP string
	// StaffID of the submitting user. Recorded for audit.
	StaffID uuid.UUID
}

// SubmitCompliance writes the full compliance attestation for a clinic.
// Returns ErrForbidden if any required ack is missing or if PO contact is
// empty — UI must guard but server enforces. All required acks must be
// true to stamp completed_at; partial submissions update only the fields
// the caller provided.
func (s *Service) SubmitCompliance(ctx context.Context, clinicID uuid.UUID, in SubmitComplianceInput) (*ClinicResponse, error) {
	now := domain.TimeNow()

	// Server-side validation. Required fields for a complete attestation:
	// PO name, PO email, all four boolean acks, DPA acceptance.
	if strings.TrimSpace(in.PrivacyOfficerName) == "" {
		return nil, fmt.Errorf("clinic.service.SubmitCompliance: %w: privacy_officer_name required", domain.ErrForbidden)
	}
	if strings.TrimSpace(in.PrivacyOfficerEmail) == "" {
		return nil, fmt.Errorf("clinic.service.SubmitCompliance: %w: privacy_officer_email required", domain.ErrForbidden)
	}
	if !in.POTrainingAttested || !in.CrossBorderAcknowledged ||
		!in.AIOversightAcknowledged || !in.PatientConsentAcknowledged ||
		!in.DPAAccepted {
		return nil, fmt.Errorf("clinic.service.SubmitCompliance: %w: all attestations required", domain.ErrForbidden)
	}

	p := ComplianceParams{
		PrivacyOfficerName:    ptrStr(strings.TrimSpace(in.PrivacyOfficerName)),
		PrivacyOfficerEmail:   ptrStr(strings.TrimSpace(in.PrivacyOfficerEmail)),
		PrivacyOfficerPhone:   ptrStr(strings.TrimSpace(in.PrivacyOfficerPhone)),
		POTrainingAttestedAt:  &now,
		CrossBorderAckAt:      &now,
		CrossBorderAckVersion: ptrStr(complianceSchemaVersion),
		MHRRegistered:         in.MHRRegistered,
		AIOversightAckAt:      &now,
		PatientConsentAckAt:   &now,
		DPAAcceptedAt:         &now,
		DPAVersion:            ptrStr(complianceSchemaVersion),
		CompletedAt:           &now,
		Version:               ptrStr(complianceSchemaVersion),
		UserID:                &in.StaffID,
		AdvanceStep:           true,
	}
	if in.IP != "" {
		p.IP = &in.IP
	}

	row, err := s.repo.SubmitCompliance(ctx, clinicID, p)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.SubmitCompliance: %w", err)
	}

	// Compliance just completed — fire the Salvia content materialiser to
	// install the per-(vertical, country) library. Best-effort: the
	// materialiser handles per-template errors internally and logs;
	// failures here must not abort compliance submission. Idempotent
	// thanks to the unique index `idx_forms_salvia_template_per_clinic` /
	// `idx_policies_salvia_template_per_clinic` — re-submission re-runs
	// safely.
	if s.salviaMaterialiser != nil && row.Country != "" && row.Vertical != "" {
		s.salviaMaterialiser.MaterialiseFor(ctx, clinicID, row.Vertical, row.Country, in.StaffID)
	}

	return s.decryptAndBuild(ctx, row)
}

// ptrStr is a small helper for converting a string to a pointer when
// non-empty. Empty strings become nil so COALESCE leaves the column
// unchanged on partial submissions.
func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UploadLogo persists a logo to object storage and updates the clinic's logo_key.
// Returns the updated clinic with a freshly-signed logo_url.
func (s *Service) UploadLogo(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (*ClinicResponse, error) {
	if s.logoUploader == nil {
		return nil, fmt.Errorf("clinic.service.UploadLogo: storage not configured")
	}
	key, err := s.logoUploader.UploadLogo(ctx, clinicID, contentType, body, size)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.UploadLogo: upload: %w", err)
	}
	row, err := s.repo.Update(ctx, clinicID, UpdateParams{LogoKey: &key})
	if err != nil {
		return nil, fmt.Errorf("clinic.service.UploadLogo: persist key: %w", err)
	}
	return s.decryptAndBuild(ctx, row)
}

// ── Private helpers ───────────────────────────────────────────────────────────

func (s *Service) decryptAndBuild(ctx context.Context, row *Clinic) (*ClinicResponse, error) {
	email, err := s.cipher.Decrypt(row.Email)
	if err != nil {
		return nil, fmt.Errorf("clinic.service: decrypt email: %w", err)
	}

	var phone, address *string
	if row.Phone != nil {
		p, err := s.cipher.Decrypt(*row.Phone)
		if err != nil {
			return nil, fmt.Errorf("clinic.service: decrypt phone: %w", err)
		}
		phone = &p
	}
	if row.Address != nil {
		a, err := s.cipher.Decrypt(*row.Address)
		if err != nil {
			return nil, fmt.Errorf("clinic.service: decrypt address: %w", err)
		}
		address = &a
	}

	dto := s.toDTO(row, email, phone, address)

	if row.LogoKey != nil && s.logoSigner != nil {
		url, err := s.logoSigner.SignLogoURL(ctx, *row.LogoKey)
		if err != nil {
			return nil, fmt.Errorf("clinic.service: sign logo url: %w", err)
		}
		dto.LogoURL = &url
	}

	return dto, nil
}

// decodeRegulatoryIDs unmarshals the JSONB blob into a string→string
// map, returning nil when the column is empty so the response omits
// the JSON key entirely.
func decodeRegulatoryIDs(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		// A bad blob in the DB should never happen — service-side
		// validates on every write. Surface as nil so the response
		// stays well-formed; the operator can fix the row manually.
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Service) toDTO(row *Clinic, email string, phone, address *string) *ClinicResponse {
	return &ClinicResponse{
		ID:                 row.ID.String(),
		Name:               row.Name,
		Slug:               row.Slug,
		Email:              email,
		Phone:              phone,
		Address:            address,
		Vertical:           row.Vertical,
		Status:             row.Status,
		TrialEndsAt:        row.TrialEndsAt,
		PlanCode:           row.PlanCode,
		NoteCount:          row.NoteCount,
		NoteCap:            row.NoteCap,
		DataRegion:         row.DataRegion,
		AccentColor:        row.AccentColor,
		PDFHeaderText:      row.PDFHeaderText,
		PDFFooterText:      row.PDFFooterText,
		PDFPrimaryColor:    row.PDFPrimaryColor,
		PDFFont:            row.PDFFont,
		OnboardingStep:     row.OnboardingStep,
		OnboardingComplete: row.OnboardingComplete,
		LegalName:          row.LegalName,
		Country:            row.Country,
		Timezone:           row.Timezone,
		BusinessRegNo:                   row.BusinessRegNo,
		TermsAcceptedAt:                 row.TermsAcceptedAt,
		PrivacyOfficerName:              row.PrivacyOfficerName,
		PrivacyOfficerEmail:             row.PrivacyOfficerEmail,
		PrivacyOfficerPhone:             row.PrivacyOfficerPhone,
		POTrainingAttestedAt:            row.POTrainingAttestedAt,
		CrossBorderAckAt:                row.CrossBorderAckAt,
		CrossBorderAckVersion:           row.CrossBorderAckVersion,
		MHRRegistered:                   row.MHRRegistered,
		AIOversightAckAt:                row.AIOversightAckAt,
		PatientConsentAckAt:             row.PatientConsentAckAt,
		DPAAcceptedAt:                   row.DPAAcceptedAt,
		DPAVersion:                      row.DPAVersion,
		ComplianceOnboardingCompletedAt: row.ComplianceOnboardingCompletedAt,
		ComplianceOnboardingVersion:     row.ComplianceOnboardingVersion,
		RegulatoryIDs:                   decodeRegulatoryIDs(row.RegulatoryIDs),
		CreatedAt:                       row.CreatedAt,
	}
}

var slugNonAlpha = regexp.MustCompile(`[^a-z0-9]+`)

// generateSlug creates a URL-safe ASCII slug from a clinic name.
// Unicode letters are decomposed (NFD) and their combining marks stripped so
// that e.g. "Wānaka" → "wanaka". Duplicates are handled at the DB level
// (UNIQUE constraint) — callers should append a random suffix on conflict.
func generateSlug(name string) string {
	// Strip diacritics: NFD decompose, remove non-spacing marks, re-encode.
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	ascii, _, _ := transform.String(t, name)

	lower := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return '-'
	}, ascii)
	slug := slugNonAlpha.ReplaceAllString(lower, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = slug[:60]
	}
	return slug
}
