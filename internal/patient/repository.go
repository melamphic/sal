package patient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// ContactRecord is the raw database representation of a contact.
// PII fields are stored encrypted and decrypted by the service layer.
type ContactRecord struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	FullName   string  // encrypted
	Phone      *string // encrypted
	Email      *string // encrypted
	EmailHash  *string
	Address    *string // encrypted
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt *time.Time
}

// SubjectRecord is the raw database representation of a subject row.
type SubjectRecord struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	ContactID   *uuid.UUID
	DisplayName string
	Status      domain.SubjectStatus
	Vertical    domain.Vertical
	PhotoURL    *string // legacy column — new writes use PhotoKey
	PhotoKey    *string // durable object-storage path; signed on serve
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  *time.Time
}

// VetDetailsRecord is the raw database representation of a vet_subject_details row.
// Encrypted fields (Allergies, ChronicConditions, InsurancePolicyNumber) hold
// ciphertext at this layer — service.go encrypts on write and decrypts on read.
type VetDetailsRecord struct {
	SubjectID             uuid.UUID
	Species               domain.VetSpecies
	Breed                 *string
	Sex                   *domain.VetSex
	Desexed               *bool
	DateOfBirth           *time.Time
	Color                 *string
	Microchip             *string
	WeightKg              *float64
	Allergies             *string // PHI: encrypted
	ChronicConditions     *string // PHI: encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // PII: encrypted
	ReferringVetName      *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// DentalDetailsRecord is the raw database representation of a dental_subject_details row.
// Encrypted fields (MedicalAlerts, Medications, Allergies, ChronicConditions,
// InsurancePolicyNumber) hold ciphertext at this layer — service.go encrypts
// on write and decrypts on read.
type DentalDetailsRecord struct {
	SubjectID             uuid.UUID
	DateOfBirth           *time.Time
	Sex                   *domain.DentalSex
	MedicalAlerts         *string // PHI: encrypted
	Medications           *string // PHI: encrypted
	Allergies             *string // PHI: encrypted
	ChronicConditions     *string // PHI: encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // PII: encrypted
	ReferringDentistName  *string
	PrimaryDentistName    *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// GeneralDetailsRecord is the raw database representation of a
// general_subject_details row. Encrypted fields (MedicalAlerts, Medications,
// Allergies, ChronicConditions, InsurancePolicyNumber) hold ciphertext at
// this layer — service.go encrypts on write and decrypts on read.
type GeneralDetailsRecord struct {
	SubjectID             uuid.UUID
	DateOfBirth           *time.Time
	Sex                   *domain.GeneralSex
	MedicalAlerts         *string // PHI: encrypted
	Medications           *string // PHI: encrypted
	Allergies             *string // PHI: encrypted
	ChronicConditions     *string // PHI: encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // PII: encrypted
	ReferringProviderName *string
	PrimaryProviderName   *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// AgedCareDetailsRecord is the raw database representation of an
// aged_care_subject_details row. Encrypted fields (NHINumber, MedicareNumber,
// MedicalAlerts, Medications, Allergies, ChronicConditions, DietNotes) hold
// ciphertext at this layer — service.go encrypts on write and decrypts on read.
type AgedCareDetailsRecord struct {
	SubjectID            uuid.UUID
	DateOfBirth          *time.Time
	Sex                  *domain.AgedCareSex
	Room                 *string
	NHINumber            *string // PII: encrypted
	MedicareNumber       *string // PII: encrypted
	Ethnicity            *string
	PreferredLanguage    *string
	MedicalAlerts        *string // PHI: encrypted
	Medications          *string // PHI: encrypted
	Allergies            *string // PHI: encrypted
	ChronicConditions    *string // PHI: encrypted
	CognitiveStatus      *domain.AgedCareCognitiveStatus
	MobilityStatus       *domain.AgedCareMobilityStatus
	ContinenceStatus     *domain.AgedCareContinenceStatus
	DietNotes            *string // PHI: encrypted
	AdvanceDirectiveFlag bool
	FundingLevel         *domain.AgedCareFundingLevel
	AdmissionDate        *time.Time
	PrimaryGPName        *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// SubjectContactRecord is a row from subject_contacts — one (subject, contact,
// role) binding. A single contact can appear multiple times for the same
// subject with different roles, which is why role is part of the PK.
type SubjectContactRecord struct {
	SubjectID uuid.UUID
	ContactID uuid.UUID
	Role      domain.SubjectContactRole
	Note      *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SubjectContactWithContact is a subject_contacts row joined with the
// underlying contact record so the service can decrypt and return the
// contact details in a single call.
type SubjectContactWithContact struct {
	Role    domain.SubjectContactRole
	Note    *string
	Contact *ContactRecord
}

// CreateSubjectContactParams holds values for an add-contact insert.
type CreateSubjectContactParams struct {
	SubjectID uuid.UUID
	ContactID uuid.UUID
	Role      domain.SubjectContactRole
	Note      *string
}

// SubjectAccessLogRecord is a row from subject_access_log. Used for
// historical replay / compliance export; writes go through CreateSubjectAccessLog.
type SubjectAccessLogRecord struct {
	ID        uuid.UUID
	SubjectID uuid.UUID
	StaffID   uuid.UUID
	ClinicID  uuid.UUID
	Action    domain.SubjectAccessAction
	Purpose   *string
	At        time.Time
}

// CreateSubjectAccessLogParams holds values for a single access-log insert.
type CreateSubjectAccessLogParams struct {
	ID        uuid.UUID
	SubjectID uuid.UUID
	StaffID   uuid.UUID
	ClinicID  uuid.UUID
	Action    domain.SubjectAccessAction
	Purpose   *string
}

// SubjectRow is a fully joined subject — subject + contact + per-vertical details.
// This is what the service receives and decrypts before returning to handlers.
// Only one of VetDetails / DentalDetails / GeneralDetails / AgedCareDetails is
// non-nil for any given subject.
type SubjectRow struct {
	Subject         SubjectRecord
	Contact         *ContactRecord         // nil if no contact linked
	VetDetails      *VetDetailsRecord      // nil if not a vet vertical
	DentalDetails   *DentalDetailsRecord   // nil if not a dental vertical
	GeneralDetails  *GeneralDetailsRecord  // nil if not a general_clinic vertical
	AgedCareDetails *AgedCareDetailsRecord // nil if not an aged_care vertical
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreateContactParams holds all values needed to insert a new contact.
type CreateContactParams struct {
	ID        uuid.UUID
	ClinicID  uuid.UUID
	FullName  string  // pre-encrypted
	Phone     *string // pre-encrypted
	Email     *string // pre-encrypted
	EmailHash *string
	Address   *string // pre-encrypted
}

// UpdateContactParams holds fields for a partial contact update.
// Nil means "leave unchanged".
type UpdateContactParams struct {
	FullName  *string // pre-encrypted
	Phone     *string // pre-encrypted
	Email     *string // pre-encrypted
	EmailHash *string
	Address   *string // pre-encrypted
}

// CreateSubjectParams holds all values needed to insert a new subject.
type CreateSubjectParams struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	ContactID   *uuid.UUID
	DisplayName string
	Status      domain.SubjectStatus
	Vertical    domain.Vertical
	PhotoURL    *string // legacy — only set when persisting an unsigned external URL
	PhotoKey    *string // durable storage path; signed on read
	CreatedBy   uuid.UUID
}

// CreateVetDetailsParams holds all values needed to insert a vet_subject_details row.
// Encrypted fields are pre-encrypted by the service before this call.
type CreateVetDetailsParams struct {
	SubjectID             uuid.UUID
	Species               domain.VetSpecies
	Breed                 *string
	Sex                   *domain.VetSex
	Desexed               *bool
	DateOfBirth           *time.Time
	Color                 *string
	Microchip             *string
	WeightKg              *float64
	Allergies             *string // pre-encrypted
	ChronicConditions     *string // pre-encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // pre-encrypted
	ReferringVetName      *string
}

// UpdateSubjectParams holds fields for a partial subject update.
// PhotoKey vs PhotoURL: callers writing a freshly-uploaded photo set
// PhotoKey (the service signs the URL on read). Setting PhotoURL is
// reserved for legacy external links and clears PhotoKey in lockstep.
type UpdateSubjectParams struct {
	DisplayName *string
	Status      *domain.SubjectStatus
	ContactID   *uuid.UUID
	PhotoURL    *string
	PhotoKey    *string
}

// UpdateVetDetailsParams holds fields for a partial vet details update.
// Encrypted fields are pre-encrypted by the service before this call.
type UpdateVetDetailsParams struct {
	Breed                 *string
	Sex                   *domain.VetSex
	Desexed               *bool
	DateOfBirth           *time.Time
	Color                 *string
	Microchip             *string
	WeightKg              *float64
	Allergies             *string // pre-encrypted
	ChronicConditions     *string // pre-encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // pre-encrypted
	ReferringVetName      *string
}

// CreateDentalDetailsParams holds all values needed to insert a dental_subject_details row.
// Encrypted fields are pre-encrypted by the service before this call.
type CreateDentalDetailsParams struct {
	SubjectID             uuid.UUID
	DateOfBirth           *time.Time
	Sex                   *domain.DentalSex
	MedicalAlerts         *string // pre-encrypted
	Medications           *string // pre-encrypted
	Allergies             *string // pre-encrypted
	ChronicConditions     *string // pre-encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // pre-encrypted
	ReferringDentistName  *string
	PrimaryDentistName    *string
}

// UpdateDentalDetailsParams holds fields for a partial dental details update.
// Encrypted fields are pre-encrypted by the service before this call.
type UpdateDentalDetailsParams struct {
	DateOfBirth           *time.Time
	Sex                   *domain.DentalSex
	MedicalAlerts         *string // pre-encrypted
	Medications           *string // pre-encrypted
	Allergies             *string // pre-encrypted
	ChronicConditions     *string // pre-encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // pre-encrypted
	ReferringDentistName  *string
	PrimaryDentistName    *string
}

// CreateGeneralDetailsParams holds all values needed to insert a general_subject_details row.
// Encrypted fields are pre-encrypted by the service before this call.
type CreateGeneralDetailsParams struct {
	SubjectID             uuid.UUID
	DateOfBirth           *time.Time
	Sex                   *domain.GeneralSex
	MedicalAlerts         *string // pre-encrypted
	Medications           *string // pre-encrypted
	Allergies             *string // pre-encrypted
	ChronicConditions     *string // pre-encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // pre-encrypted
	ReferringProviderName *string
	PrimaryProviderName   *string
}

// UpdateGeneralDetailsParams holds fields for a partial general details update.
// Encrypted fields are pre-encrypted by the service before this call.
type UpdateGeneralDetailsParams struct {
	DateOfBirth           *time.Time
	Sex                   *domain.GeneralSex
	MedicalAlerts         *string // pre-encrypted
	Medications           *string // pre-encrypted
	Allergies             *string // pre-encrypted
	ChronicConditions     *string // pre-encrypted
	AdmissionWarnings     *string
	InsuranceProviderName *string
	InsurancePolicyNumber *string // pre-encrypted
	ReferringProviderName *string
	PrimaryProviderName   *string
}

// CreateAgedCareDetailsParams holds all values needed to insert an
// aged_care_subject_details row. Encrypted fields are pre-encrypted by the
// service before this call.
type CreateAgedCareDetailsParams struct {
	SubjectID            uuid.UUID
	DateOfBirth          *time.Time
	Sex                  *domain.AgedCareSex
	Room                 *string
	NHINumber            *string // pre-encrypted
	MedicareNumber       *string // pre-encrypted
	Ethnicity            *string
	PreferredLanguage    *string
	MedicalAlerts        *string // pre-encrypted
	Medications          *string // pre-encrypted
	Allergies            *string // pre-encrypted
	ChronicConditions    *string // pre-encrypted
	CognitiveStatus      *domain.AgedCareCognitiveStatus
	MobilityStatus       *domain.AgedCareMobilityStatus
	ContinenceStatus     *domain.AgedCareContinenceStatus
	DietNotes            *string // pre-encrypted
	AdvanceDirectiveFlag bool
	FundingLevel         *domain.AgedCareFundingLevel
	AdmissionDate        *time.Time
	PrimaryGPName        *string
}

// UpdateAgedCareDetailsParams holds fields for a partial aged-care details
// update. Encrypted fields are pre-encrypted by the service before this call.
type UpdateAgedCareDetailsParams struct {
	DateOfBirth          *time.Time
	Sex                  *domain.AgedCareSex
	Room                 *string
	NHINumber            *string // pre-encrypted
	MedicareNumber       *string // pre-encrypted
	Ethnicity            *string
	PreferredLanguage    *string
	MedicalAlerts        *string // pre-encrypted
	Medications          *string // pre-encrypted
	Allergies            *string // pre-encrypted
	ChronicConditions    *string // pre-encrypted
	CognitiveStatus      *domain.AgedCareCognitiveStatus
	MobilityStatus       *domain.AgedCareMobilityStatus
	ContinenceStatus     *domain.AgedCareContinenceStatus
	DietNotes            *string // pre-encrypted
	AdvanceDirectiveFlag *bool
	FundingLevel         *domain.AgedCareFundingLevel
	AdmissionDate        *time.Time
	PrimaryGPName        *string
}

// ListParams holds pagination parameters for contact listing.
type ListParams struct {
	Limit  int
	Offset int
}

// ListSubjectsParams holds pagination + filter parameters for subject listing.
type ListSubjectsParams struct {
	Limit     int
	Offset    int
	Status    *domain.SubjectStatus // optional filter
	Species   *domain.VetSpecies    // optional filter
	ContactID *uuid.UUID            // optional filter
	CreatedBy *uuid.UUID            // optional — for view_own_patients scope
	Search    *string               // optional — ILIKE match on display_name
}

// Repository handles all database interactions for the patient module.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new patient Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── Contacts ──────────────────────────────────────────────────────────────────

// CreateContact inserts a new contact and returns the created record.
func (r *Repository) CreateContact(ctx context.Context, p CreateContactParams) (*ContactRecord, error) {
	c := &ContactRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO contacts (id, clinic_id, full_name, phone, email, email_hash, address)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, clinic_id, full_name, phone, email, email_hash, address,
		          created_at, updated_at, archived_at
	`, p.ID, p.ClinicID, p.FullName, p.Phone, p.Email, p.EmailHash, p.Address).Scan(
		&c.ID, &c.ClinicID, &c.FullName, &c.Phone, &c.Email, &c.EmailHash, &c.Address,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateContact: %w", err)
	}
	return c, nil
}

// GetContactByID fetches a contact by ID within a clinic.
func (r *Repository) GetContactByID(ctx context.Context, id, clinicID uuid.UUID) (*ContactRecord, error) {
	c := &ContactRecord{}
	err := r.db.QueryRow(ctx, `
		SELECT id, clinic_id, full_name, phone, email, email_hash, address,
		       created_at, updated_at, archived_at
		FROM contacts
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
	`, id, clinicID).Scan(
		&c.ID, &c.ClinicID, &c.FullName, &c.Phone, &c.Email, &c.EmailHash, &c.Address,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.GetContactByID: %w", err)
	}
	return c, nil
}

// ListContacts returns a page of contacts for a clinic ordered by creation date.
func (r *Repository) ListContacts(ctx context.Context, clinicID uuid.UUID, p ListParams) ([]*ContactRecord, int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, clinic_id, full_name, phone, email, email_hash, address,
		       created_at, updated_at, archived_at
		FROM contacts
		WHERE clinic_id = $1 AND archived_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, clinicID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListContacts: query: %w", err)
	}
	defer rows.Close()

	var contacts []*ContactRecord
	for rows.Next() {
		c := &ContactRecord{}
		if err := scanContact(rows, c); err != nil {
			return nil, 0, fmt.Errorf("patient.repo.ListContacts: scan: %w", err)
		}
		contacts = append(contacts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListContacts: rows: %w", err)
	}

	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM contacts WHERE clinic_id = $1 AND archived_at IS NULL
	`, clinicID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListContacts: count: %w", err)
	}

	return contacts, total, nil
}

// UpdateContact applies a partial update to a contact row.
func (r *Repository) UpdateContact(ctx context.Context, id, clinicID uuid.UUID, p UpdateContactParams) (*ContactRecord, error) {
	c := &ContactRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE contacts SET
			full_name  = COALESCE($3, full_name),
			phone      = COALESCE($4, phone),
			email      = COALESCE($5, email),
			email_hash = COALESCE($6, email_hash),
			address    = COALESCE($7, address),
			updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, full_name, phone, email, email_hash, address,
		          created_at, updated_at, archived_at
	`, id, clinicID, p.FullName, p.Phone, p.Email, p.EmailHash, p.Address).Scan(
		&c.ID, &c.ClinicID, &c.FullName, &c.Phone, &c.Email, &c.EmailHash, &c.Address,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateContact: %w", err)
	}
	return c, nil
}

// ── Subjects ──────────────────────────────────────────────────────────────────

// CreateSubject inserts a new subject row.
func (r *Repository) CreateSubject(ctx context.Context, p CreateSubjectParams) (*SubjectRecord, error) {
	s := &SubjectRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO subjects (id, clinic_id, contact_id, display_name, status, vertical, photo_url, photo_key, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, clinic_id, contact_id, display_name, status, vertical, photo_url, photo_key,
		          created_by, created_at, updated_at, archived_at
	`, p.ID, p.ClinicID, p.ContactID, p.DisplayName, p.Status, p.Vertical, p.PhotoURL, p.PhotoKey, p.CreatedBy).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical, &s.PhotoURL, &s.PhotoKey,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateSubject: %w", err)
	}
	return s, nil
}

// CreateVetDetails inserts a vet_subject_details row.
func (r *Repository) CreateVetDetails(ctx context.Context, p CreateVetDetailsParams) (*VetDetailsRecord, error) {
	d := &VetDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO vet_subject_details
			(subject_id, species, breed, sex, desexed, date_of_birth, color, microchip, weight_kg,
			 allergies, chronic_conditions, admission_warnings,
			 insurance_provider_name, insurance_policy_number, referring_vet_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING subject_id, species, breed, sex, desexed, date_of_birth, color,
		          microchip, weight_kg, allergies, chronic_conditions, admission_warnings,
		          insurance_provider_name, insurance_policy_number, referring_vet_name,
		          created_at, updated_at
	`, p.SubjectID, p.Species, p.Breed, p.Sex, p.Desexed, p.DateOfBirth, p.Color, p.Microchip, p.WeightKg,
		p.Allergies, p.ChronicConditions, p.AdmissionWarnings,
		p.InsuranceProviderName, p.InsurancePolicyNumber, p.ReferringVetName,
	).Scan(
		&d.SubjectID, &d.Species, &d.Breed, &d.Sex, &d.Desexed, &d.DateOfBirth, &d.Color,
		&d.Microchip, &d.WeightKg, &d.Allergies, &d.ChronicConditions, &d.AdmissionWarnings,
		&d.InsuranceProviderName, &d.InsurancePolicyNumber, &d.ReferringVetName,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateVetDetails: %w", err)
	}
	return d, nil
}

// GetSubjectByID fetches a subject with its contact and per-vertical details joined.
func (r *Repository) GetSubjectByID(ctx context.Context, id, clinicID uuid.UUID) (*SubjectRow, error) {
	row := &SubjectRow{}
	s := &SubjectRecord{}
	c := &ContactRecord{}
	d := &VetDetailsRecord{}
	dd := &DentalDetailsRecord{}
	g := &GeneralDetailsRecord{}
	ac := &AgedCareDetailsRecord{}

	var (
		// Contact nullable columns.
		cID        *uuid.UUID
		cClinicID  *uuid.UUID
		cFullName  *string
		cPhone     *string
		cEmail     *string
		cEmailHash *string
		cAddress   *string
		cCreatedAt *time.Time
		cUpdatedAt *time.Time
		// Vet details nullable columns.
		dSubjectID         *uuid.UUID
		dSpecies           *domain.VetSpecies
		dBreed             *string
		dSex               *domain.VetSex
		dDesexed           *bool
		dDOB               *time.Time
		dColor             *string
		dMicrochip         *string
		dWeightKg          *float64
		dAllergies         *string
		dChronicConditions *string
		dAdmissionWarn     *string
		dInsProvider       *string
		dInsPolicy         *string
		dReferringVet      *string
		dCreatedAt         *time.Time
		dUpdatedAt         *time.Time
		// Dental details nullable columns.
		ddSubjectID         *uuid.UUID
		ddDOB               *time.Time
		ddSex               *domain.DentalSex
		ddMedicalAlerts     *string
		ddMedications       *string
		ddAllergies         *string
		ddChronicConditions *string
		ddAdmissionWarn     *string
		ddInsProvider       *string
		ddInsPolicy         *string
		ddReferringDentist  *string
		ddPrimaryDentist    *string
		ddCreatedAt         *time.Time
		ddUpdatedAt         *time.Time
		// General details nullable columns.
		gSubjectID         *uuid.UUID
		gDOB               *time.Time
		gSex               *domain.GeneralSex
		gMedicalAlerts     *string
		gMedications       *string
		gAllergies         *string
		gChronicConditions *string
		gAdmissionWarn     *string
		gInsProvider       *string
		gInsPolicy         *string
		gReferringProvider *string
		gPrimaryProvider   *string
		gCreatedAt         *time.Time
		gUpdatedAt         *time.Time
		// Aged-care details nullable columns.
		acSubjectID         *uuid.UUID
		acDOB               *time.Time
		acSex               *domain.AgedCareSex
		acRoom              *string
		acNHI               *string
		acMedicare          *string
		acEthnicity         *string
		acLanguage          *string
		acMedicalAlerts     *string
		acMedications       *string
		acAllergies         *string
		acChronicConditions *string
		acCognitive         *domain.AgedCareCognitiveStatus
		acMobility          *domain.AgedCareMobilityStatus
		acContinence        *domain.AgedCareContinenceStatus
		acDietNotes         *string
		acAdvanceDirective  *bool
		acFundingLevel      *domain.AgedCareFundingLevel
		acAdmissionDate     *time.Time
		acPrimaryGP         *string
		acCreatedAt         *time.Time
		acUpdatedAt         *time.Time
	)

	err := r.db.QueryRow(ctx, `
		SELECT
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical, s.photo_url, s.photo_key,
			s.created_by, s.created_at, s.updated_at, s.archived_at,
			c.id, c.clinic_id, c.full_name, c.phone, c.email, c.email_hash, c.address,
			c.created_at, c.updated_at,
			v.subject_id, v.species, v.breed, v.sex, v.desexed, v.date_of_birth,
			v.color, v.microchip, v.weight_kg, v.allergies, v.chronic_conditions,
			v.admission_warnings, v.insurance_provider_name, v.insurance_policy_number,
			v.referring_vet_name, v.created_at, v.updated_at,
			dd.subject_id, dd.date_of_birth, dd.sex, dd.medical_alerts, dd.medications,
			dd.allergies, dd.chronic_conditions, dd.admission_warnings,
			dd.insurance_provider_name, dd.insurance_policy_number,
			dd.referring_dentist_name, dd.primary_dentist_name,
			dd.created_at, dd.updated_at,
			g.subject_id, g.date_of_birth, g.sex, g.medical_alerts, g.medications,
			g.allergies, g.chronic_conditions, g.admission_warnings,
			g.insurance_provider_name, g.insurance_policy_number,
			g.referring_provider_name, g.primary_provider_name,
			g.created_at, g.updated_at,
			ac.subject_id, ac.date_of_birth, ac.sex, ac.room, ac.nhi_number,
			ac.medicare_number, ac.ethnicity, ac.preferred_language,
			ac.medical_alerts, ac.medications, ac.allergies, ac.chronic_conditions,
			ac.cognitive_status, ac.mobility_status, ac.continence_status,
			ac.diet_notes, ac.advance_directive_flag, ac.funding_level,
			ac.admission_date, ac.primary_gp_name, ac.created_at, ac.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
		LEFT JOIN aged_care_subject_details ac ON ac.subject_id = s.id
		WHERE s.id = $1 AND s.clinic_id = $2 AND s.archived_at IS NULL
	`, id, clinicID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical, &s.PhotoURL, &s.PhotoKey,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
		&cID, &cClinicID, &cFullName, &cPhone, &cEmail, &cEmailHash, &cAddress, &cCreatedAt, &cUpdatedAt,
		&dSubjectID, &dSpecies, &dBreed, &dSex, &dDesexed, &dDOB, &dColor, &dMicrochip, &dWeightKg,
		&dAllergies, &dChronicConditions, &dAdmissionWarn, &dInsProvider, &dInsPolicy, &dReferringVet,
		&dCreatedAt, &dUpdatedAt,
		&ddSubjectID, &ddDOB, &ddSex, &ddMedicalAlerts, &ddMedications,
		&ddAllergies, &ddChronicConditions, &ddAdmissionWarn,
		&ddInsProvider, &ddInsPolicy, &ddReferringDentist, &ddPrimaryDentist,
		&ddCreatedAt, &ddUpdatedAt,
		&gSubjectID, &gDOB, &gSex, &gMedicalAlerts, &gMedications,
		&gAllergies, &gChronicConditions, &gAdmissionWarn,
		&gInsProvider, &gInsPolicy, &gReferringProvider, &gPrimaryProvider,
		&gCreatedAt, &gUpdatedAt,
		&acSubjectID, &acDOB, &acSex, &acRoom, &acNHI, &acMedicare,
		&acEthnicity, &acLanguage, &acMedicalAlerts, &acMedications,
		&acAllergies, &acChronicConditions, &acCognitive, &acMobility,
		&acContinence, &acDietNotes, &acAdvanceDirective, &acFundingLevel,
		&acAdmissionDate, &acPrimaryGP, &acCreatedAt, &acUpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.GetSubjectByID: %w", err)
	}

	row.Subject = *s

	if cID != nil {
		c.ID = *cID
		c.ClinicID = *cClinicID
		c.FullName = *cFullName
		c.Phone = cPhone
		c.Email = cEmail
		c.EmailHash = cEmailHash
		c.Address = cAddress
		c.CreatedAt = *cCreatedAt
		c.UpdatedAt = *cUpdatedAt
		row.Contact = c
	}

	if dSubjectID != nil {
		d.SubjectID = *dSubjectID
		d.Species = *dSpecies
		d.Breed = dBreed
		d.Sex = dSex
		d.Desexed = dDesexed
		d.DateOfBirth = dDOB
		d.Color = dColor
		d.Microchip = dMicrochip
		d.WeightKg = dWeightKg
		d.Allergies = dAllergies
		d.ChronicConditions = dChronicConditions
		d.AdmissionWarnings = dAdmissionWarn
		d.InsuranceProviderName = dInsProvider
		d.InsurancePolicyNumber = dInsPolicy
		d.ReferringVetName = dReferringVet
		d.CreatedAt = *dCreatedAt
		d.UpdatedAt = *dUpdatedAt
		row.VetDetails = d
	}

	if ddSubjectID != nil {
		dd.SubjectID = *ddSubjectID
		dd.DateOfBirth = ddDOB
		dd.Sex = ddSex
		dd.MedicalAlerts = ddMedicalAlerts
		dd.Medications = ddMedications
		dd.Allergies = ddAllergies
		dd.ChronicConditions = ddChronicConditions
		dd.AdmissionWarnings = ddAdmissionWarn
		dd.InsuranceProviderName = ddInsProvider
		dd.InsurancePolicyNumber = ddInsPolicy
		dd.ReferringDentistName = ddReferringDentist
		dd.PrimaryDentistName = ddPrimaryDentist
		dd.CreatedAt = *ddCreatedAt
		dd.UpdatedAt = *ddUpdatedAt
		row.DentalDetails = dd
	}

	if gSubjectID != nil {
		g.SubjectID = *gSubjectID
		g.DateOfBirth = gDOB
		g.Sex = gSex
		g.MedicalAlerts = gMedicalAlerts
		g.Medications = gMedications
		g.Allergies = gAllergies
		g.ChronicConditions = gChronicConditions
		g.AdmissionWarnings = gAdmissionWarn
		g.InsuranceProviderName = gInsProvider
		g.InsurancePolicyNumber = gInsPolicy
		g.ReferringProviderName = gReferringProvider
		g.PrimaryProviderName = gPrimaryProvider
		g.CreatedAt = *gCreatedAt
		g.UpdatedAt = *gUpdatedAt
		row.GeneralDetails = g
	}

	if acSubjectID != nil {
		ac.SubjectID = *acSubjectID
		ac.DateOfBirth = acDOB
		ac.Sex = acSex
		ac.Room = acRoom
		ac.NHINumber = acNHI
		ac.MedicareNumber = acMedicare
		ac.Ethnicity = acEthnicity
		ac.PreferredLanguage = acLanguage
		ac.MedicalAlerts = acMedicalAlerts
		ac.Medications = acMedications
		ac.Allergies = acAllergies
		ac.ChronicConditions = acChronicConditions
		ac.CognitiveStatus = acCognitive
		ac.MobilityStatus = acMobility
		ac.ContinenceStatus = acContinence
		ac.DietNotes = acDietNotes
		if acAdvanceDirective != nil {
			ac.AdvanceDirectiveFlag = *acAdvanceDirective
		}
		ac.FundingLevel = acFundingLevel
		ac.AdmissionDate = acAdmissionDate
		ac.PrimaryGPName = acPrimaryGP
		ac.CreatedAt = *acCreatedAt
		ac.UpdatedAt = *acUpdatedAt
		row.AgedCareDetails = ac
	}

	return row, nil
}

// ListSubjects returns a page of subjects with optional filters.
func (r *Repository) ListSubjects(ctx context.Context, clinicID uuid.UUID, p ListSubjectsParams) ([]*SubjectRow, int, error) {
	// Build the WHERE clause dynamically based on provided filters.
	args := []any{clinicID}
	where := "s.clinic_id = $1 AND s.archived_at IS NULL"

	if p.Status != nil {
		args = append(args, *p.Status)
		where += fmt.Sprintf(" AND s.status = $%d", len(args))
	}
	if p.ContactID != nil {
		args = append(args, *p.ContactID)
		where += fmt.Sprintf(" AND s.contact_id = $%d", len(args))
	}
	if p.CreatedBy != nil {
		args = append(args, *p.CreatedBy)
		where += fmt.Sprintf(" AND s.created_by = $%d", len(args))
	}
	if p.Species != nil {
		args = append(args, *p.Species)
		where += fmt.Sprintf(" AND v.species = $%d", len(args))
	}
	if p.Search != nil && *p.Search != "" {
		args = append(args, *p.Search)
		where += fmt.Sprintf(" AND s.display_name ILIKE '%%' || $%d || '%%'", len(args))
	}

	// Count total matching rows.
	var total int
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM subjects s
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
		LEFT JOIN aged_care_subject_details ac ON ac.subject_id = s.id
		WHERE %s
	`, where)
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListSubjects: count: %w", err)
	}

	// Fetch page.
	args = append(args, p.Limit, p.Offset)
	listQuery := fmt.Sprintf(`
		SELECT
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical, s.photo_url, s.photo_key,
			s.created_by, s.created_at, s.updated_at, s.archived_at,
			c.id, c.clinic_id, c.full_name, c.phone, c.email, c.email_hash, c.address,
			c.created_at, c.updated_at,
			v.subject_id, v.species, v.breed, v.sex, v.desexed, v.date_of_birth,
			v.color, v.microchip, v.weight_kg, v.allergies, v.chronic_conditions,
			v.admission_warnings, v.insurance_provider_name, v.insurance_policy_number,
			v.referring_vet_name, v.created_at, v.updated_at,
			dd.subject_id, dd.date_of_birth, dd.sex, dd.medical_alerts, dd.medications,
			dd.allergies, dd.chronic_conditions, dd.admission_warnings,
			dd.insurance_provider_name, dd.insurance_policy_number,
			dd.referring_dentist_name, dd.primary_dentist_name,
			dd.created_at, dd.updated_at,
			g.subject_id, g.date_of_birth, g.sex, g.medical_alerts, g.medications,
			g.allergies, g.chronic_conditions, g.admission_warnings,
			g.insurance_provider_name, g.insurance_policy_number,
			g.referring_provider_name, g.primary_provider_name,
			g.created_at, g.updated_at,
			ac.subject_id, ac.date_of_birth, ac.sex, ac.room, ac.nhi_number,
			ac.medicare_number, ac.ethnicity, ac.preferred_language,
			ac.medical_alerts, ac.medications, ac.allergies, ac.chronic_conditions,
			ac.cognitive_status, ac.mobility_status, ac.continence_status,
			ac.diet_notes, ac.advance_directive_flag, ac.funding_level,
			ac.admission_date, ac.primary_gp_name, ac.created_at, ac.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
		LEFT JOIN aged_care_subject_details ac ON ac.subject_id = s.id
		WHERE %s
		ORDER BY s.created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListSubjects: query: %w", err)
	}
	defer rows.Close()

	var subjects []*SubjectRow
	for rows.Next() {
		row, err := scanSubjectRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("patient.repo.ListSubjects: scan: %w", err)
		}
		subjects = append(subjects, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListSubjects: rows: %w", err)
	}

	return subjects, total, nil
}

// UpdateSubject applies a partial update to a subject row.
func (r *Repository) UpdateSubject(ctx context.Context, id, clinicID uuid.UUID, p UpdateSubjectParams) (*SubjectRecord, error) {
	s := &SubjectRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE subjects SET
			display_name = COALESCE($3, display_name),
			status       = COALESCE($4, status),
			contact_id   = COALESCE($5, contact_id),
			photo_url    = COALESCE($6, photo_url),
			photo_key    = COALESCE($7, photo_key),
			updated_at   = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, contact_id, display_name, status, vertical, photo_url, photo_key,
		          created_by, created_at, updated_at, archived_at
	`, id, clinicID, p.DisplayName, p.Status, p.ContactID, p.PhotoURL, p.PhotoKey).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical, &s.PhotoURL, &s.PhotoKey,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateSubject: %w", err)
	}
	return s, nil
}

