package patient

import (
	"context"

	"github.com/google/uuid"
)

// repo is the interface the Service uses for all database operations.
// The real implementation is Repository. Tests use fakeRepo.
type repo interface {
	// ── Contacts ──────────────────────────────────────────────────────────────

	// CreateContact inserts a new contact and returns the created record.
	CreateContact(ctx context.Context, p CreateContactParams) (*ContactRecord, error)

	// GetContactByID fetches a contact by ID within a clinic.
	GetContactByID(ctx context.Context, id, clinicID uuid.UUID) (*ContactRecord, error)

	// ListContacts returns a page of contacts for a clinic.
	ListContacts(ctx context.Context, clinicID uuid.UUID, p ListParams) ([]*ContactRecord, int, error)

	// UpdateContact applies a partial update to a contact and returns the updated record.
	UpdateContact(ctx context.Context, id, clinicID uuid.UUID, p UpdateContactParams) (*ContactRecord, error)

	// ── Subjects ──────────────────────────────────────────────────────────────

	// CreateSubject inserts a new subject row.
	CreateSubject(ctx context.Context, p CreateSubjectParams) (*SubjectRecord, error)

	// CreateVetDetails inserts a vet_subject_details row for a subject.
	CreateVetDetails(ctx context.Context, p CreateVetDetailsParams) (*VetDetailsRecord, error)

	// GetSubjectByID fetches a subject with its contact and vet details joined.
	GetSubjectByID(ctx context.Context, id, clinicID uuid.UUID) (*SubjectRow, error)

	// ListSubjects returns a page of subjects with optional filters.
	ListSubjects(ctx context.Context, clinicID uuid.UUID, p ListSubjectsParams) ([]*SubjectRow, int, error)

	// UpdateSubject applies a partial update to a subject row.
	UpdateSubject(ctx context.Context, id, clinicID uuid.UUID, p UpdateSubjectParams) (*SubjectRecord, error)

	// UpdateVetDetails applies a partial update to a vet_subject_details row.
	UpdateVetDetails(ctx context.Context, subjectID uuid.UUID, p UpdateVetDetailsParams) (*VetDetailsRecord, error)

	// CreateDentalDetails inserts a dental_subject_details row for a subject.
	CreateDentalDetails(ctx context.Context, p CreateDentalDetailsParams) (*DentalDetailsRecord, error)

	// UpdateDentalDetails applies a partial update to a dental_subject_details row.
	UpdateDentalDetails(ctx context.Context, subjectID uuid.UUID, p UpdateDentalDetailsParams) (*DentalDetailsRecord, error)

	// CreateGeneralDetails inserts a general_subject_details row for a subject.
	CreateGeneralDetails(ctx context.Context, p CreateGeneralDetailsParams) (*GeneralDetailsRecord, error)

	// UpdateGeneralDetails applies a partial update to a general_subject_details row.
	UpdateGeneralDetails(ctx context.Context, subjectID uuid.UUID, p UpdateGeneralDetailsParams) (*GeneralDetailsRecord, error)

	// ArchiveSubject soft-deletes a subject by setting archived_at.
	ArchiveSubject(ctx context.Context, id, clinicID uuid.UUID) (*SubjectRecord, error)

	// LinkContact sets contact_id on an existing subject.
	LinkContact(ctx context.Context, subjectID, clinicID, contactID uuid.UUID) (*SubjectRecord, error)

	// ListSubjectsByContact returns all active subjects for a given contact.
	ListSubjectsByContact(ctx context.Context, contactID, clinicID uuid.UUID) ([]*SubjectRow, error)

	// ── Access log ────────────────────────────────────────────────────────────

	// CreateSubjectAccessLog appends an audit entry for subject access.
	CreateSubjectAccessLog(ctx context.Context, p CreateSubjectAccessLogParams) (*SubjectAccessLogRecord, error)
}
