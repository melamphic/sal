package clinic

import (
	"context"
	"errors"
	"fmt"
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

// Service handles all clinic business logic.
type Service struct {
	repo   repo // interface — see repo.go
	cipher *crypto.Cipher
}

// NewService creates a new clinic Service.
func NewService(repo repo, cipher *crypto.Cipher) *Service {
	return &Service{repo: repo, cipher: cipher}
}

// ClinicResponse is the decrypted, service-layer representation of a clinic.
// This is what handlers receive and what API responses are built from.
// The encrypted DB row is never exposed outside the service layer.
type ClinicResponse struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Slug        string              `json:"slug"`
	Email       string              `json:"email"`
	Phone       *string             `json:"phone,omitempty"`
	Address     *string             `json:"address,omitempty"`
	Vertical    domain.Vertical     `json:"vertical"`
	Status      domain.ClinicStatus `json:"status"`
	TrialEndsAt time.Time           `json:"trial_ends_at"`
	NoteCount   int                 `json:"note_count"`
	NoteCap     *int                `json:"note_cap,omitempty"`
	DataRegion  string              `json:"data_region"`
	CreatedAt   time.Time           `json:"created_at"`
}

// RegisterInput holds the data required to register a new clinic.
type RegisterInput struct {
	Name       string
	Email      string
	Phone      *string
	Address    *string
	Vertical   domain.Vertical
	DataRegion string
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

	p := CreateParams{
		ID:          domain.NewID(),
		Name:        in.Name,
		Slug:        generateSlug(in.Name),
		Email:       encEmail,
		EmailHash:   emailHash,
		Phone:       encPhone,
		Address:     encAddress,
		Vertical:    vertical,
		TrialEndsAt: domain.TimeNow().Add(14 * 24 * time.Hour),
		DataRegion:  dataRegion,
	}

	row, err := s.repo.Create(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.Register: create: %w", err)
	}

	return s.toDTO(row, in.Email, in.Phone, in.Address), nil
}

// GetByID returns decrypted clinic details for the authenticated clinic.
func (s *Service) GetByID(ctx context.Context, clinicID uuid.UUID) (*ClinicResponse, error) {
	row, err := s.repo.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("clinic.service.GetByID: %w", err)
	}

	return s.decryptAndBuild(row)
}

// UpdateInput holds optional update fields. Nil = leave unchanged.
type UpdateInput struct {
	Name    *string
	Phone   *string
	Address *string
}

// Update applies a partial update to the clinic settings.
func (s *Service) Update(ctx context.Context, clinicID uuid.UUID, in UpdateInput) (*ClinicResponse, error) {
	p := UpdateParams{Name: in.Name}

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

	return s.decryptAndBuild(row)
}

// ── Private helpers ───────────────────────────────────────────────────────────

func (s *Service) decryptAndBuild(row *Clinic) (*ClinicResponse, error) {
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

	return s.toDTO(row, email, phone, address), nil
}

func (s *Service) toDTO(row *Clinic, email string, phone, address *string) *ClinicResponse {
	return &ClinicResponse{
		ID:          row.ID.String(),
		Name:        row.Name,
		Slug:        row.Slug,
		Email:       email,
		Phone:       phone,
		Address:     address,
		Vertical:    row.Vertical,
		Status:      row.Status,
		TrialEndsAt: row.TrialEndsAt,
		NoteCount:   row.NoteCount,
		NoteCap:     row.NoteCap,
		DataRegion:  row.DataRegion,
		CreatedAt:   row.CreatedAt,
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