// UpdateVetDetails applies a partial update to a vet_subject_details row.
func (r *Repository) UpdateVetDetails(ctx context.Context, subjectID uuid.UUID, p UpdateVetDetailsParams) (*VetDetailsRecord, error) {
	d := &VetDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE vet_subject_details SET
			breed                   = COALESCE($2, breed),
			sex                     = COALESCE($3, sex),
			desexed                 = COALESCE($4, desexed),
			date_of_birth           = COALESCE($5, date_of_birth),
			color                   = COALESCE($6, color),
			microchip               = COALESCE($7, microchip),
			weight_kg               = COALESCE($8, weight_kg),
			allergies               = COALESCE($9, allergies),
			chronic_conditions      = COALESCE($10, chronic_conditions),
			admission_warnings      = COALESCE($11, admission_warnings),
			insurance_provider_name = COALESCE($12, insurance_provider_name),
			insurance_policy_number = COALESCE($13, insurance_policy_number),
			referring_vet_name      = COALESCE($14, referring_vet_name),
			updated_at              = NOW()
		WHERE subject_id = $1
		RETURNING subject_id, species, breed, sex, desexed, date_of_birth, color,
		          microchip, weight_kg, allergies, chronic_conditions, admission_warnings,
		          insurance_provider_name, insurance_policy_number, referring_vet_name,
		          created_at, updated_at
	`, subjectID, p.Breed, p.Sex, p.Desexed, p.DateOfBirth, p.Color, p.Microchip, p.WeightKg,
		p.Allergies, p.ChronicConditions, p.AdmissionWarnings,
		p.InsuranceProviderName, p.InsurancePolicyNumber, p.ReferringVetName,
	).Scan(
		&d.SubjectID, &d.Species, &d.Breed, &d.Sex, &d.Desexed, &d.DateOfBirth, &d.Color,
		&d.Microchip, &d.WeightKg, &d.Allergies, &d.ChronicConditions, &d.AdmissionWarnings,
		&d.InsuranceProviderName, &d.InsurancePolicyNumber, &d.ReferringVetName,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateVetDetails: %w", err)
	}
	return d, nil
}

// CreateDentalDetails inserts a dental_subject_details row.
func (r *Repository) CreateDentalDetails(ctx context.Context, p CreateDentalDetailsParams) (*DentalDetailsRecord, error) {
	dd := &DentalDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO dental_subject_details
			(subject_id, date_of_birth, sex, medical_alerts, medications,
			 allergies, chronic_conditions, admission_warnings,
			 insurance_provider_name, insurance_policy_number,
			 referring_dentist_name, primary_dentist_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING subject_id, date_of_birth, sex, medical_alerts, medications,
		          allergies, chronic_conditions, admission_warnings,
		          insurance_provider_name, insurance_policy_number,
		          referring_dentist_name, primary_dentist_name,
		          created_at, updated_at
	`, p.SubjectID, p.DateOfBirth, p.Sex, p.MedicalAlerts, p.Medications,
		p.Allergies, p.ChronicConditions, p.AdmissionWarnings,
		p.InsuranceProviderName, p.InsurancePolicyNumber,
		p.ReferringDentistName, p.PrimaryDentistName,
	).Scan(
		&dd.SubjectID, &dd.DateOfBirth, &dd.Sex, &dd.MedicalAlerts, &dd.Medications,
		&dd.Allergies, &dd.ChronicConditions, &dd.AdmissionWarnings,
		&dd.InsuranceProviderName, &dd.InsurancePolicyNumber,
		&dd.ReferringDentistName, &dd.PrimaryDentistName,
		&dd.CreatedAt, &dd.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateDentalDetails: %w", err)
	}
	return dd, nil
}

