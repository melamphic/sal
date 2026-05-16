package patient

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/crypto"
)

// PhotoUploader stores subject avatar bytes in object storage and returns
// both the persisted key (durable id, suitable for re-signing later) and
// a short-lived URL the frontend can render immediately. Implemented by
// platform/storage in app.go; nil during tests that don't exercise the
// upload path.
type PhotoUploader interface {
	UploadSubjectPhoto(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (key, url string, err error)
}

// PhotoSigner mints a short-lived signed download URL from a stored
// photo key. Implemented in app.go via platform/storage; called every
// time the service builds a SubjectResponse so photo URLs never expire
// from the client's perspective.
type PhotoSigner interface {
	SignSubjectPhoto(ctx context.Context, key string) (string, error)
}

// Service handles all business logic for the patient module.
// It has no knowledge of HTTP — inputs and outputs are plain Go types.
type Service struct {
	repo          repo
	cipher        *crypto.Cipher
	photoUploader PhotoUploader // nil = photo upload disabled
	photoSigner   PhotoSigner   // nil = serve raw stored URL (legacy rows)
}

// NewService creates a new patient Service.
func NewService(r repo, cipher *crypto.Cipher) *Service {
	return &Service{repo: r, cipher: cipher}
}

// SetPhotoUploader wires the object-storage adapter that backs
// `POST /api/v1/patients/upload-photo`. Optional — if absent, the
// endpoint returns a 503 so the frontend can fall back to the legacy
// URL field.
func (s *Service) SetPhotoUploader(u PhotoUploader) {
	s.photoUploader = u
}

// SetPhotoSigner wires the read-side adapter that turns a stored
// photo_key into a freshly signed URL on every subject read. Optional
// in tests; required in prod for new photos to display.
func (s *Service) SetPhotoSigner(p PhotoSigner) {
	s.photoSigner = p
}

// resolveSubjectPhotoURL returns the URL the client should render for
// a subject. Prefers a freshly signed URL from photo_key (durable);
// falls back to the legacy photo_url column for any row that predates
// the photo_key migration.
func (s *Service) resolveSubjectPhotoURL(ctx context.Context, key, legacyURL *string) *string {
	if key != nil && *key != "" && s.photoSigner != nil {
		signed, err := s.photoSigner.SignSubjectPhoto(ctx, *key)
		if err == nil && signed != "" {
			return &signed
		}
		// Signer error: fall through to legacy URL so the row still
		// renders something instead of going blank.
	}
	if legacyURL != nil && *legacyURL != "" {
		return legacyURL
	}
	return nil
}

// UploadSubjectPhoto streams [body] (already size-validated by the
// handler) into object storage and returns the persisted key plus a
// freshly-signed download URL. The caller is expected to write [url]
// into `subjects.photo_url` via Create / Update — this method does not
// touch the DB itself, mirroring the doc-theme logo upload pattern so
// the same uploaded photo can be reused across multiple draft subjects
// before commit.
func (s *Service) UploadSubjectPhoto(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (key, url string, err error) {
	if s.photoUploader == nil {
		return "", "", fmt.Errorf("patient.service.UploadSubjectPhoto: storage not configured")
	}
	k, u, err := s.photoUploader.UploadSubjectPhoto(ctx, clinicID, contentType, body, size)
	if err != nil {
		return "", "", fmt.Errorf("patient.service.UploadSubjectPhoto: %w", err)
	}
	return k, u, nil
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

// GeneralDetailsResponse is the API-safe representation of general_clinic subject details.
type GeneralDetailsResponse struct {
	DateOfBirth           *string            `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	Sex                   *domain.GeneralSex `json:"sex,omitempty"`
	MedicalAlerts         *string            `json:"medical_alerts,omitempty"`
	Medications           *string            `json:"medications,omitempty"`
	Allergies             *string            `json:"allergies,omitempty"`
	ChronicConditions     *string            `json:"chronic_conditions,omitempty"`
	AdmissionWarnings     *string            `json:"admission_warnings,omitempty"`
	InsuranceProviderName *string            `json:"insurance_provider_name,omitempty"`
	InsurancePolicyNumber *string            `json:"insurance_policy_number,omitempty"`
	ReferringProviderName *string            `json:"referring_provider_name,omitempty"`
	PrimaryProviderName   *string            `json:"primary_provider_name,omitempty"`
}

// AgedCareDetailsResponse is the API-safe representation of aged-care
// resident details. PII/PHI fields (NHI, Medicare, medications, etc.) are
// decrypted by the service before this DTO is returned.
type AgedCareDetailsResponse struct {
	DateOfBirth          *string                          `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	Sex                  *domain.AgedCareSex              `json:"sex,omitempty"`
	Room                 *string                          `json:"room,omitempty"`
	NHINumber            *string                          `json:"nhi_number,omitempty"`
	MedicareNumber       *string                          `json:"medicare_number,omitempty"`
	Ethnicity            *string                          `json:"ethnicity,omitempty"`
	PreferredLanguage    *string                          `json:"preferred_language,omitempty"`
	MedicalAlerts        *string                          `json:"medical_alerts,omitempty"`
	Medications          *string                          `json:"medications,omitempty"`
	Allergies            *string                          `json:"allergies,omitempty"`
	ChronicConditions    *string                          `json:"chronic_conditions,omitempty"`
	CognitiveStatus      *domain.AgedCareCognitiveStatus  `json:"cognitive_status,omitempty"`
	MobilityStatus       *domain.AgedCareMobilityStatus   `json:"mobility_status,omitempty"`
	ContinenceStatus     *domain.AgedCareContinenceStatus `json:"continence_status,omitempty"`
	DietNotes            *string                          `json:"diet_notes,omitempty"`
	AdvanceDirectiveFlag bool                             `json:"advance_directive_flag"`
	FundingLevel         *domain.AgedCareFundingLevel     `json:"funding_level,omitempty"`
	AdmissionDate        *string                          `json:"admission_date,omitempty"` // YYYY-MM-DD
	PrimaryGPName        *string                          `json:"primary_gp_name,omitempty"`
}

// DentalDetailsResponse is the API-safe representation of dental subject details.
type DentalDetailsResponse struct {
	DateOfBirth           *string           `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	Sex                   *domain.DentalSex `json:"sex,omitempty"`
	MedicalAlerts         *string           `json:"medical_alerts,omitempty"`
	Medications           *string           `json:"medications,omitempty"`
	Allergies             *string           `json:"allergies,omitempty"`
	ChronicConditions     *string           `json:"chronic_conditions,omitempty"`
	AdmissionWarnings     *string           `json:"admission_warnings,omitempty"`
	InsuranceProviderName *string           `json:"insurance_provider_name,omitempty"`
	InsurancePolicyNumber *string           `json:"insurance_policy_number,omitempty"`
	ReferringDentistName  *string           `json:"referring_dentist_name,omitempty"`
	PrimaryDentistName    *string           `json:"primary_dentist_name,omitempty"`
}

// VetDetailsResponse is the API-safe representation of vet subject details.
type VetDetailsResponse struct {
	Species               domain.VetSpecies `json:"species"`
	Breed                 *string           `json:"breed,omitempty"`
	Sex                   *domain.VetSex    `json:"sex,omitempty"`
	Desexed               *bool             `json:"desexed,omitempty"`
	DateOfBirth           *string           `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	Color                 *string           `json:"color,omitempty"`
	Microchip             *string           `json:"microchip,omitempty"`
	WeightKg              *float64          `json:"weight_kg,omitempty"`
	Allergies             *string           `json:"allergies,omitempty"`
	ChronicConditions     *string           `json:"chronic_conditions,omitempty"`
	AdmissionWarnings     *string           `json:"admission_warnings,omitempty"`
	InsuranceProviderName *string           `json:"insurance_provider_name,omitempty"`
	InsurancePolicyNumber *string           `json:"insurance_policy_number,omitempty"`
	ReferringVetName      *string           `json:"referring_vet_name,omitempty"`
}

// SubjectResponse is the decrypted, API-safe representation of a subject
// with its contact and vertical-specific details inline.
//
//nolint:revive
type SubjectResponse struct {
	ID              string                   `json:"id"`
	ClinicID        string                   `json:"clinic_id"`
	DisplayName     string                   `json:"display_name"`
	Status          domain.SubjectStatus     `json:"status"`
	Vertical        domain.Vertical          `json:"vertical"`
	PhotoURL        *string                  `json:"photo_url,omitempty"`
	Contact         *ContactResponse         `json:"contact,omitempty"`
	VetDetails      *VetDetailsResponse      `json:"vet_details,omitempty"`
	DentalDetails   *DentalDetailsResponse   `json:"dental_details,omitempty"`
	GeneralDetails  *GeneralDetailsResponse  `json:"general_details,omitempty"`
	AgedCareDetails *AgedCareDetailsResponse `json:"aged_care_details,omitempty"`
	CreatedBy       string                   `json:"created_by"`
	CreatedAt       string                   `json:"created_at"`
	UpdatedAt       string                   `json:"updated_at"`
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
// Each vertical requires its own details struct to be populated.
// PhotoKey is preferred over PhotoURL — new clients upload via
// `POST /patients/upload-photo`, get back a durable key, and send the
// key here. The service signs a URL on every read.
type CreateSubjectInput struct {
	ClinicID        uuid.UUID
	CallerID        uuid.UUID
	Vertical        domain.Vertical
	DisplayName     string
	PhotoURL        *string // legacy fallback
	PhotoKey        *string // preferred
	ContactID       *uuid.UUID // optional — can be linked later
	VetDetails      *VetDetailsInput
	DentalDetails   *DentalDetailsInput
	GeneralDetails  *GeneralDetailsInput
	AgedCareDetails *AgedCareDetailsInput
}

// VetDetailsInput holds vet-specific fields for subject creation/update.
// Allergies, ChronicConditions, and InsurancePolicyNumber arrive as plaintext
// and are encrypted by the service before reaching the repository.
type VetDetailsInput struct {
	Species               domain.VetSpecies
	Breed                 *string
	Sex                   *domain.VetSex
	Desexed               *bool
	DateOfBirth           *time.Time
	Color                 *string
	Microchip             *string
	WeightKg              *float64
	Allergies             *string
	ChronicConditions     *string
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string
	ReferringVetName      *string
}

// GeneralDetailsInput holds general_clinic-specific fields for subject creation.
// Encrypted fields arrive as plaintext and are encrypted by the service.
type GeneralDetailsInput struct {
	DateOfBirth           *time.Time
	Sex                   *domain.GeneralSex
	MedicalAlerts         *string
	Medications           *string
	Allergies             *string
	ChronicConditions     *string
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string
	ReferringProviderName *string
	PrimaryProviderName   *string
}

// DentalDetailsInput holds dental-specific fields for subject creation.
// Encrypted fields arrive as plaintext and are encrypted by the service.
type DentalDetailsInput struct {
	DateOfBirth           *time.Time
	Sex                   *domain.DentalSex
	MedicalAlerts         *string
	Medications           *string
	Allergies             *string
	ChronicConditions     *string
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string
	ReferringDentistName  *string
	PrimaryDentistName    *string
}

// AgedCareDetailsInput holds aged-care-specific fields for subject creation.
// Encrypted fields (NHINumber, MedicareNumber, MedicalAlerts, Medications,
// Allergies, ChronicConditions, DietNotes) arrive as plaintext and are
// encrypted by the service.
type AgedCareDetailsInput struct {
	DateOfBirth          *time.Time
	Sex                  *domain.AgedCareSex
	Room                 *string
	NHINumber            *string
	MedicareNumber       *string
	Ethnicity            *string
	PreferredLanguage    *string
	MedicalAlerts        *string
	Medications          *string
	Allergies            *string
	ChronicConditions    *string
	CognitiveStatus      *domain.AgedCareCognitiveStatus
	MobilityStatus       *domain.AgedCareMobilityStatus
	ContinenceStatus     *domain.AgedCareContinenceStatus
	DietNotes            *string
	AdvanceDirectiveFlag bool
	FundingLevel         *domain.AgedCareFundingLevel
	AdmissionDate        *time.Time
	PrimaryGPName        *string
}

// UpdateSubjectInput holds validated input for updating a subject.
type UpdateSubjectInput struct {
	DisplayName     *string
	Status          *domain.SubjectStatus
	PhotoURL        *string // legacy fallback
	PhotoKey        *string // preferred
	VetDetails      *UpdateVetDetailsInput
	DentalDetails   *UpdateDentalDetailsInput
	GeneralDetails  *UpdateGeneralDetailsInput
	AgedCareDetails *UpdateAgedCareDetailsInput
}

// UpdateGeneralDetailsInput holds general_clinic-specific fields for a partial update.
// Encrypted fields arrive as plaintext and are encrypted by the service.
type UpdateGeneralDetailsInput struct {
	DateOfBirth           *time.Time
	Sex                   *domain.GeneralSex
	MedicalAlerts         *string
	Medications           *string
	Allergies             *string
	ChronicConditions     *string
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string
	ReferringProviderName *string
	PrimaryProviderName   *string
}

// UpdateAgedCareDetailsInput holds aged-care-specific fields for a partial update.
// Encrypted fields arrive as plaintext and are encrypted by the service.
type UpdateAgedCareDetailsInput struct {
	DateOfBirth          *time.Time
	Sex                  *domain.AgedCareSex
	Room                 *string
	NHINumber            *string
	MedicareNumber       *string
	Ethnicity            *string
	PreferredLanguage    *string
	MedicalAlerts        *string
	Medications          *string
	Allergies            *string
	ChronicConditions    *string
	CognitiveStatus      *domain.AgedCareCognitiveStatus
	MobilityStatus       *domain.AgedCareMobilityStatus
	ContinenceStatus     *domain.AgedCareContinenceStatus
	DietNotes            *string
	AdvanceDirectiveFlag *bool
	FundingLevel         *domain.AgedCareFundingLevel
	AdmissionDate        *time.Time
	PrimaryGPName        *string
}

// UpdateDentalDetailsInput holds dental-specific fields for a partial update.
// Encrypted fields arrive as plaintext and are encrypted by the service.
type UpdateDentalDetailsInput struct {
	DateOfBirth           *time.Time
	Sex                   *domain.DentalSex
	MedicalAlerts         *string
	Medications           *string
	Allergies             *string
	ChronicConditions     *string
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string
	ReferringDentistName  *string
	PrimaryDentistName    *string
}

// UpdateVetDetailsInput holds vet-specific fields for a partial update.
// Encrypted fields arrive as plaintext and are encrypted by the service.
type UpdateVetDetailsInput struct {
	Breed                 *string
	Sex                   *domain.VetSex
	Desexed               *bool
	DateOfBirth           *time.Time
	Color                 *string
	Microchip             *string
	WeightKg              *float64
	Allergies             *string
	ChronicConditions     *string
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string
	ReferringVetName      *string
}

// ListSubjectsInput holds filter + pagination parameters for listing subjects.
type ListSubjectsInput struct {
	Limit     int
	Offset    int
	Status    *domain.SubjectStatus
	Species   *domain.VetSpecies
	ContactID *uuid.UUID
	Search    *string
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
		dto, err := s.decryptSubject(ctx, row)
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

// LookupSubjectNames batches a display-name fetch for the given subject
// ids. Empty input returns an empty map. Missing/archived ids are simply
// absent from the result. Intended for cross-domain feeds (e.g. staff
// activity) that need to decorate subject IDs with human-readable names.
func (s *Service) LookupSubjectNames(ctx context.Context, clinicID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out, err := s.repo.LookupDisplayNames(ctx, clinicID, ids)
	if err != nil {
		return nil, fmt.Errorf("patient.service.LookupSubjectNames: %w", err)
	}
	return out, nil
}

// ArchiveContact soft-deletes a contact. Fails with ErrConflict if the
// contact is still linked to any active subject.
func (s *Service) ArchiveContact(ctx context.Context, id, clinicID uuid.UUID) (*ContactResponse, error) {
	rec, err := s.repo.ArchiveContact(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.ArchiveContact: %w", err)
	}
	return s.decryptContact(rec)
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
	if input.Vertical == domain.VerticalDental && input.DentalDetails == nil {
		return nil, fmt.Errorf("patient.service.CreateSubject: %w", domain.ErrValidation)
	}
	if input.Vertical == domain.VerticalGeneralClinic && input.GeneralDetails == nil {
		return nil, fmt.Errorf("patient.service.CreateSubject: %w", domain.ErrValidation)
	}
	if input.Vertical == domain.VerticalAgedCare && input.AgedCareDetails == nil {
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
		PhotoURL:    input.PhotoURL,
		PhotoKey:    input.PhotoKey,
		CreatedBy:   input.CallerID,
	})
	if err != nil {
		return nil, fmt.Errorf("patient.service.CreateSubject: %w", err)
	}

	var vetDetails *VetDetailsRecord
	if input.VetDetails != nil {
		encAllergies, err := s.encryptOpt(input.VetDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt allergies: %w", err)
		}
		encChronic, err := s.encryptOpt(input.VetDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt chronic_conditions: %w", err)
		}
		encPolicy, err := s.encryptOpt(input.VetDetails.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt insurance_policy_number: %w", err)
		}

		vetDetails, err = s.repo.CreateVetDetails(ctx, CreateVetDetailsParams{
			SubjectID:             subjectID,
			Species:               input.VetDetails.Species,
			Breed:                 input.VetDetails.Breed,
			Sex:                   input.VetDetails.Sex,
			Desexed:               input.VetDetails.Desexed,
			DateOfBirth:           input.VetDetails.DateOfBirth,
			Color:                 input.VetDetails.Color,
			Microchip:             input.VetDetails.Microchip,
			WeightKg:              input.VetDetails.WeightKg,
			Allergies:             encAllergies,
			ChronicConditions:     encChronic,
			AdmissionWarnings:     input.VetDetails.AdmissionWarnings,
			InsuranceProviderName: input.VetDetails.InsuranceProviderName,
			InsurancePolicyNumber: encPolicy,
			ReferringVetName:      input.VetDetails.ReferringVetName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: vet details: %w", err)
		}
	}

	var dentalDetails *DentalDetailsRecord
	if input.DentalDetails != nil {
		encMedAlerts, err := s.encryptOpt(input.DentalDetails.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt medical_alerts: %w", err)
		}
		encMeds, err := s.encryptOpt(input.DentalDetails.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt medications: %w", err)
		}
		encDAllergies, err := s.encryptOpt(input.DentalDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt dental allergies: %w", err)
		}
		encDChronic, err := s.encryptOpt(input.DentalDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt dental chronic_conditions: %w", err)
		}
		encDPolicy, err := s.encryptOpt(input.DentalDetails.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt dental insurance_policy_number: %w", err)
		}

		dentalDetails, err = s.repo.CreateDentalDetails(ctx, CreateDentalDetailsParams{
			SubjectID:             subjectID,
			DateOfBirth:           input.DentalDetails.DateOfBirth,
			Sex:                   input.DentalDetails.Sex,
			MedicalAlerts:         encMedAlerts,
			Medications:           encMeds,
			Allergies:             encDAllergies,
			ChronicConditions:     encDChronic,
			AdmissionWarnings:     input.DentalDetails.AdmissionWarnings,
			InsuranceProviderName: input.DentalDetails.InsuranceProviderName,
			InsurancePolicyNumber: encDPolicy,
			ReferringDentistName:  input.DentalDetails.ReferringDentistName,
			PrimaryDentistName:    input.DentalDetails.PrimaryDentistName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: dental details: %w", err)
		}
	}

	var generalDetails *GeneralDetailsRecord
	if input.GeneralDetails != nil {
		encMedAlerts, err := s.encryptOpt(input.GeneralDetails.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt general medical_alerts: %w", err)
		}
		encMeds, err := s.encryptOpt(input.GeneralDetails.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt general medications: %w", err)
		}
		encGAllergies, err := s.encryptOpt(input.GeneralDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt general allergies: %w", err)
		}
		encGChronic, err := s.encryptOpt(input.GeneralDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt general chronic_conditions: %w", err)
		}
		encGPolicy, err := s.encryptOpt(input.GeneralDetails.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt general insurance_policy_number: %w", err)
		}

		generalDetails, err = s.repo.CreateGeneralDetails(ctx, CreateGeneralDetailsParams{
			SubjectID:             subjectID,
			DateOfBirth:           input.GeneralDetails.DateOfBirth,
			Sex:                   input.GeneralDetails.Sex,
			MedicalAlerts:         encMedAlerts,
			Medications:           encMeds,
			Allergies:             encGAllergies,
			ChronicConditions:     encGChronic,
			AdmissionWarnings:     input.GeneralDetails.AdmissionWarnings,
			InsuranceProviderName: input.GeneralDetails.InsuranceProviderName,
			InsurancePolicyNumber: encGPolicy,
			ReferringProviderName: input.GeneralDetails.ReferringProviderName,
			PrimaryProviderName:   input.GeneralDetails.PrimaryProviderName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: general details: %w", err)
		}
	}

	var agedCareDetails *AgedCareDetailsRecord
	if input.AgedCareDetails != nil {
		encNHI, err := s.encryptOpt(input.AgedCareDetails.NHINumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care nhi_number: %w", err)
		}
		encMedicare, err := s.encryptOpt(input.AgedCareDetails.MedicareNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care medicare_number: %w", err)
		}
		encACMedAlerts, err := s.encryptOpt(input.AgedCareDetails.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care medical_alerts: %w", err)
		}
		encACMeds, err := s.encryptOpt(input.AgedCareDetails.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care medications: %w", err)
		}
		encACAllergies, err := s.encryptOpt(input.AgedCareDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care allergies: %w", err)
		}
		encACChronic, err := s.encryptOpt(input.AgedCareDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care chronic_conditions: %w", err)
		}
		encACDiet, err := s.encryptOpt(input.AgedCareDetails.DietNotes)
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: encrypt aged-care diet_notes: %w", err)
		}

		agedCareDetails, err = s.repo.CreateAgedCareDetails(ctx, CreateAgedCareDetailsParams{
			SubjectID:            subjectID,
			DateOfBirth:          input.AgedCareDetails.DateOfBirth,
			Sex:                  input.AgedCareDetails.Sex,
			Room:                 input.AgedCareDetails.Room,
			NHINumber:            encNHI,
			MedicareNumber:       encMedicare,
			Ethnicity:            input.AgedCareDetails.Ethnicity,
			PreferredLanguage:    input.AgedCareDetails.PreferredLanguage,
			MedicalAlerts:        encACMedAlerts,
			Medications:          encACMeds,
			Allergies:            encACAllergies,
			ChronicConditions:    encACChronic,
			CognitiveStatus:      input.AgedCareDetails.CognitiveStatus,
			MobilityStatus:       input.AgedCareDetails.MobilityStatus,
			ContinenceStatus:     input.AgedCareDetails.ContinenceStatus,
			DietNotes:            encACDiet,
			AdvanceDirectiveFlag: input.AgedCareDetails.AdvanceDirectiveFlag,
			FundingLevel:         input.AgedCareDetails.FundingLevel,
			AdmissionDate:        input.AgedCareDetails.AdmissionDate,
			PrimaryGPName:        input.AgedCareDetails.PrimaryGPName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.CreateSubject: aged-care details: %w", err)
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

	if err := s.logAccess(ctx, subjectID, input.ClinicID, input.CallerID, domain.SubjectAccessActionCreate, nil); err != nil {
		return nil, fmt.Errorf("patient.service.CreateSubject: %w", err)
	}

	return s.decryptSubject(ctx, &SubjectRow{
		Subject:         *subjectRec,
		Contact:         contactRec,
		VetDetails:      vetDetails,
		DentalDetails:   dentalDetails,
		GeneralDetails:  generalDetails,
		AgedCareDetails: agedCareDetails,
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

	if err := s.logAccess(ctx, id, clinicID, callerID, domain.SubjectAccessActionView, nil); err != nil {
		return nil, fmt.Errorf("patient.service.GetSubjectByID: %w", err)
	}

	return s.decryptSubject(ctx, row)
}

// GetSubjectForRender fetches and decrypts a subject for system-driven
// rendering (PDF generation, scheduled exports). Bypasses access logging and
// caller-scoped checks because the calling code runs in a background worker
// after the human action has already been audited via the originating note's
// submission. Never expose this through an HTTP handler.
func (s *Service) GetSubjectForRender(ctx context.Context, id, clinicID uuid.UUID) (*SubjectResponse, error) {
	row, err := s.repo.GetSubjectByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetSubjectForRender: %w", err)
	}
	dto, err := s.decryptSubject(ctx, row)
	if err != nil {
		return nil, fmt.Errorf("patient.service.GetSubjectForRender: %w", err)
	}
	return dto, nil
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
		Search:    input.Search,
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
		dto, err := s.decryptSubject(ctx, row)
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
func (s *Service) UpdateSubject(ctx context.Context, id, clinicID, callerID uuid.UUID, input UpdateSubjectInput) (*SubjectResponse, error) {
	_, err := s.repo.UpdateSubject(ctx, id, clinicID, UpdateSubjectParams{
		DisplayName: input.DisplayName,
		Status:      input.Status,
		PhotoURL:    input.PhotoURL,
		PhotoKey:    input.PhotoKey,
	})
	if err != nil {
		return nil, fmt.Errorf("patient.service.UpdateSubject: %w", err)
	}

	if input.VetDetails != nil {
		encAllergies, err := s.encryptOpt(input.VetDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt allergies: %w", err)
		}
		encChronic, err := s.encryptOpt(input.VetDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt chronic_conditions: %w", err)
		}
		encPolicy, err := s.encryptOpt(input.VetDetails.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt insurance_policy_number: %w", err)
		}

		_, err = s.repo.UpdateVetDetails(ctx, id, UpdateVetDetailsParams{
			Breed:                 input.VetDetails.Breed,
			Sex:                   input.VetDetails.Sex,
			Desexed:               input.VetDetails.Desexed,
			DateOfBirth:           input.VetDetails.DateOfBirth,
			Color:                 input.VetDetails.Color,
			Microchip:             input.VetDetails.Microchip,
			WeightKg:              input.VetDetails.WeightKg,
			Allergies:             encAllergies,
			ChronicConditions:     encChronic,
			AdmissionWarnings:     input.VetDetails.AdmissionWarnings,
			InsuranceProviderName: input.VetDetails.InsuranceProviderName,
			InsurancePolicyNumber: encPolicy,
			ReferringVetName:      input.VetDetails.ReferringVetName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: vet details: %w", err)
		}
	}

	if input.DentalDetails != nil {
		encMedAlerts, err := s.encryptOpt(input.DentalDetails.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt medical_alerts: %w", err)
		}
		encMeds, err := s.encryptOpt(input.DentalDetails.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt medications: %w", err)
		}
		encDAllergies, err := s.encryptOpt(input.DentalDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt dental allergies: %w", err)
		}
		encDChronic, err := s.encryptOpt(input.DentalDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt dental chronic_conditions: %w", err)
		}
		encDPolicy, err := s.encryptOpt(input.DentalDetails.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt dental insurance_policy_number: %w", err)
		}

		_, err = s.repo.UpdateDentalDetails(ctx, id, UpdateDentalDetailsParams{
			DateOfBirth:           input.DentalDetails.DateOfBirth,
			Sex:                   input.DentalDetails.Sex,
			MedicalAlerts:         encMedAlerts,
			Medications:           encMeds,
			Allergies:             encDAllergies,
			ChronicConditions:     encDChronic,
			AdmissionWarnings:     input.DentalDetails.AdmissionWarnings,
			InsuranceProviderName: input.DentalDetails.InsuranceProviderName,
			InsurancePolicyNumber: encDPolicy,
			ReferringDentistName:  input.DentalDetails.ReferringDentistName,
			PrimaryDentistName:    input.DentalDetails.PrimaryDentistName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: dental details: %w", err)
		}
	}

	if input.GeneralDetails != nil {
		encMedAlerts, err := s.encryptOpt(input.GeneralDetails.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt general medical_alerts: %w", err)
		}
		encMeds, err := s.encryptOpt(input.GeneralDetails.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt general medications: %w", err)
		}
		encGAllergies, err := s.encryptOpt(input.GeneralDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt general allergies: %w", err)
		}
		encGChronic, err := s.encryptOpt(input.GeneralDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt general chronic_conditions: %w", err)
		}
		encGPolicy, err := s.encryptOpt(input.GeneralDetails.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt general insurance_policy_number: %w", err)
		}

		_, err = s.repo.UpdateGeneralDetails(ctx, id, UpdateGeneralDetailsParams{
			DateOfBirth:           input.GeneralDetails.DateOfBirth,
			Sex:                   input.GeneralDetails.Sex,
			MedicalAlerts:         encMedAlerts,
			Medications:           encMeds,
			Allergies:             encGAllergies,
			ChronicConditions:     encGChronic,
			AdmissionWarnings:     input.GeneralDetails.AdmissionWarnings,
			InsuranceProviderName: input.GeneralDetails.InsuranceProviderName,
			InsurancePolicyNumber: encGPolicy,
			ReferringProviderName: input.GeneralDetails.ReferringProviderName,
			PrimaryProviderName:   input.GeneralDetails.PrimaryProviderName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: general details: %w", err)
		}
	}

	if input.AgedCareDetails != nil {
		encNHI, err := s.encryptOpt(input.AgedCareDetails.NHINumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care nhi_number: %w", err)
		}
		encMedicare, err := s.encryptOpt(input.AgedCareDetails.MedicareNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care medicare_number: %w", err)
		}
		encACMedAlerts, err := s.encryptOpt(input.AgedCareDetails.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care medical_alerts: %w", err)
		}
		encACMeds, err := s.encryptOpt(input.AgedCareDetails.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care medications: %w", err)
		}
		encACAllergies, err := s.encryptOpt(input.AgedCareDetails.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care allergies: %w", err)
		}
		encACChronic, err := s.encryptOpt(input.AgedCareDetails.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care chronic_conditions: %w", err)
		}
		encACDiet, err := s.encryptOpt(input.AgedCareDetails.DietNotes)
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: encrypt aged-care diet_notes: %w", err)
		}

		_, err = s.repo.UpdateAgedCareDetails(ctx, id, UpdateAgedCareDetailsParams{
			DateOfBirth:          input.AgedCareDetails.DateOfBirth,
			Sex:                  input.AgedCareDetails.Sex,
			Room:                 input.AgedCareDetails.Room,
			NHINumber:            encNHI,
			MedicareNumber:       encMedicare,
			Ethnicity:            input.AgedCareDetails.Ethnicity,
			PreferredLanguage:    input.AgedCareDetails.PreferredLanguage,
			MedicalAlerts:        encACMedAlerts,
			Medications:          encACMeds,
			Allergies:            encACAllergies,
			ChronicConditions:    encACChronic,
			CognitiveStatus:      input.AgedCareDetails.CognitiveStatus,
			MobilityStatus:       input.AgedCareDetails.MobilityStatus,
			ContinenceStatus:     input.AgedCareDetails.ContinenceStatus,
			DietNotes:            encACDiet,
			AdvanceDirectiveFlag: input.AgedCareDetails.AdvanceDirectiveFlag,
			FundingLevel:         input.AgedCareDetails.FundingLevel,
			AdmissionDate:        input.AgedCareDetails.AdmissionDate,
			PrimaryGPName:        input.AgedCareDetails.PrimaryGPName,
		})
		if err != nil {
			return nil, fmt.Errorf("patient.service.UpdateSubject: aged-care details: %w", err)
		}
	}

	if err := s.logAccess(ctx, id, clinicID, callerID, domain.SubjectAccessActionUpdate, nil); err != nil {
		return nil, fmt.Errorf("patient.service.UpdateSubject: %w", err)
	}

	// Re-fetch so the response has the fully joined row.
	row, err := s.repo.GetSubjectByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.UpdateSubject: refetch: %w", err)
	}
	return s.decryptSubject(ctx, row)
}

// LinkContact links a contact to a subject that was created without one.
func (s *Service) LinkContact(ctx context.Context, subjectID, clinicID, contactID, callerID uuid.UUID) (*SubjectResponse, error) {
	// Verify the contact belongs to this clinic before linking.
	if _, err := s.repo.GetContactByID(ctx, contactID, clinicID); err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: contact: %w", err)
	}

	if _, err := s.repo.LinkContact(ctx, subjectID, clinicID, contactID); err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: %w", err)
	}

	if err := s.logAccess(ctx, subjectID, clinicID, callerID, domain.SubjectAccessActionUpdate, nil); err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: %w", err)
	}

	row, err := s.repo.GetSubjectByID(ctx, subjectID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.LinkContact: refetch: %w", err)
	}
	return s.decryptSubject(ctx, row)
}

// ArchiveSubject soft-deletes a subject.
func (s *Service) ArchiveSubject(ctx context.Context, id, clinicID, callerID uuid.UUID) (*SubjectResponse, error) {
	rec, err := s.repo.ArchiveSubject(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.ArchiveSubject: %w", err)
	}
	if err := s.logAccess(ctx, id, clinicID, callerID, domain.SubjectAccessActionArchive, nil); err != nil {
		return nil, fmt.Errorf("patient.service.ArchiveSubject: %w", err)
	}
	return s.decryptSubject(ctx, &SubjectRow{Subject: *rec})
}

// ── Subject ↔ contact link methods ────────────────────────────────────────────

// SubjectContactResponse is one (contact, role) binding for a subject with
// the contact already decrypted.
type SubjectContactResponse struct {
	Role    domain.SubjectContactRole `json:"role"`
	Note    *string                   `json:"note,omitempty"`
	Contact *ContactResponse          `json:"contact"`
}

// AddSubjectContactInput holds validated input for linking a contact to a
// subject with a specific role.
type AddSubjectContactInput struct {
	SubjectID uuid.UUID
	ClinicID  uuid.UUID
	ContactID uuid.UUID
	Role      domain.SubjectContactRole
	Note      *string
	CallerID  uuid.UUID
}

// AddSubjectContact inserts a (subject, contact, role) binding and audits.
func (s *Service) AddSubjectContact(ctx context.Context, in AddSubjectContactInput) (*SubjectContactResponse, error) {
	rec, err := s.repo.CreateSubjectContact(ctx, in.ClinicID, CreateSubjectContactParams{
		SubjectID: in.SubjectID,
		ContactID: in.ContactID,
		Role:      in.Role,
		Note:      in.Note,
	})
	if err != nil {
		return nil, fmt.Errorf("patient.service.AddSubjectContact: %w", err)
	}

	contactRec, err := s.repo.GetContactByID(ctx, in.ContactID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.AddSubjectContact: fetch contact: %w", err)
	}
	contactDTO, err := s.decryptContact(contactRec)
	if err != nil {
		return nil, fmt.Errorf("patient.service.AddSubjectContact: %w", err)
	}

	if err := s.logAccess(ctx, in.SubjectID, in.ClinicID, in.CallerID, domain.SubjectAccessActionUpdate, nil); err != nil {
		return nil, fmt.Errorf("patient.service.AddSubjectContact: %w", err)
	}

	return &SubjectContactResponse{
		Role:    rec.Role,
		Note:    rec.Note,
		Contact: contactDTO,
	}, nil
}

// RemoveSubjectContact deletes a single (subject, contact, role) binding.
func (s *Service) RemoveSubjectContact(
	ctx context.Context,
	subjectID, clinicID, contactID, callerID uuid.UUID,
	role domain.SubjectContactRole,
) error {
	if err := s.repo.DeleteSubjectContact(ctx, clinicID, subjectID, contactID, role); err != nil {
		return fmt.Errorf("patient.service.RemoveSubjectContact: %w", err)
	}
	if err := s.logAccess(ctx, subjectID, clinicID, callerID, domain.SubjectAccessActionUpdate, nil); err != nil {
		return fmt.Errorf("patient.service.RemoveSubjectContact: %w", err)
	}
	return nil
}

// ListSubjectContacts returns all decrypted contact bindings for a subject.
func (s *Service) ListSubjectContacts(ctx context.Context, subjectID, clinicID uuid.UUID) ([]*SubjectContactResponse, error) {
	links, err := s.repo.ListSubjectContacts(ctx, clinicID, subjectID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.ListSubjectContacts: %w", err)
	}

	out := make([]*SubjectContactResponse, 0, len(links))
	for _, l := range links {
		dto, err := s.decryptContact(l.Contact)
		if err != nil {
			return nil, fmt.Errorf("patient.service.ListSubjectContacts: %w", err)
		}
		out = append(out, &SubjectContactResponse{
			Role:    l.Role,
			Note:    l.Note,
			Contact: dto,
		})
	}
	return out, nil
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

func (s *Service) decryptSubject(ctx context.Context, row *SubjectRow) (*SubjectResponse, error) {
	dto := &SubjectResponse{
		ID:          row.Subject.ID.String(),
		ClinicID:    row.Subject.ClinicID.String(),
		DisplayName: row.Subject.DisplayName,
		Status:      row.Subject.Status,
		Vertical:    row.Subject.Vertical,
		PhotoURL: s.resolveSubjectPhotoURL(
			ctx,
			row.Subject.PhotoKey,
			row.Subject.PhotoURL,
		),
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
			Species:               v.Species,
			Breed:                 v.Breed,
			Sex:                   v.Sex,
			Desexed:               v.Desexed,
			Color:                 v.Color,
			Microchip:             v.Microchip,
			WeightKg:              v.WeightKg,
			AdmissionWarnings:     v.AdmissionWarnings,
			InsuranceProviderName: v.InsuranceProviderName,
			ReferringVetName:      v.ReferringVetName,
		}
		if v.DateOfBirth != nil {
			s := v.DateOfBirth.Format("2006-01-02")
			vd.DateOfBirth = &s
		}
		decAllergies, err := s.decryptOpt(v.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: allergies: %w", err)
		}
		vd.Allergies = decAllergies
		decChronic, err := s.decryptOpt(v.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: chronic_conditions: %w", err)
		}
		vd.ChronicConditions = decChronic
		decPolicy, err := s.decryptOpt(v.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: insurance_policy_number: %w", err)
		}
		vd.InsurancePolicyNumber = decPolicy
		dto.VetDetails = vd
	}

	if row.DentalDetails != nil {
		dd := row.DentalDetails
		dr := &DentalDetailsResponse{
			Sex:                   dd.Sex,
			AdmissionWarnings:     dd.AdmissionWarnings,
			InsuranceProviderName: dd.InsuranceProviderName,
			ReferringDentistName:  dd.ReferringDentistName,
			PrimaryDentistName:    dd.PrimaryDentistName,
		}
		if dd.DateOfBirth != nil {
			str := dd.DateOfBirth.Format("2006-01-02")
			dr.DateOfBirth = &str
		}
		decMedAlerts, err := s.decryptOpt(dd.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: medical_alerts: %w", err)
		}
		dr.MedicalAlerts = decMedAlerts
		decMeds, err := s.decryptOpt(dd.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: medications: %w", err)
		}
		dr.Medications = decMeds
		decDAllergies, err := s.decryptOpt(dd.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: dental allergies: %w", err)
		}
		dr.Allergies = decDAllergies
		decDChronic, err := s.decryptOpt(dd.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: dental chronic_conditions: %w", err)
		}
		dr.ChronicConditions = decDChronic
		decDPolicy, err := s.decryptOpt(dd.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: dental insurance_policy_number: %w", err)
		}
		dr.InsurancePolicyNumber = decDPolicy
		dto.DentalDetails = dr
	}

	if row.GeneralDetails != nil {
		g := row.GeneralDetails
		gr := &GeneralDetailsResponse{
			Sex:                   g.Sex,
			AdmissionWarnings:     g.AdmissionWarnings,
			InsuranceProviderName: g.InsuranceProviderName,
			ReferringProviderName: g.ReferringProviderName,
			PrimaryProviderName:   g.PrimaryProviderName,
		}
		if g.DateOfBirth != nil {
			str := g.DateOfBirth.Format("2006-01-02")
			gr.DateOfBirth = &str
		}
		decMedAlerts, err := s.decryptOpt(g.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: general medical_alerts: %w", err)
		}
		gr.MedicalAlerts = decMedAlerts
		decMeds, err := s.decryptOpt(g.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: general medications: %w", err)
		}
		gr.Medications = decMeds
		decGAllergies, err := s.decryptOpt(g.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: general allergies: %w", err)
		}
		gr.Allergies = decGAllergies
		decGChronic, err := s.decryptOpt(g.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: general chronic_conditions: %w", err)
		}
		gr.ChronicConditions = decGChronic
		decGPolicy, err := s.decryptOpt(g.InsurancePolicyNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: general insurance_policy_number: %w", err)
		}
		gr.InsurancePolicyNumber = decGPolicy
		dto.GeneralDetails = gr
	}

	if row.AgedCareDetails != nil {
		a := row.AgedCareDetails
		ar := &AgedCareDetailsResponse{
			Sex:                  a.Sex,
			Room:                 a.Room,
			Ethnicity:            a.Ethnicity,
			PreferredLanguage:    a.PreferredLanguage,
			CognitiveStatus:      a.CognitiveStatus,
			MobilityStatus:       a.MobilityStatus,
			ContinenceStatus:     a.ContinenceStatus,
			AdvanceDirectiveFlag: a.AdvanceDirectiveFlag,
			FundingLevel:         a.FundingLevel,
			PrimaryGPName:        a.PrimaryGPName,
		}
		if a.DateOfBirth != nil {
			str := a.DateOfBirth.Format("2006-01-02")
			ar.DateOfBirth = &str
		}
		if a.AdmissionDate != nil {
			str := a.AdmissionDate.Format("2006-01-02")
			ar.AdmissionDate = &str
		}
		decNHI, err := s.decryptOpt(a.NHINumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care nhi_number: %w", err)
		}
		ar.NHINumber = decNHI
		decMedicare, err := s.decryptOpt(a.MedicareNumber)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care medicare_number: %w", err)
		}
		ar.MedicareNumber = decMedicare
		decACMedAlerts, err := s.decryptOpt(a.MedicalAlerts)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care medical_alerts: %w", err)
		}
		ar.MedicalAlerts = decACMedAlerts
		decACMeds, err := s.decryptOpt(a.Medications)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care medications: %w", err)
		}
		ar.Medications = decACMeds
		decACAllergies, err := s.decryptOpt(a.Allergies)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care allergies: %w", err)
		}
		ar.Allergies = decACAllergies
		decACChronic, err := s.decryptOpt(a.ChronicConditions)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care chronic_conditions: %w", err)
		}
		ar.ChronicConditions = decACChronic
		decACDiet, err := s.decryptOpt(a.DietNotes)
		if err != nil {
			return nil, fmt.Errorf("patient.service.decryptSubject: aged-care diet_notes: %w", err)
		}
		ar.DietNotes = decACDiet
		dto.AgedCareDetails = ar
	}

	return dto, nil
}

// encryptOpt encrypts an optional plaintext pointer, returning nil when input is nil.
func (s *Service) encryptOpt(v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	enc, err := s.cipher.Encrypt(*v)
	if err != nil {
		return nil, fmt.Errorf("patient.service.encryptOpt: %w", err)
	}
	return &enc, nil
}

// decryptOpt decrypts an optional ciphertext pointer, returning nil when input is nil.
func (s *Service) decryptOpt(v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	dec, err := s.cipher.Decrypt(*v)
	if err != nil {
		return nil, fmt.Errorf("patient.service.decryptOpt: %w", err)
	}
	return &dec, nil
}

// clampLimit enforces the list limit to the range [1, 100] with a default of 20.
func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

// logAccess writes a subject_access_log entry.
// Callers must invoke this only after the covered mutation/read has succeeded
// so the audit trail never references an action that did not happen.
func (s *Service) logAccess(
	ctx context.Context,
	subjectID, clinicID, staffID uuid.UUID,
	action domain.SubjectAccessAction,
	purpose *string,
) error {
	_, err := s.repo.CreateSubjectAccessLog(ctx, CreateSubjectAccessLogParams{
		ID:        domain.NewID(),
		SubjectID: subjectID,
		StaffID:   staffID,
		ClinicID:  clinicID,
		Action:    action,
		Purpose:   purpose,
	})
	if err != nil {
		return fmt.Errorf("patient.service.logAccess: %w", err)
	}
	return nil
}

// UnmaskPIIInput holds the fields needed to reveal an encrypted value.
type UnmaskPIIInput struct {
	SubjectID uuid.UUID
	ClinicID  uuid.UUID
	CallerID  uuid.UUID
	// Field is the JSON name of the encrypted field the caller wants to reveal,
	// e.g. "insurance_policy_number", "allergies", "medical_alerts".
	Field string
	// Purpose is a free-text reason captured by the UI — logged verbatim.
	Purpose *string
}

// UnmaskPIIResponse carries a single revealed field value.
type UnmaskPIIResponse struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// UnmaskPII fetches the requested encrypted field, decrypts it, and writes
// a subject_access_log entry with action='unmask_pii'. Returns ErrNotFound
// if the subject does not exist or the requested field has no value stored.
// Returns ErrValidation if the field is not one of the reveal-able fields
// for the subject's vertical.
func (s *Service) UnmaskPII(ctx context.Context, in UnmaskPIIInput) (*UnmaskPIIResponse, error) {
	row, err := s.repo.GetSubjectByID(ctx, in.SubjectID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.service.UnmaskPII: %w", err)
	}

	cipherValue, ok := lookupEncryptedField(row, in.Field)
	if !ok {
		return nil, fmt.Errorf("patient.service.UnmaskPII: %w", domain.ErrValidation)
	}
	if cipherValue == nil {
		return nil, fmt.Errorf("patient.service.UnmaskPII: %w", domain.ErrNotFound)
	}

	plain, err := s.cipher.Decrypt(*cipherValue)
	if err != nil {
		return nil, fmt.Errorf("patient.service.UnmaskPII: decrypt: %w", err)
	}

	if err := s.logAccess(ctx, in.SubjectID, in.ClinicID, in.CallerID, domain.SubjectAccessActionUnmaskPII, in.Purpose); err != nil {
		return nil, fmt.Errorf("patient.service.UnmaskPII: %w", err)
	}

	return &UnmaskPIIResponse{Field: in.Field, Value: plain}, nil
}

// lookupEncryptedField resolves a JSON field name to its encrypted *string
// on the loaded SubjectRow. Returns (value, true) if the field is known and
// valid for this subject's vertical, or (nil, false) if unknown.
//
// Only encrypted PHI/PII fields appear here — plaintext fields like
// admission_warnings are already visible in normal GET responses and do
// not require an unmask step.
func lookupEncryptedField(row *SubjectRow, field string) (*string, bool) {
	if row.VetDetails != nil {
		switch field {
		case "allergies":
			return row.VetDetails.Allergies, true
		case "chronic_conditions":
			return row.VetDetails.ChronicConditions, true
		case "insurance_policy_number":
			return row.VetDetails.InsurancePolicyNumber, true
		}
	}
	if row.DentalDetails != nil {
		switch field {
		case "medical_alerts":
			return row.DentalDetails.MedicalAlerts, true
		case "medications":
			return row.DentalDetails.Medications, true
		case "allergies":
			return row.DentalDetails.Allergies, true
		case "chronic_conditions":
			return row.DentalDetails.ChronicConditions, true
		case "insurance_policy_number":
			return row.DentalDetails.InsurancePolicyNumber, true
		}
	}
	if row.GeneralDetails != nil {
		switch field {
		case "medical_alerts":
			return row.GeneralDetails.MedicalAlerts, true
		case "medications":
			return row.GeneralDetails.Medications, true
		case "allergies":
			return row.GeneralDetails.Allergies, true
		case "chronic_conditions":
			return row.GeneralDetails.ChronicConditions, true
		case "insurance_policy_number":
			return row.GeneralDetails.InsurancePolicyNumber, true
		}
	}
	if row.AgedCareDetails != nil {
		switch field {
		case "nhi_number":
			return row.AgedCareDetails.NHINumber, true
		case "medicare_number":
			return row.AgedCareDetails.MedicareNumber, true
		case "medical_alerts":
			return row.AgedCareDetails.MedicalAlerts, true
		case "medications":
			return row.AgedCareDetails.Medications, true
		case "allergies":
			return row.AgedCareDetails.Allergies, true
		case "chronic_conditions":
			return row.AgedCareDetails.ChronicConditions, true
		case "diet_notes":
			return row.AgedCareDetails.DietNotes, true
		}
	}
	return nil, false
}
