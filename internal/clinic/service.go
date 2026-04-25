package clinic

import (
	"context"
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
	repo         repo // interface — see repo.go
	cipher       *crypto.Cipher
	bootstrapper AdminBootstrapper // nil = skip (for tests that don't need the full flow)
	logoSigner   LogoSigner        // nil = skip URL signing (logo_url omitted)
	logoUploader LogoUploader      // nil = logo upload disabled
}

// NewService creates a new clinic Service.
func NewService(repo repo, cipher *crypto.Cipher, bootstrapper AdminBootstrapper, signer LogoSigner, uploader LogoUploader) *Service {
	return &Service{repo: repo, cipher: cipher, bootstrapper: bootstrapper, logoSigner: signer, logoUploader: uploader}
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
	CreatedAt                       time.Time  `json:"created_at"`
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
			TrialEndsAt: domain.TimeNow().Add(14 * 24 * time.Hour),
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
			TrialEndsAt:      domain.TimeNow().Add(14 * 24 * time.Hour),
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

// GetByID returns decrypted clinic details for the authenticated clinic.
func (s *Service) GetByID(ctx context.Context, clinicID uuid.UUID) (*ClinicResponse, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.GetByID: %w", err)
	}

	return s.decryptAndBuild(ctx, row)
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