// UpdateDentalDetails applies a partial update to a dental_subject_details row.
func (r *Repository) UpdateDentalDetails(ctx context.Context, subjectID uuid.UUID, p UpdateDentalDetailsParams) (*DentalDetailsRecord, error) {
	dd := &DentalDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE dental_subject_details SET
			date_of_birth           = COALESCE($2, date_of_birth),
			sex                     = COALESCE($3, sex),
			medical_alerts          = COALESCE($4, medical_alerts),
			medications             = COALESCE($5, medications),
			allergies               = COALESCE($6, allergies),
			chronic_conditions      = COALESCE($7, chronic_conditions),
			admission_warnings      = COALESCE($8, admission_warnings),
			insurance_provider_name = COALESCE($9, insurance_provider_name),
			insurance_policy_number = COALESCE($10, insurance_policy_number),
			referring_dentist_name  = COALESCE($11, referring_dentist_name),
			primary_dentist_name    = COALESCE($12, primary_dentist_name),
			updated_at              = NOW()
		WHERE subject_id = $1
		RETURNING subject_id, date_of_birth, sex, medical_alerts, medications,
		          allergies, chronic_conditions, admission_warnings,
		          insurance_provider_name, insurance_policy_number,
		          referring_dentist_name, primary_dentist_name,
		          created_at, updated_at
	`, subjectID, p.DateOfBirth, p.Sex, p.MedicalAlerts, p.Medications,
		p.Allergies, p.ChronicConditions, p.AdmissionWarnings,
		p.InsuranceProviderName, p.InsurancePolicyNumber,
		p.ReferringDentistName, p.PrimaryDentistName,
	).Scan(
		&dd.SubjectID, &dd.DateOfBirth, &dd.Sex, &dd.MedicalAlerts, &dd.Medications,
		&dd.Allergies, &dd.ChronicConditions, &dd.AdmissionWarnings,
		&dd.InsuranceProviderName, &dd.InsurancePolicyNumber,
		&dd.ReferringDentistName, &dd.PrimaryDentistName,
		&dd.CreatedAt, &dd.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateDentalDetails: %w", err)
	}
	return dd, nil
}

// CreateGeneralDetails inserts a general_subject_details row.
func (r *Repository) CreateGeneralDetails(ctx context.Context, p CreateGeneralDetailsParams) (*GeneralDetailsRecord, error) {
	g := &GeneralDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO general_subject_details
			(subject_id, date_of_birth, sex, medical_alerts, medications,
			 allergies, chronic_conditions, admission_warnings,
			 insurance_provider_name, insurance_policy_number,
			 referring_provider_name, primary_provider_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING subject_id, date_of_birth, sex, medical_alerts, medications,
		          allergies, chronic_conditions, admission_warnings,
		          insurance_provider_name, insurance_policy_number,
		          referring_provider_name, primary_provider_name,
		          created_at, updated_at
	`, p.SubjectID, p.DateOfBirth, p.Sex, p.MedicalAlerts, p.Medications,
		p.Allergies, p.ChronicConditions, p.AdmissionWarnings,
		p.InsuranceProviderName, p.InsurancePolicyNumber,
		p.ReferringProviderName, p.PrimaryProviderName,
	).Scan(
		&g.SubjectID, &g.DateOfBirth, &g.Sex, &g.MedicalAlerts, &g.Medications,
		&g.Allergies, &g.ChronicConditions, &g.AdmissionWarnings,
		&g.InsuranceProviderName, &g.InsurancePolicyNumber,
		&g.ReferringProviderName, &g.PrimaryProviderName,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateGeneralDetails: %w", err)
	}
	return g, nil
}

