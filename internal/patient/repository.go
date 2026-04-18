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
// Only one of VetDetails / DentalDetails / GeneralDetails is non-nil for any given subject.
type SubjectRow struct {
	Subject        SubjectRecord
	Contact        *ContactRecord        // nil if no contact linked
	VetDetails     *VetDetailsRecord     // nil if not a vet vertical
	DentalDetails  *DentalDetailsRecord  // nil if not a dental vertical
	GeneralDetails *GeneralDetailsRecord // nil if not a general_clinic vertical
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
type UpdateSubjectParams struct {
	DisplayName *string
	Status      *domain.SubjectStatus
	ContactID   *uuid.UUID
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
		INSERT INTO subjects (id, clinic_id, contact_id, display_name, status, vertical, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, clinic_id, contact_id, display_name, status, vertical,
		          created_by, created_at, updated_at, archived_at
	`, p.ID, p.ClinicID, p.ContactID, p.DisplayName, p.Status, p.Vertical, p.CreatedBy).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
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
	)

	err := r.db.QueryRow(ctx, `
		SELECT
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical,
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
			g.created_at, g.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
		WHERE s.id = $1 AND s.clinic_id = $2 AND s.archived_at IS NULL
	`, id, clinicID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
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

	// Count total matching rows.
	var total int
	countQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM subjects s
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
		WHERE %s
	`, where)
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("patient.repo.ListSubjects: count: %w", err)
	}

	// Fetch page.
	args = append(args, p.Limit, p.Offset)
	listQuery := fmt.Sprintf(`
		SELECT
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical,
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
			g.created_at, g.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
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
			updated_at   = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, contact_id, display_name, status, vertical,
		          created_by, created_at, updated_at, archived_at
	`, id, clinicID, p.DisplayName, p.Status, p.ContactID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
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

// ArchiveSubject soft-deletes a subject.
func (r *Repository) ArchiveSubject(ctx context.Context, id, clinicID uuid.UUID) (*SubjectRecord, error) {
	s := &SubjectRecord{}
	err := r.db.QueryRow(ctx, `
		UPDATE subjects
		SET status = 'archived', archived_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING id, clinic_id, contact_id, display_name, status, vertical,
		          created_by, created_at, updated_at, archived_at
	`, id, clinicID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
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
		RETURNING id, clinic_id, contact_id, display_name, status, vertical,
		          created_by, created_at, updated_at, archived_at
	`, subjectID, clinicID, contactID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
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
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical,
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
			g.created_at, g.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		LEFT JOIN dental_subject_details dd ON dd.subject_id = s.id
		LEFT JOIN general_subject_details g ON g.subject_id = s.id
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
	)

	if err := rows.Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
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
