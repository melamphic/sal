//go:build integration

package patient_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/patient"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.IntegrationMain(m)
}

func newRepo(t *testing.T) *patient.Repository {
	t.Helper()
	return patient.NewRepository(testutil.NewTestDB(t))
}

// seedClinic inserts a minimal clinic row to satisfy FK constraints.
func seedClinic(t *testing.T, clinicID uuid.UUID) {
	t.Helper()
	pool := testutil.NewTestDB(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'Test Clinic','tc-'||$1::text,'enc','ch-'||$1::text,'veterinary','trial',NOW()+interval'21d','ap-southeast-2')
		ON CONFLICT DO NOTHING
	`, clinicID)
	require.NoError(t, err)
}

// seedStaff inserts a minimal staff row to satisfy the created_by FK on subjects.
func seedStaff(t *testing.T, clinicID, staffID uuid.UUID) {
	t.Helper()
	pool := testutil.NewTestDB(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO staff (id, clinic_id, email, email_hash, full_name, role, status)
		VALUES ($1, $2, 'enc', 'hash-'||$1::text, 'enc', 'vet', 'active')
		ON CONFLICT DO NOTHING
	`, staffID, clinicID)
	require.NoError(t, err)
}

// ── Contacts ──────────────────────────────────────────────────────────────────

func TestRepository_CreateContact_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	email := "enc-email"
	hash := "hash-" + uuid.New().String()[:8]
	p := patient.CreateContactParams{
		ID:        domain.NewID(),
		ClinicID:  clinicID,
		FullName:  "enc-name",
		Email:     &email,
		EmailHash: &hash,
	}

	c, err := r.CreateContact(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, p.ID, c.ID)
	assert.Equal(t, clinicID, c.ClinicID)
	assert.Equal(t, "enc-name", c.FullName)
	assert.False(t, c.CreatedAt.IsZero())
}

func TestRepository_GetContactByID_Found(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := patient.CreateContactParams{
		ID: domain.NewID(), ClinicID: clinicID, FullName: "enc",
	}
	created, err := r.CreateContact(ctx, p)
	require.NoError(t, err)

	got, err := r.GetContactByID(ctx, created.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
}

func TestRepository_GetContactByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := patient.CreateContactParams{ID: domain.NewID(), ClinicID: clinicID, FullName: "enc"}
	created, err := r.CreateContact(ctx, p)
	require.NoError(t, err)

	_, err = r.GetContactByID(ctx, created.ID, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestRepository_UpdateContact_UpdatesFields(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	created, err := r.CreateContact(ctx, patient.CreateContactParams{
		ID: domain.NewID(), ClinicID: clinicID, FullName: "old-enc",
	})
	require.NoError(t, err)

	newName := "new-enc"
	updated, err := r.UpdateContact(ctx, created.ID, clinicID, patient.UpdateContactParams{FullName: &newName})
	require.NoError(t, err)
	assert.Equal(t, "new-enc", updated.FullName)
}

func TestRepository_ListContacts_ReturnsOnlyClinicContacts(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicA := domain.NewID()
	clinicB := domain.NewID()
	seedClinic(t, clinicA)
	seedClinic(t, clinicB)

	for range 3 {
		_, err := r.CreateContact(ctx, patient.CreateContactParams{
			ID: domain.NewID(), ClinicID: clinicA, FullName: "enc",
		})
		require.NoError(t, err)
	}
	_, err := r.CreateContact(ctx, patient.CreateContactParams{
		ID: domain.NewID(), ClinicID: clinicB, FullName: "enc",
	})
	require.NoError(t, err)

	contacts, total, err := r.ListContacts(ctx, clinicA, patient.ListParams{Limit: 20, Offset: 0})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, contacts, 3)
}

// ── Subjects ──────────────────────────────────────────────────────────────────

func TestRepository_CreateSubject_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID:          domain.NewID(),
		ClinicID:    clinicID,
		DisplayName: "Buddy",
		Status:      domain.SubjectStatusActive,
		Vertical:    domain.VerticalVeterinary,
		CreatedBy:   staffID,
	})
	require.NoError(t, err)
	assert.Equal(t, "Buddy", s.DisplayName)
	assert.Equal(t, domain.SubjectStatusActive, s.Status)
	assert.Nil(t, s.ContactID)
}

func TestRepository_CreateVetDetails_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Bella",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)

	breed := "Labrador"
	d, err := r.CreateVetDetails(ctx, patient.CreateVetDetailsParams{
		SubjectID: s.ID,
		Species:   domain.VetSpeciesDog,
		Breed:     &breed,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.VetSpeciesDog, d.Species)
	assert.Equal(t, &breed, d.Breed)
}

func TestRepository_GetSubjectByID_WithContactAndVetDetails(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	contact, err := r.CreateContact(ctx, patient.CreateContactParams{
		ID: domain.NewID(), ClinicID: clinicID, FullName: "enc-owner",
	})
	require.NoError(t, err)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, ContactID: &contact.ID,
		DisplayName: "Max", Status: domain.SubjectStatusActive,
		Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)

	_, err = r.CreateVetDetails(ctx, patient.CreateVetDetailsParams{
		SubjectID: s.ID, Species: domain.VetSpeciesDog,
	})
	require.NoError(t, err)

	row, err := r.GetSubjectByID(ctx, s.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, "Max", row.Subject.DisplayName)
	require.NotNil(t, row.Contact)
	assert.Equal(t, contact.ID, row.Contact.ID)
	require.NotNil(t, row.VetDetails)
	assert.Equal(t, domain.VetSpeciesDog, row.VetDetails.Species)
}