// UpdateGeneralDetails applies a partial update to a general_subject_details row.
func (r *Repository) UpdateGeneralDetails(ctx context.Context, subjectID uuid.UUID, p UpdateGeneralDetailsParams) (*GeneralDetailsRecord, error) {
	g := &GeneralDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE general_subject_details SET
			date_of_birth           = COALESCE($2, date_of_birth),
			sex                     = COALESCE($3, sex),
			medical_alerts          = COALESCE($4, medical_alerts),
			medications             = COALESCE($5, medications),
			allergies               = COALESCE($6, allergies),
			chronic_conditions      = COALESCE($7, chronic_conditions),
			admission_warnings      = COALESCE($8, admission_warnings),
			insurance_provider_name = COALESCE($9, insurance_provider_name),
			insurance_policy_number = COALESCE($10, insurance_policy_number),
			referring_provider_name = COALESCE($11, referring_provider_name),
			primary_provider_name   = COALESCE($12, primary_provider_name),
			updated_at              = NOW()
		WHERE subject_id = $1
		RETURNING subject_id, date_of_birth, sex, medical_alerts, medications,
		          allergies, chronic_conditions, admission_warnings,
		          insurance_provider_name, insurance_policy_number,
		          referring_provider_name, primary_provider_name,
		          created_at, updated_at
	`, subjectID, p.DateOfBirth, p.Sex, p.MedicalAlerts, p.Medications,
		p.Allergies, p.ChronicConditions, p.AdmissionWarnings,
		p.InsuranceProviderName, p.InsurancePolicyNumber,
		p.ReferringProviderName, p.PrimaryProviderName,
	).Scan(
		&g.SubjectID, &g.DateOfBirth, &g.Sex, &g.MedicalAlerts, &g.Medications,
		&g.Allergies, &g.ChronicConditions, &g.AdmissionWarnings,
		&g.InsuranceProviderName, &g.InsurancePolicyNumber,
		&g.ReferringProviderName, &g.PrimaryProviderName,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateGeneralDetails: %w", err)
	}
	return g, nil
}

// CreateAgedCareDetails inserts an aged_care_subject_details row.
func (r *Repository) CreateAgedCareDetails(ctx context.Context, p CreateAgedCareDetailsParams) (*AgedCareDetailsRecord, error) {
	a := &AgedCareDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO aged_care_subject_details
			(subject_id, date_of_birth, sex, room, nhi_number, medicare_number,
			 ethnicity, preferred_language, medical_alerts, medications,
			 allergies, chronic_conditions, cognitive_status, mobility_status,
			 continence_status, diet_notes, advance_directive_flag,
			 funding_level, admission_date, primary_gp_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		        $15, $16, $17, $18, $19, $20)
		RETURNING subject_id, date_of_birth, sex, room, nhi_number, medicare_number,
		          ethnicity, preferred_language, medical_alerts, medications,
		          allergies, chronic_conditions, cognitive_status, mobility_status,
		          continence_status, diet_notes, advance_directive_flag,
		          funding_level, admission_date, primary_gp_name,
		          created_at, updated_at
	`, p.SubjectID, p.DateOfBirth, p.Sex, p.Room, p.NHINumber, p.MedicareNumber,
		p.Ethnicity, p.PreferredLanguage, p.MedicalAlerts, p.Medications,
		p.Allergies, p.ChronicConditions, p.CognitiveStatus, p.MobilityStatus,
		p.ContinenceStatus, p.DietNotes, p.AdvanceDirectiveFlag,
		p.FundingLevel, p.AdmissionDate, p.PrimaryGPName,
	).Scan(
		&a.SubjectID, &a.DateOfBirth, &a.Sex, &a.Room, &a.NHINumber, &a.MedicareNumber,
		&a.Ethnicity, &a.PreferredLanguage, &a.MedicalAlerts, &a.Medications,
		&a.Allergies, &a.ChronicConditions, &a.CognitiveStatus, &a.MobilityStatus,
		&a.ContinenceStatus, &a.DietNotes, &a.AdvanceDirectiveFlag,
		&a.FundingLevel, &a.AdmissionDate, &a.PrimaryGPName,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateAgedCareDetails: %w", err)
	}
	return a, nil
}

