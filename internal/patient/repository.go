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
type VetDetailsRecord struct {
	SubjectID   uuid.UUID
	Species     domain.VetSpecies
	Breed       *string
	Sex         *domain.VetSex
	Desexed     *bool
	DateOfBirth *time.Time
	Color       *string
	Microchip   *string
	WeightKg    *float64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SubjectRow is a fully joined subject — subject + contact + vet details.
// This is what the service receives and decrypts before returning to handlers.
type SubjectRow struct {
	Subject    SubjectRecord
	Contact    *ContactRecord    // nil if no contact linked
	VetDetails *VetDetailsRecord // nil if not a vet vertical
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
type CreateVetDetailsParams struct {
	SubjectID   uuid.UUID
	Species     domain.VetSpecies
	Breed       *string
	Sex         *domain.VetSex
	Desexed     *bool
	DateOfBirth *time.Time
	Color       *string
	Microchip   *string
	WeightKg    *float64
}

// UpdateSubjectParams holds fields for a partial subject update.
type UpdateSubjectParams struct {
	DisplayName *string
	Status      *domain.SubjectStatus
	ContactID   *uuid.UUID
}

// UpdateVetDetailsParams holds fields for a partial vet details update.
type UpdateVetDetailsParams struct {
	Breed       *string
	Sex         *domain.VetSex
	Desexed     *bool
	DateOfBirth *time.Time
	Color       *string
	Microchip   *string
	WeightKg    *float64
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
			(subject_id, species, breed, sex, desexed, date_of_birth, color, microchip, weight_kg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING subject_id, species, breed, sex, desexed, date_of_birth, color,
		          microchip, weight_kg, created_at, updated_at
	`, p.SubjectID, p.Species, p.Breed, p.Sex, p.Desexed, p.DateOfBirth, p.Color, p.Microchip, p.WeightKg).Scan(
		&d.SubjectID, &d.Species, &d.Breed, &d.Sex, &d.Desexed, &d.DateOfBirth, &d.Color,
		&d.Microchip, &d.WeightKg, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("patient.repo.CreateVetDetails: %w", err)
	}
	return d, nil
}

// GetSubjectByID fetches a subject with its contact and vet details joined.
func (r *Repository) GetSubjectByID(ctx context.Context, id, clinicID uuid.UUID) (*SubjectRow, error) {
	row := &SubjectRow{}
	s := &SubjectRecord{}
	c := &ContactRecord{}
	d := &VetDetailsRecord{}

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
		dSubjectID *uuid.UUID
		dSpecies   *domain.VetSpecies
		dBreed     *string
		dSex       *domain.VetSex
		dDesexed   *bool
		dDOB       *time.Time
		dColor     *string
		dMicrochip *string
		dWeightKg  *float64
		dCreatedAt *time.Time
		dUpdatedAt *time.Time
	)

	err := r.db.QueryRow(ctx, `
		SELECT
			s.id, s.clinic_id, s.contact_id, s.display_name, s.status, s.vertical,
			s.created_by, s.created_at, s.updated_at, s.archived_at,
			c.id, c.clinic_id, c.full_name, c.phone, c.email, c.email_hash, c.address,
			c.created_at, c.updated_at,
			v.subject_id, v.species, v.breed, v.sex, v.desexed, v.date_of_birth,
			v.color, v.microchip, v.weight_kg, v.created_at, v.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
		WHERE s.id = $1 AND s.clinic_id = $2 AND s.archived_at IS NULL
	`, id, clinicID).Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
		&cID, &cClinicID, &cFullName, &cPhone, &cEmail, &cEmailHash, &cAddress, &cCreatedAt, &cUpdatedAt,
		&dSubjectID, &dSpecies, &dBreed, &dSex, &dDesexed, &dDOB, &dColor, &dMicrochip, &dWeightKg, &dCreatedAt, &dUpdatedAt,
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
		d.CreatedAt = *dCreatedAt
		d.UpdatedAt = *dUpdatedAt
		row.VetDetails = d
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
			v.color, v.microchip, v.weight_kg, v.created_at, v.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
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
			breed        = COALESCE($2, breed),
			sex          = COALESCE($3, sex),
			desexed      = COALESCE($4, desexed),
			date_of_birth = COALESCE($5, date_of_birth),
			color        = COALESCE($6, color),
			microchip    = COALESCE($7, microchip),
			weight_kg    = COALESCE($8, weight_kg),
			updated_at   = NOW()
		WHERE subject_id = $1
		RETURNING subject_id, species, breed, sex, desexed, date_of_birth, color,
		          microchip, weight_kg, created_at, updated_at
	`, subjectID, p.Breed, p.Sex, p.Desexed, p.DateOfBirth, p.Color, p.Microchip, p.WeightKg).Scan(
		&d.SubjectID, &d.Species, &d.Breed, &d.Sex, &d.Desexed, &d.DateOfBirth, &d.Color,
		&d.Microchip, &d.WeightKg, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("patient.repo.UpdateVetDetails: %w", err)
	}
	return d, nil
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
			v.color, v.microchip, v.weight_kg, v.created_at, v.updated_at
		FROM subjects s
		LEFT JOIN contacts c ON c.id = s.contact_id AND c.archived_at IS NULL
		LEFT JOIN vet_subject_details v ON v.subject_id = s.id
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

		dSubjectID *uuid.UUID
		dSpecies   *domain.VetSpecies
		dBreed     *string
		dSex       *domain.VetSex
		dDesexed   *bool
		dDOB       *time.Time
		dColor     *string
		dMicrochip *string
		dWeightKg  *float64
		dCreatedAt *time.Time
		dUpdatedAt *time.Time
	)

	if err := rows.Scan(
		&s.ID, &s.ClinicID, &s.ContactID, &s.DisplayName, &s.Status, &s.Vertical,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
		&cID, &cClinicID, &cFullName, &cPhone, &cEmail, &cEmailHash, &cAddress, &cCreatedAt, &cUpdatedAt,
		&dSubjectID, &dSpecies, &dBreed, &dSex, &dDesexed, &dDOB, &dColor, &dMicrochip, &dWeightKg, &dCreatedAt, &dUpdatedAt,
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
		d.CreatedAt = *dCreatedAt
		d.UpdatedAt = *dUpdatedAt
		row.VetDetails = d
	}

	return row, nil
}
