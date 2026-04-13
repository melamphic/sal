package patient

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/crypto"
)

// Service handles all business logic for the patient module.
// It has no knowledge of HTTP — inputs and outputs are plain Go types.
type Service struct {
	repo   repo
	cipher *crypto.Cipher
}

// NewService creates a new patient Service.
func NewService(r repo, cipher *crypto.Cipher) *Service {
	return &Service{repo: r, cipher: cipher}
}

// ── Response types ────────────────────────────────────────────────────────────

// ContactResponse is the decrypted, API-safe representation of a contact.
type ContactResponse struct {
	ID        string  `json:"id"`
	ClinicID  string  `json:"clinic_id"`
	FullName  string  `json:"full_name"`
	Phone     *string `json:"phone,omitempty"`
	Email     *string `json:"email,omitempty"`
	Address   *string `json:"address,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// VetDetailsResponse is the API-safe representation of vet subject details.
type VetDetailsResponse struct {
	Species     domain.VetSpecies `json:"species"`
	Breed       *string           `json:"breed,omitempty"`
	Sex         *domain.VetSex    `json:"sex,omitempty"`
	Desexed     *bool             `json:"desexed,omitempty"`
	DateOfBirth *string           `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	Color       *string           `json:"color,omitempty"`
	Microchip   *string           `json:"microchip,omitempty"`
	WeightKg    *float64          `json:"weight_kg,omitempty"`
}

// SubjectResponse is the decrypted, API-safe representation of a subject
// with its contact and vertical-specific details inline.
//
//nolint:revive
type SubjectResponse struct {
	ID          string               `json:"id"`
	ClinicID    string               `json:"clinic_id"`
	DisplayName string               `json:"display_name"`
	Status      domain.SubjectStatus `json:"status"`
	Vertical    domain.Vertical      `json:"vertical"`
	Contact     *ContactResponse     `json:"contact,omitempty"`
	VetDetails  *VetDetailsResponse  `json:"vet_details,omitempty"`
	CreatedBy   string               `json:"created_by"`
	CreatedAt   string               `json:"created_at"`
	UpdatedAt   string               `json:"updated_at"`
}