// UpdateAgedCareDetails applies a partial update to an aged_care_subject_details row.
func (r *Repository) UpdateAgedCareDetails(ctx context.Context, subjectID uuid.UUID, p UpdateAgedCareDetailsParams) (*AgedCareDetailsRecord, error) {
	a := &AgedCareDetailsRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE aged_care_subject_details SET
			date_of_birth          = COALESCE($2, date_of_birth),
			sex                    = COALESCE($3, sex),
			room                   = COALESCE($4, room),
			nhi_number             = COALESCE($5, nhi_number),
			medicare_number        = COALESCE($6, medicare_number),
			ethnicity              = COALESCE($7, ethnicity),
			preferred_language     = COALESCE($8, preferred_language),
			medical_alerts         = COALESCE($9, medical_alerts),
			medications            = COALESCE($10, medications),
			allergies              = COALESCE($11, allergies),
			chronic_conditions     = COALESCE($12, chronic_conditions),
			cognitive_status       = COALESCE($13, cognitive_status),
			mobility_status        = COALESCE($14, mobility_status),
			continence_status      = COALESCE($15, continence_status),
			diet_notes             = COALESCE($16, diet_notes),
			advance_directive_flag = COALESCE($17, advance_directive_flag),
			funding_level          = COALESCE($18, funding_level),
			admission_date         = COALESCE($19, admission_date),
			primary_gp_name        = COALESCE($20, primary_gp_name),
			updated_at             = NOW()
		WHERE subject_id = $1
		RETURNING subject_id, date_of_birth, sex, room, nhi_number, medicare_number,
		          ethnicity, preferred_language, medical_alerts, medications,
		          allergies, chronic_conditions, cognitive_status, mobility_status,
		          continence_status, diet_notes, advance_directive_flag,
		          funding_level, admission_date, primary_gp_name,
		          created_at, updated_at
	`, subjectID, p.DateOfBirth, p.Sex, p.Room, p.NHINumber, p.MedicareNumber,
		p.Ethnicity, p.PreferredLanguage, p.MedicalAlerts, p.Medications,
		p.Allergies, p.ChronicConditions, p.CognitiveStatus, p.MobilityStatus,
		p.ContinenceStatus, p.DietNotes, p.AdvanceDirectiveFlag,
		p.FundingLevel, p.AdmissionDate, p.PrimaryGPName,
	).Scan(
		&a.SubjectID, &a.DateOfBirth, &a.Sex, &a.Room, &a.NHINumber, &a.MedicareNumber,
		&a.Ethnicity, &a.PreferredLanguage, &a.MedicalAlerts, &a.Medications,
		&a.Allergies, &a.ChronicConditions, &a.CognitiveStatus, &a.MobilityStatus,
		&a.ContinenceStatus, &a.DietNotes, &a.AdvanceDirectiveFlag,
		&a.FundingLevel, &a.AdmissionDate, &a.PrimaryGPName,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateAgedCareDetails: %w", err)
	}
	return a, nil
}

// ArchiveContact soft-deletes a contact by stamping archived_at.
// Returns ErrConflict if the contact is still linked to any active subject,
// since archiving it out from under a live patient would strand the link.
func (r *Repository) ArchiveContact(ctx context.Context, id, clinicID uuid.UUID) (*ContactRecord, error) {
	var linkedCount int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM subjects
		WHERE contact_id = $1 AND clinic_id = $2 AND archived_at IS NULL
	`, id, clinicID).Scan(&linkedCount); err != nil {
		return nil, fmt.Errorf("patient.repo.ArchiveContact: count subjects: %w", err)
	}
	if linkedCount > 0 {
		return nil, domain.ErrConflict
	}

	c := &ContactRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE contacts
		SET archived_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, full_name, phone, email, email_hash, address,
		          created_at, updated_at, archived_at
	`, id, clinicID).Scan(
		&c.ID, &c.ClinicID, &c.FullName, &c.Phone, &c.Email, &c.EmailHash, &c.Address,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.ArchiveContact: %w", err)
	}
	return c, nil
}

// ArchiveSubject soft-deletes a subject.
func (r *Repository) ArchiveSubject(ctx context.Context, id, clinicID uuid.UUID) (*SubjectRecord, error) {
	s := &SubjectRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE subjects
		SET status = 'archived', archived_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, contact_id, display_name, status, vertical, photo_url,
		          created_by, created_at, updated_at, archived_at
	`, id, clinicID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical, &s.PhotoURL,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.ArchiveSubject: %w", err)
	}
	return s, nil
}

