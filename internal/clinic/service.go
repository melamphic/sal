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
	CreatedAt          time.Time           `json:"created_at"`
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

// GetByID returns decrypted clinic details for the authenticated clinic.
func (s *Service) GetByID(ctx context.Context, clinicID uuid.UUID) (*ClinicResponse, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.GetByID: %w", err)
	}

	return s.decryptAndBuild(ctx, row)
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
		CreatedAt:          row.CreatedAt,
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