// SubjectListResponse is a paginated list of subjects.
//
//nolint:revive
type SubjectListResponse struct {
	Items  []*SubjectResponse `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// ContactListResponse is a paginated list of contacts.
//
//nolint:revive
type ContactListResponse struct {
	Items  []*ContactResponse `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// ContactWithSubjectsResponse is a contact with all of its subjects inline.
//
//nolint:revive
type ContactWithSubjectsResponse struct {
	*ContactResponse
	Subjects []*SubjectResponse `json:"subjects"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateContactInput holds validated input for creating a contact.
type CreateContactInput struct {
	ClinicID uuid.UUID
	FullName string
	Phone    *string
	Email    *string
	Address  *string
}

// UpdateContactInput holds validated input for updating a contact.
type UpdateContactInput struct {
	FullName *string
	Phone    *string
	Email    *string
	Address  *string
}

// CreateSubjectInput holds validated input for creating a subject.
// For vet vertical, VetDetails must be provided.
type CreateSubjectInput struct {
	ClinicID    uuid.UUID
	CallerID    uuid.UUID
	Vertical    domain.Vertical
	DisplayName string
	ContactID   *uuid.UUID // optional — can be linked later
	VetDetails  *VetDetailsInput
}

// VetDetailsInput holds vet-specific fields for subject creation/update.
type VetDetailsInput struct {
	Species     domain.VetSpecies
	Breed       *string
	Sex         *domain.VetSex
	Desexed     *bool
	DateOfBirth *time.Time
	Color       *string
	Microchip   *string
	WeightKg    *float64
}

// UpdateSubjectInput holds validated input for updating a subject.
type UpdateSubjectInput struct {
	DisplayName *string
	Status      *domain.SubjectStatus
	VetDetails  *UpdateVetDetailsInput
}

// UpdateVetDetailsInput holds vet-specific fields for a partial update.
type UpdateVetDetailsInput struct {
	Breed       *string
	Sex         *domain.VetSex
	Desexed     *bool
	DateOfBirth *time.Time
	Color       *string
	Microchip   *string
	WeightKg    *float64
}

// ListSubjectsInput holds filter + pagination parameters for listing subjects.
type ListSubjectsInput struct {
	Limit     int
	Offset    int
	Status    *domain.SubjectStatus
	Species   *domain.VetSpecies
	ContactID *uuid.UUID
	// ViewAll: caller has view_all_patients — no ownership filter is applied.
	ViewAll bool
	// OwnerScope: caller has view_own_patients but not view_all_patients.
	// When true, only subjects where created_by = CallerID are returned.
	OwnerScope bool
	CallerID   uuid.UUID
}

// ── Contact methods ───────────────────────────────────────────────────────────

// CreateContact encrypts PII and inserts a new contact.
func (s *Service) CreateContact(ctx context.Context, input CreateContactInput) (*ContactResponse, error) {
	encName, err := s.cipher.Encrypt(input.FullName)
	if err != nil {
		return nil, fmt.Errorf("patient.service.CreateContact: encrypt name: %w", err)
	}

	p := CreateContactParams{
		ID:       domain.NewID(),
		ClinicID: input.ClinicID,
		FullName: encName,
	}

	if input.Phone != nil {
		enc, err := s.cipher.Encrypt(*input.Phone)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateContact: encrypt phone: %w", err)
		}
		p.Phone = &enc
	}
	if input.Email != nil {
		enc, err := s.cipher.Encrypt(*input.Email)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateContact: encrypt email: %w", err)
		}
		hash := s.cipher.Hash(*input.Email)
		p.Email = &enc
		p.EmailHash = &hash
	}
	if input.Address != nil {
		enc, err := s.cipher.Encrypt(*input.Address)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateContact: encrypt address: %w", err)
		}
		p.Address = &enc
	}

	rec, err := s.repo.CreateContact(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("patient.service.CreateContact: %w", err)
	}

	return s.decryptContact(rec)
}

// GetContactByID fetches and decrypts a contact.
func (s *Service) GetContactByID(ctx context.Context, id, clinicID uuid.UUID) (*ContactResponse, error) {
	rec, err := s.repo.GetContactByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetContactByID: %w", err)
	}
	return s.decryptContact(rec)
}

// GetContactWithSubjects fetches a contact and all of its active subjects.
func (s *Service) GetContactWithSubjects(ctx context.Context, id, clinicID uuid.UUID) (*ContactWithSubjectsResponse, error) {
	rec, err := s.repo.GetContactByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetContactWithSubjects: %w", err)
	}

	contactDTO, err := s.decryptContact(rec)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetContactWithSubjects: %w", err)
	}

	rows, err := s.repo.ListSubjectsByContact(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetContactWithSubjects: %w", err)
	}

	subjects := make([]*SubjectResponse, 0, len(rows))
	for _, row := range rows {
		dto, err := s.decryptSubject(row)
		if err != nil {
			return nil, fmt.Errorf("patient.service.GetContactWithSubjects: %w", err)
		}
		subjects = append(subjects, dto)
	}

	return &ContactWithSubjectsResponse{
		ContactResponse: contactDTO,
		Subjects:        subjects,
	}, nil
}

// ListContacts returns a paginated, decrypted list of contacts for a clinic.
func (s *Service) ListContacts(ctx context.Context, clinicID uuid.UUID, limit, offset int) (*ContactListResponse, error) {
	limit = clampLimit(limit)
	recs, total, err := s.repo.ListContacts(ctx, clinicID, ListParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("patient.service.ListContacts: %w", err)
	}

	items := make([]*ContactResponse, 0, len(recs))
	for _, rec := range recs {
		dto, err := s.decryptContact(rec)
		if err != nil {
			return nil, fmt.Errorf("patient.service.ListContacts: %w", err)
		}
		items = append(items, dto)
	}

	return &ContactListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// UpdateContact encrypts changed PII fields and applies a partial update.
func (s *Service) UpdateContact(ctx context.Context, id, clinicID uuid.UUID, input UpdateContactInput) (*ContactResponse, error) {
	p := UpdateContactParams{}

	if input.FullName != nil {
		enc, err := s.cipher.Encrypt(*input.FullName)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateContact: encrypt name: %w", err)
		}
		p.FullName = &enc
	}
	if input.Phone != nil {
		enc, err := s.cipher.Encrypt(*input.Phone)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateContact: encrypt phone: %w", err)
		}
		p.Phone = &enc
	}
	if input.Email != nil {
		enc, err := s.cipher.Encrypt(*input.Email)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateContact: encrypt email: %w", err)
		}
		hash := s.cipher.Hash(*input.Email)
		p.Email = &enc
		p.EmailHash = &hash
	}
	if input.Address != nil {
		enc, err := s.cipher.Encrypt(*input.Address)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateContact: encrypt address: %w", err)
		}
		p.Address = &enc
	}

	rec, err := s.repo.UpdateContact(ctx, id, clinicID, p)
	if err != nil {
		return nil, fmt.Errorf("patient.service.UpdateContact: %w", err)
	}
	return s.decryptContact(rec)
}

// ── Subject methods ───────────────────────────────────────────────────────────

// CreateSubject creates a subject and its vertical extension row in one call.
// For vet vertical, VetDetails is required.
func (s *Service) CreateSubject(ctx context.Context, input CreateSubjectInput) (*SubjectResponse, error) {
	if input.Vertical == domain.VerticalVeterinary && input.VetDetails == nil {
		return nil, fmt.Errorf("patient.service.CreateSubject: %w", domain.ErrValidation)
	}

	subjectID := domain.NewID()

	subjectRec, err := s.repo.CreateSubject(ctx, CreateSubjectParams{
		ID:          subjectID,
		ClinicID:    input.ClinicID,
		ContactID:   input.ContactID,
		DisplayName: input.DisplayName,
		Status:      domain.SubjectStatusActive,
		Vertical:    input.Vertical,
		CreatedBy:   input.CallerID,
	})
	if err != nil {
		return nil, fmt.Errorf("patient.service.CreateSubject: %w", err)
	}

	var vetDetails *VetDetailsRecord
	if input.VetDetails != nil {
		vetDetails, err = s.repo.CreateVetDetails(ctx, CreateVetDetailsParams{
			SubjectID:   subjectID,
			Species:     input.VetDetails.Species,
			Breed:       input.VetDetails.Breed,
			Sex:         input.VetDetails.Sex,
			Desexed:     input.VetDetails.Desexed,
			DateOfBirth: input.VetDetails.DateOfBirth,
			Color:       input.VetDetails.Color,
			Microchip:   input.VetDetails.Microchip,
			WeightKg:    input.VetDetails.WeightKg,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: vet details: %w", err)
		}
	}

	// Fetch contact if linked so the response is complete.
	var contactRec *ContactRecord
	if input.ContactID != nil {
		contactRec, err = s.repo.GetContactByID(ctx, *input.ContactID, input.ClinicID)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: fetch contact: %w", err)
		}
	}

	return s.decryptSubject(&SubjectRow{
		Subject:    *subjectRec,
		Contact:    contactRec,
		VetDetails: vetDetails,
	})
}

// GetSubjectByID fetches and decrypts a subject with its contact and vet details.
// Enforces view_own_patients scope when the caller does not have view_all_patients.
func (s *Service) GetSubjectByID(ctx context.Context, id, clinicID, callerID uuid.UUID, viewAll bool) (*SubjectResponse, error) {
	row, err := s.repo.GetSubjectByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetSubjectByID: %w", err)
	}

	// Enforce view_own_patients: only the creator can see it.
	if !viewAll && row.Subject.CreatedBy != callerID {
		return nil, fmt.Errorf("patient.service.GetSubjectByID: %w", domain.ErrNotFound)
	}

	return s.decryptSubject(row)
}

// ListSubjects returns a paginated, decrypted list of subjects with filters.
// Returns ErrForbidden if the caller has neither view_all_patients nor view_own_patients.
func (s *Service) ListSubjects(ctx context.Context, clinicID uuid.UUID, input ListSubjectsInput) (*SubjectListResponse, error) {
	if !input.ViewAll && !input.OwnerScope {
		return nil, fmt.Errorf("patient.service.ListSubjects: %w", domain.ErrForbidden)
	}

	input.Limit = clampLimit(input.Limit)

	p := ListSubjectsParams{
		Limit:     input.Limit,
		Offset:    input.Offset,
		Status:    input.Status,
		Species:   input.Species,
		ContactID: input.ContactID,
	}

	// Apply own-patient scope at the repo layer.
	if input.OwnerScope && !input.ViewAll {
		p.CreatedBy = &input.CallerID
	}

	rows, total, err := s.repo.ListSubjects(ctx, clinicID, p)
	if err != nil {
		return nil, fmt.Errorf("patient.service.ListSubjects: %w", err)
	}

	items := make([]*SubjectResponse, 0, len(rows))
	for _, row := range rows {
		dto, err := s.decryptSubject(row)
		if err != nil {
			return nil, fmt.Errorf("patient.service.ListSubjects: %w", err)
		}
		items = append(items, dto)
	}

	return &SubjectListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// UpdateSubject applies a partial update to a subject and optionally its vet details.
func (s *Service) UpdateSubject(ctx context.Context, id, clinicID uuid.UUID, input UpdateSubjectInput) (*SubjectResponse, error) {
	_, err := s.repo.UpdateSubject(ctx, id, clinicID, UpdateSubjectParams{
		DisplayName: input.DisplayName,
		Status:      input.Status,
	})
	if err != nil {
		return nil, fmt.Errorf("patient.service.UpdateSubject: %w", err)
	}

	if input.VetDetails != nil {
		_, err = s.repo.UpdateVetDetails(ctx, id, UpdateVetDetailsParams{
			Breed:       input.VetDetails.Breed,
			Sex:         input.VetDetails.Sex,
			Desexed:     input.VetDetails.Desexed,
			DateOfBirth: input.VetDetails.DateOfBirth,
			Color:       input.VetDetails.Color,
			Microchip:   input.VetDetails.Microchip,
			WeightKg:    input.VetDetails.WeightKg,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: vet details: %w", err)
		}
	}

	// Re-fetch so the response has the fully joined row.
	row, err := s.repo.GetSubjectByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.UpdateSubject: refetch: %w", err)
	}
	return s.decryptSubject(row)
}

// LinkContact links a contact to a subject that was created without one.
func (s *Service) LinkContact(ctx context.Context, subjectID, clinicID, contactID uuid.UUID) (*SubjectResponse, error) {
	// Verify the contact belongs to this clinic before linking.
	if _, err := s.repo.GetContactByID(ctx, contactID, clinicID); err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: contact: %w", err)
	}

	if _, err := s.repo.LinkContact(ctx, subjectID, clinicID, contactID); err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: %w", err)
	}

	row, err := s.repo.GetSubjectByID(ctx, subjectID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: refetch: %w", err)
	}
	return s.decryptSubject(row)
}

// ArchiveSubject soft-deletes a subject.
func (s *Service) ArchiveSubject(ctx context.Context, id, clinicID uuid.UUID) (*SubjectResponse, error) {
	rec, err := s.repo.ArchiveSubject(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.ArchiveSubject: %w", err)
	}
	return s.decryptSubject(&SubjectRow{Subject: *rec})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Service) decryptContact(rec *ContactRecord) (*ContactResponse, error) {
	name, err := s.cipher.Decrypt(rec.FullName)
	if err != nil {
		return nil, fmt.Errorf("patient.service.decryptContact: name: %w", err)
	}

	dto := &ContactResponse{
		ID:        rec.ID.String(),
		ClinicID:  rec.ClinicID.String(),
		FullName:  name,
		CreatedAt: rec.CreatedAt.Format(time.RFC3339),
		UpdatedAt: rec.UpdatedAt.Format(time.RFC3339),
	}

	if rec.Phone != nil {
		dec, err := s.cipher.Decrypt(*rec.Phone)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptContact: phone: %w", err)
		}
		dto.Phone = &dec
	}
	if rec.Email != nil {
		dec, err := s.cipher.Decrypt(*rec.Email)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptContact: email: %w", err)
		}
		dto.Email = &dec
	}
	if rec.Address != nil {
		dec, err := s.cipher.Decrypt(*rec.Address)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptContact: address: %w", err)
		}
		dto.Address = &dec
	}

	return dto, nil
}

func (s *Service) decryptSubject(row *SubjectRow) (*SubjectResponse, error) {
	dto := &SubjectResponse{
		ID:          row.Subject.ID.String(),
		ClinicID:    row.Subject.ClinicID.String(),
		DisplayName: row.Subject.DisplayName,
		Status:      row.Subject.Status,
		Vertical:    row.Subject.Vertical,
		CreatedBy:   row.Subject.CreatedBy.String(),
		CreatedAt:   row.Subject.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.Subject.UpdatedAt.Format(time.RFC3339),
	}

	if row.Contact != nil {
		contactDTO, err := s.decryptContact(row.Contact)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: contact: %w", err)
		}
		dto.Contact = contactDTO
	}

	if row.VetDetails != nil {
		v := row.VetDetails
		vd := &VetDetailsResponse{
			Species:   v.Species,
			Breed:     v.Breed,
			Sex:       v.Sex,
			Desexed:   v.Desexed,
			Color:     v.Color,
			Microchip: v.Microchip,
			WeightKg:  v.WeightKg,
		}
		if v.DateOfBirth != nil {
			s := v.DateOfBirth.Format("2006-01-02")
			vd.DateOfBirth = &s
		}
		dto.VetDetails = vd
	}

	return dto, nil
}

// clampLimit enforces the list limit to the range [1, 100] with a default of 20.
func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}