// LinkContact sets contact_id on an existing subject.
func (r *Repository) LinkContact(ctx context.Context, subjectID, clinicID, contactID uuid.UUID) (*SubjectRecord, error) {
	s := &SubjectRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE subjects
		SET contact_id = $3, updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, contact_id, display_name, status, vertical, photo_url,
		          created_by, created_at, updated_at, archived_at
	`, subjectID, clinicID, contactID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical, &s.PhotoURL,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.LinkContact: %w", err)
	}
	return s, nil
}

// ListSubjectsByContact returns all active subjects for a given contact.
func (r *Repository) ListSubjectsByContact(ctx context.Context, contactID, clinicID uuid.UUID) ([]*SubjectRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical, s.photo_url,
			s.created_by, s.created_at, s.updated_at, s.archived_at,
			c.id, c.clinic_id, c.full_name, c.phone, c.email, c.email_hash, c.address,
			c.created_at, c.updated_at,
			v.subject_id, v.species, v.breed, v.sex, v.desexed, v.date_of_birth,
			v.color, v.microchip, v.weight_kg, v.allergies, v.chronic_conditions,
			v.admission_warnings, v.insurance_provider_name, v.insurance_policy_number,
			v.referring_vet_name, v.created_at, v.updated_at,
			dd.subject_id, dd.date_of_birth, dd.sex, dd.medical_alerts, dd.medications,
			dd.allergies, dd.chronic_conditions, dd.admission_warnings,
			dd.insurance_provider_name, dd.insurance_policy_number,
			dd.referring_dentist_name, dd.primary_dentist_name,
			dd.created_at, dd.updated_at,
			g.subject_id, g.date_of_birth, g.sex, g.medical_alerts, g.medications,
			g.allergies, g.chronic_conditions, g.admission_warnings,
			g.insurance_provider_name, g.insurance_policy_number,
			g.referring_provider_name, g.primary_provider_name,
			g.created_at, g.updated_at,
			ac.subject_id, ac.date_of_birth, ac.sex, ac.room, ac.nhi_number,
			ac.medicare_number, ac.ethnicity, ac.preferred_language,
			ac.medical_alerts, ac.medications, ac.allergies, ac.chronic_conditions,
			ac.cognitive_status, ac.mobility_status, ac.continence_status,
			ac.diet_notes, ac.advance_directive_flag, ac.funding_level,
			ac.admission_date, ac.primary_gp_name, ac.created_at, ac.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
		LEFT JOIN aged_care_subject_details ac ON ac.subject_id = s.id
		WHERE s.contact_id = $1 AND s.clinic_id = $2 AND s.archived_at IS NULL
		ORDER BY s.created_at DESC
	`, contactID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.ListSubjectsByContact: query: %w", err)
	}
	defer rows.Close()

	var subjects []*SubjectRow
	for rows.Next() {
		row, err := scanSubjectRow(rows)
		if err != nil {
			return nil, fmt.Errorf("patient.repo.ListSubjectsByContact: scan: %w", err)
		}
		subjects = append(subjects, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("patient.repo.ListSubjectsByContact: rows: %w", err)
	}

	return subjects, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

type pgxScanner interface {
	Scan(dest ...any) error
}

func scanContact(row pgxScanner, c *ContactRecord) error {
	if err := row.Scan(
		&c.ID, &c.ClinicID, &c.FullName, &c.Phone, &c.Email, &c.EmailHash, &c.Address,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	); err != nil {
		return fmt.Errorf("patient.repo.scanContact: %w", err)
	}
	return nil
}

func scanSubjectRow(rows pgxScanner) (*SubjectRow, error) {
	row := &SubjectRow{}
	s := &SubjectRecord{}
	c := &ContactRecord{}
	d := &VetDetailsRecord{}
	dd := &DentalDetailsRecord{}
	g := &GeneralDetailsRecord{}
	ac := &AgedCareDetailsRecord{}

	var (
		cID        *uuid.UUID
		cClinicID  *uuid.UUID
		cFullName  *string
		cPhone     *string
		cEmail     *string
		cEmailHash *string
		cAddress   *string
		cCreatedAt *time.Time
		cUpdatedAt *time.Time

		dSubjectID         *uuid.UUID
		dSpecies           *domain.VetSpecies
		dBreed             *string
		dSex               *domain.VetSex
		dDesexed           *bool
		dDOB               *time.Time
		dColor             *string
		dMicrochip         *string
		dWeightKg          *float64
		dAllergies         *string
		dChronicConditions *string
		dAdmissionWarn     *string
		dInsProvider       *string
		dInsPolicy         *string
		dReferringVet      *string
		dCreatedAt         *time.Time
		dUpdatedAt         *time.Time

		ddSubjectID         *uuid.UUID
		ddDOB               *time.Time
		ddSex               *domain.DentalSex
		ddMedicalAlerts     *string
		ddMedications       *string
		ddAllergies         *string
		ddChronicConditions *string
		ddAdmissionWarn     *string
		ddInsProvider       *string
		ddInsPolicy         *string
		ddReferringDentist  *string
		ddPrimaryDentist    *string
		ddCreatedAt         *time.Time
		ddUpdatedAt         *time.Time

		gSubjectID         *uuid.UUID
		gDOB               *time.Time
		gSex               *domain.GeneralSex
		gMedicalAlerts     *string
		gMedications       *string
		gAllergies         *string
		gChronicConditions *string
		gAdmissionWarn     *string
		gInsProvider       *string
		gInsPolicy         *string
		gReferringProvider *string
		gPrimaryProvider   *string
		gCreatedAt         *time.Time
		gUpdatedAt         *time.Time

		acSubjectID         *uuid.UUID
		acDOB               *time.Time
		acSex               *domain.AgedCareSex
		acRoom              *string
		acNHI               *string
		acMedicare          *string
		acEthnicity         *string
		acLanguage          *string
		acMedicalAlerts     *string
		acMedications       *string
		acAllergies         *string
		acChronicConditions *string
		acCognitive         *domain.AgedCareCognitiveStatus
		acMobility          *domain.AgedCareMobilityStatus
		acContinence        *domain.AgedCareContinenceStatus
		acDietNotes         *string
		acAdvanceDirective  *bool
		acFundingLevel      *domain.AgedCareFundingLevel
		acAdmissionDate     *time.Time
		acPrimaryGP         *string
		acCreatedAt         *time.Time
		acUpdatedAt         *time.Time
	)

	if err := rows.Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical, &s.PhotoURL, &s.PhotoKey,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
		&cID, &cClinicID, &cFullName, &cPhone, &cEmail, &cEmailHash, &cAddress, &cCreatedAt, &cUpdatedAt,
		&dSubjectID, &dSpecies, &dBreed, &dSex, &dDesexed, &dDOB, &dColor, &dMicrochip, &dWeightKg,
		&dAllergies, &dChronicConditions, &dAdmissionWarn, &dInsProvider, &dInsPolicy, &dReferringVet,
		&dCreatedAt, &dUpdatedAt,
		&ddSubjectID, &ddDOB, &ddSex, &ddMedicalAlerts, &ddMedications,
		&ddAllergies, &ddChronicConditions, &ddAdmissionWarn,
		&ddInsProvider, &ddInsPolicy, &ddReferringDentist, &ddPrimaryDentist,
		&ddCreatedAt, &ddUpdatedAt,
		&gSubjectID, &gDOB, &gSex, &gMedicalAlerts, &gMedications,
		&gAllergies, &gChronicConditions, &gAdmissionWarn,
		&gInsProvider, &gInsPolicy, &gReferringProvider, &gPrimaryProvider,
		&gCreatedAt, &gUpdatedAt,
		&acSubjectID, &acDOB, &acSex, &acRoom, &acNHI, &acMedicare,
		&acEthnicity, &acLanguage, &acMedicalAlerts, &acMedications,
		&acAllergies, &acChronicConditions, &acCognitive, &acMobility,
		&acContinence, &acDietNotes, &acAdvanceDirective, &acFundingLevel,
		&acAdmissionDate, &acPrimaryGP, &acCreatedAt, &acUpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("patient.repo.scanSubjectRow: %w", err)
	}

	row.Subject = *s

	if cID != nil {
		c.ID = *cID
		c.ClinicID = *cClinicID
		c.FullName = *cFullName
		c.Phone = cPhone
		c.Email = cEmail
		c.EmailHash = cEmailHash
		c.Address = cAddress
		c.CreatedAt = *cCreatedAt
		c.UpdatedAt = *cUpdatedAt
		row.Contact = c
	}

	if dSubjectID != nil {
		d.SubjectID = *dSubjectID
		d.Species = *dSpecies
		d.Breed = dBreed
		d.Sex = dSex
		d.Desexed = dDesexed
		d.DateOfBirth = dDOB
		d.Color = dColor
		d.Microchip = dMicrochip
		d.WeightKg = dWeightKg
		d.Allergies = dAllergies
		d.ChronicConditions = dChronicConditions
		d.AdmissionWarnings = dAdmissionWarn
		d.InsuranceProviderName = dInsProvider
		d.InsurancePolicyNumber = dInsPolicy
		d.ReferringVetName = dReferringVet
		d.CreatedAt = *dCreatedAt
		d.UpdatedAt = *dUpdatedAt
		row.VetDetails = d
	}

	if ddSubjectID != nil {
		dd.SubjectID = *ddSubjectID
		dd.DateOfBirth = ddDOB
		dd.Sex = ddSex
		dd.MedicalAlerts = ddMedicalAlerts
		dd.Medications = ddMedications
		dd.Allergies = ddAllergies
		dd.ChronicConditions = ddChronicConditions
		dd.AdmissionWarnings = ddAdmissionWarn
		dd.InsuranceProviderName = ddInsProvider
		dd.InsurancePolicyNumber = ddInsPolicy
		dd.ReferringDentistName = ddReferringDentist
		dd.PrimaryDentistName = ddPrimaryDentist
		dd.CreatedAt = *ddCreatedAt
		dd.UpdatedAt = *ddUpdatedAt
		row.DentalDetails = dd
	}

	if gSubjectID != nil {
		g.SubjectID = *gSubjectID
		g.DateOfBirth = gDOB
		g.Sex = gSex
		g.MedicalAlerts = gMedicalAlerts
		g.Medications = gMedications
		g.Allergies = gAllergies
		g.ChronicConditions = gChronicConditions
		g.AdmissionWarnings = gAdmissionWarn
		g.InsuranceProviderName = gInsProvider
		g.InsurancePolicyNumber = gInsPolicy
		g.ReferringProviderName = gReferringProvider
		g.PrimaryProviderName = gPrimaryProvider
		g.CreatedAt = *gCreatedAt
		g.UpdatedAt = *gUpdatedAt
		row.GeneralDetails = g
	}

	if acSubjectID != nil {
		ac.SubjectID = *acSubjectID
		ac.DateOfBirth = acDOB
		ac.Sex = acSex
		ac.Room = acRoom
		ac.NHINumber = acNHI
		ac.MedicareNumber = acMedicare
		ac.Ethnicity = acEthnicity
		ac.PreferredLanguage = acLanguage
		ac.MedicalAlerts = acMedicalAlerts
		ac.Medications = acMedications
		ac.Allergies = acAllergies
		ac.ChronicConditions = acChronicConditions
		ac.CognitiveStatus = acCognitive
		ac.MobilityStatus = acMobility
		ac.ContinenceStatus = acContinence
		ac.DietNotes = acDietNotes
		if acAdvanceDirective != nil {
			ac.AdvanceDirectiveFlag = *acAdvanceDirective
		}
		ac.FundingLevel = acFundingLevel
		ac.AdmissionDate = acAdmissionDate
		ac.PrimaryGPName = acPrimaryGP
		ac.CreatedAt = *acCreatedAt
		ac.UpdatedAt = *acUpdatedAt
		row.AgedCareDetails = ac
	}

	return row, nil
}