func TestRepository_GetSubjectByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Max",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)

	_, err = r.GetSubjectByID(ctx, s.ID, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestRepository_ListSubjects_ReturnsOnlyClinicSubjects(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicA := domain.NewID()
	clinicB := domain.NewID()
	staffA := domain.NewID()
	staffB := domain.NewID()
	seedClinic(t, clinicA)
	seedClinic(t, clinicB)
	seedStaff(t, clinicA, staffA)
	seedStaff(t, clinicB, staffB)

	for range 3 {
		_, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
			ID: domain.NewID(), ClinicID: clinicA, DisplayName: "Dog",
			Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffA,
		})
		require.NoError(t, err)
	}
	_, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicB, DisplayName: "Cat",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffB,
	})
	require.NoError(t, err)

	rows, total, err := r.ListSubjects(ctx, clinicA, patient.ListSubjectsParams{Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, rows, 3)
}

func TestRepository_ArchiveSubject_SetsArchivedAt(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Buddy",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)

	archived, err := r.ArchiveSubject(ctx, s.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubjectStatusArchived, archived.Status)
	assert.NotNil(t, archived.ArchivedAt)

	// Archived subject must not be findable.
	_, err = r.GetSubjectByID(ctx, s.ID, clinicID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestRepository_LinkContact_UpdatesSubject(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Buddy",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)
	assert.Nil(t, s.ContactID)

	contact, err := r.CreateContact(ctx, patient.CreateContactParams{
		ID: domain.NewID(), ClinicID: clinicID, FullName: "enc-owner",
	})
	require.NoError(t, err)

	updated, err := r.LinkContact(ctx, s.ID, clinicID, contact.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.ContactID)
	assert.Equal(t, contact.ID, *updated.ContactID)
}

func TestRepository_ListSubjectsByContact_ReturnsSubjects(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	contact, err := r.CreateContact(ctx, patient.CreateContactParams{
		ID: domain.NewID(), ClinicID: clinicID, FullName: "enc-owner",
	})
	require.NoError(t, err)

	for range 2 {
		s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
			ID: domain.NewID(), ClinicID: clinicID, ContactID: &contact.ID,
			DisplayName: "Pet", Status: domain.SubjectStatusActive,
			Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
		})
		require.NoError(t, err)
		_, err = r.CreateVetDetails(ctx, patient.CreateVetDetailsParams{
			SubjectID: s.ID, Species: domain.VetSpeciesDog,
		})
		require.NoError(t, err)
	}

	// Unlinked subject — should not appear.
	_, err = r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Unlinked",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)

	rows, err := r.ListSubjectsByContact(ctx, contact.ID, clinicID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

// ── Dental details ────────────────────────────────────────────────────────────

func TestRepository_CreateDentalDetails_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Jane Doe",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalDental, CreatedBy: staffID,
	})
	require.NoError(t, err)

	sex := domain.DentalSexFemale
	alerts := "enc-alerts"
	d, err := r.CreateDentalDetails(ctx, patient.CreateDentalDetailsParams{
		SubjectID:     s.ID,
		Sex:           &sex,
		MedicalAlerts: &alerts,
	})
	require.NoError(t, err)
	assert.Equal(t, s.ID, d.SubjectID)
	require.NotNil(t, d.Sex)
	assert.Equal(t, domain.DentalSexFemale, *d.Sex)
	require.NotNil(t, d.MedicalAlerts)
	assert.Equal(t, "enc-alerts", *d.MedicalAlerts)
}

// ── General clinic details ────────────────────────────────────────────────────

func TestRepository_CreateGeneralDetails_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "John Doe",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalGeneralClinic, CreatedBy: staffID,
	})
	require.NoError(t, err)

	sex := domain.GeneralSexMale
	meds := "enc-meds"
	g, err := r.CreateGeneralDetails(ctx, patient.CreateGeneralDetailsParams{
		SubjectID:   s.ID,
		Sex:         &sex,
		Medications: &meds,
	})
	require.NoError(t, err)
	assert.Equal(t, s.ID, g.SubjectID)
	require.NotNil(t, g.Medications)
	assert.Equal(t, "enc-meds", *g.Medications)
}

// ── Subject access log ────────────────────────────────────────────────────────

func TestRepository_CreateSubjectAccessLog_Appends(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	s, err := r.CreateSubject(ctx, patient.CreateSubjectParams{
		ID: domain.NewID(), ClinicID: clinicID, DisplayName: "Rex",
		Status: domain.SubjectStatusActive, Vertical: domain.VerticalVeterinary, CreatedBy: staffID,
	})
	require.NoError(t, err)

	purpose := "pharmacy lookup"
	rec, err := r.CreateSubjectAccessLog(ctx, patient.CreateSubjectAccessLogParams{
		ID:        domain.NewID(),
		SubjectID: s.ID,
		StaffID:   staffID,
		ClinicID:  clinicID,
		Action:    domain.SubjectAccessActionUnmaskPII,
		Purpose:   &purpose,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.SubjectAccessActionUnmaskPII, rec.Action)
	require.NotNil(t, rec.Purpose)
	assert.Equal(t, "pharmacy lookup", *rec.Purpose)
	assert.False(t, rec.At.IsZero())
}