// CreateSubjectAccessLog writes a single subject access-log entry.
// Access-log writes are append-only; there is no update or delete API.
func (r *Repository) CreateSubjectAccessLog(ctx context.Context, p CreateSubjectAccessLogParams) (*SubjectAccessLogRecord, error) {
	rec := &SubjectAccessLogRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO subject_access_log
			(id, subject_id, staff_id, clinic_id, action, purpose, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, subject_id, staff_id, clinic_id, action, purpose, at
	`, p.ID, p.SubjectID, p.StaffID, p.ClinicID, p.Action, p.Purpose, domain.TimeNow(),
	).Scan(
		&rec.ID, &rec.SubjectID, &rec.StaffID, &rec.ClinicID,
		&rec.Action, &rec.Purpose, &rec.At,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateSubjectAccessLog: %w", err)
	}
	return rec, nil
}

// ListSubjectAccessLog returns the subject access-log entries for a
// clinic within [from, to]. Used by the HIPAA disclosure-log report
// builder. Append-only table → no need for Total / pagination
// safeguards; the report worker pages explicitly.
func (r *Repository) ListSubjectAccessLog(ctx context.Context, clinicID uuid.UUID, from, to time.Time, limit, offset int) ([]*SubjectAccessLogRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, subject_id, staff_id, clinic_id, action, purpose, at
		FROM subject_access_log
		WHERE clinic_id = $1 AND at >= $2 AND at <= $3
		ORDER BY at DESC
		LIMIT $4 OFFSET $5`,
		clinicID, from, to, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.ListSubjectAccessLog: %w", err)
	}
	defer rows.Close()
	var out []*SubjectAccessLogRecord
	for rows.Next() {
		var rec SubjectAccessLogRecord
		if err := rows.Scan(&rec.ID, &rec.SubjectID, &rec.StaffID, &rec.ClinicID, &rec.Action, &rec.Purpose, &rec.At); err != nil {
			return nil, fmt.Errorf("patient.repo.ListSubjectAccessLog: scan: %w", err)
		}
		out = append(out, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("patient.repo.ListSubjectAccessLog: rows: %w", err)
	}
	return out, nil
}

// CreateSubjectContact inserts a (subject, contact, role) binding.
// Returns ErrConflict if the same role already exists for this pair.
func (r *Repository) CreateSubjectContact(
	ctx context.Context,
	clinicID uuid.UUID,
	p CreateSubjectContactParams,
) (*SubjectContactRecord, error) {
	// Guard that both rows belong to the calling clinic before inserting.
	var subjectClinic, contactClinic uuid.UUID
	if err := r.db.QueryRow(ctx, `
		SELECT clinic_id FROM subjects WHERE id = $1 AND archived_at IS NULL
	`, p.SubjectID).Scan(&subjectClinic); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.CreateSubjectContact: subject: %w", err)
	}
	if subjectClinic != clinicID {
		return nil, domain.ErrNotFound
	}
	if err := r.db.QueryRow(ctx, `
		SELECT clinic_id FROM contacts WHERE id = $1 AND archived_at IS NULL
	`, p.ContactID).Scan(&contactClinic); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.CreateSubjectContact: contact: %w", err)
	}
	if contactClinic != clinicID {
		return nil, domain.ErrNotFound
	}

	rec := &SubjectContactRecord{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO subject_contacts (subject_id, contact_id, role, note)
		VALUES ($1, $2, $3, $4)
		RETURNING subject_id, contact_id, role, note, created_at, updated_at
	`, p.SubjectID, p.ContactID, p.Role, p.Note,
	).Scan(
		&rec.SubjectID, &rec.ContactID, &rec.Role, &rec.Note,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, domain.ErrConflict
		}
		return nil, fmt.Errorf("patient.repo.CreateSubjectContact: %w", err)
	}
	return rec, nil
}

// DeleteSubjectContact removes a (subject, contact, role) binding.
// Returns ErrNotFound when the row is absent or outside the caller's clinic.
func (r *Repository) DeleteSubjectContact(
	ctx context.Context,
	clinicID, subjectID, contactID uuid.UUID,
	role domain.SubjectContactRole,
) error {
	tag, err := r.db.Exec(ctx, `
		DELETE FROM subject_contacts sc
		USING subjects s
		WHERE sc.subject_id = $1
		  AND sc.contact_id = $2
		  AND sc.role       = $3
		  AND sc.subject_id = s.id
		  AND s.clinic_id   = $4
	`, subjectID, contactID, role, clinicID)
	if err != nil {
		return fmt.Errorf("patient.repo.DeleteSubjectContact: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListSubjectContacts returns all (contact, role) bindings for a subject,
// joined to the contact rows so the caller can decrypt in one pass.
func (r *Repository) ListSubjectContacts(
	ctx context.Context,
	clinicID, subjectID uuid.UUID,
) ([]*SubjectContactWithContact, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sc.role, sc.note,
		       c.id, c.clinic_id, c.full_name, c.phone, c.email, c.email_hash, c.address,
		       c.created_at, c.updated_at, c.archived_at
		FROM subject_contacts sc
		JOIN subjects s ON s.id = sc.subject_id
		JOIN contacts c ON c.id = sc.contact_id
		WHERE sc.subject_id = $1
		  AND s.clinic_id   = $2
		  AND s.archived_at IS NULL
		  AND c.archived_at IS NULL
		ORDER BY sc.created_at ASC
	`, subjectID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.ListSubjectContacts: query: %w", err)
	}
	defer rows.Close()

	var out []*SubjectContactWithContact
	for rows.Next() {
		c := &ContactRecord{}
		link := &SubjectContactWithContact{Contact: c}
		if err := rows.Scan(
			&link.Role, &link.Note,
			&c.ID, &c.ClinicID, &c.FullName, &c.Phone, &c.Email, &c.EmailHash, &c.Address,
			&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
		); err != nil {
			return nil, fmt.Errorf("patient.repo.ListSubjectContacts: scan: %w", err)
		}
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("patient.repo.ListSubjectContacts: rows: %w", err)
	}
	return out, nil
}
