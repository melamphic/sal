package patient

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	r := newFakeRepo()
	c := testutil.TestCipher(t)
	return NewService(r, c), r
}

// seedContact creates a contact via the service and returns the DTO.
func seedContact(t *testing.T, svc *Service, clinicID uuid.UUID, name, email string) *ContactResponse {
	t.Helper()
	dto, err := svc.CreateContact(context.Background(), CreateContactInput{
		ClinicID: clinicID,
		FullName: name,
		Email:    &email,
	})
	require.NoError(t, err)
	return dto
}

// seedSubject creates a vet subject via the service and returns the DTO.
func seedSubject(t *testing.T, svc *Service, clinicID, callerID uuid.UUID, name string) *SubjectResponse {
	t.Helper()
	species := domain.VetSpeciesDog
	dto, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalVeterinary,
		DisplayName: name,
		VetDetails:  &VetDetailsInput{Species: species},
	})
	require.NoError(t, err)
	return dto
}

// ── Contact: Create ───────────────────────────────────────────────────────────

func TestService_CreateContact_ReturnsDecryptedDTO(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	email := "owner@clinic.co.nz"

	dto, err := svc.CreateContact(context.Background(), CreateContactInput{
		ClinicID: clinicID,
		FullName: "Jane Smith",
		Email:    &email,
	})

	require.NoError(t, err)
	assert.Equal(t, "Jane Smith", dto.FullName)
	assert.Equal(t, email, *dto.Email)
	assert.Equal(t, clinicID.String(), dto.ClinicID)
}

func TestService_CreateContact_PIIStoredEncrypted(t *testing.T) {
	t.Parallel()
	svc, r := newTestService(t)
	clinicID := uuid.New()
	email := "secret@clinic.co.nz"

	dto, err := svc.CreateContact(context.Background(), CreateContactInput{
		ClinicID: clinicID,
		FullName: "Secret Owner",
		Email:    &email,
	})
	require.NoError(t, err)

	id, err := uuid.Parse(dto.ID)
	require.NoError(t, err)
	stored := r.contacts[id]
	require.NotNil(t, stored)
	assert.NotEqual(t, "Secret Owner", stored.FullName, "name must be encrypted at rest")
	assert.NotEqual(t, email, stored.Email, "email must be encrypted at rest")
	assert.NotNil(t, stored.EmailHash, "email hash must be stored for lookup")
}

func TestService_CreateContact_WithoutEmail_Succeeds(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	dto, err := svc.CreateContact(context.Background(), CreateContactInput{
		ClinicID: uuid.New(),
		FullName: "Walk-in Owner",
	})

	require.NoError(t, err)
	assert.Nil(t, dto.Email)
}

// ── Contact: GetByID ──────────────────────────────────────────────────────────

func TestService_GetContactByID_ReturnsDecryptedData(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedContact(t, svc, clinicID, "Tom Jones", "tom@example.com")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	dto, err := svc.GetContactByID(context.Background(), id, clinicID)
	require.NoError(t, err)
	assert.Equal(t, "Tom Jones", dto.FullName)
	assert.Equal(t, "tom@example.com", *dto.Email)
}

func TestService_GetContactByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedContact(t, svc, clinicID, "Tom Jones", "tom@example.com")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	_, err = svc.GetContactByID(context.Background(), id, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Contact: Update ───────────────────────────────────────────────────────────

func TestService_UpdateContact_UpdatesFields(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedContact(t, svc, clinicID, "Old Name", "old@example.com")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	newName := "New Name"
	newEmail := "new@example.com"
	dto, err := svc.UpdateContact(context.Background(), id, clinicID, UpdateContactInput{
		FullName: &newName,
		Email:    &newEmail,
	})
	require.NoError(t, err)
	assert.Equal(t, "New Name", dto.FullName)
	assert.Equal(t, "new@example.com", *dto.Email)
}

// ── Subject: Create ───────────────────────────────────────────────────────────

func TestService_CreateSubject_VetVertical_ReturnsDecryptedDTO(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	breed := "Labrador"
	species := domain.VetSpeciesDog

	dto, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalVeterinary,
		DisplayName: "Buddy",
		VetDetails: &VetDetailsInput{
			Species: species,
			Breed:   &breed,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "Buddy", dto.DisplayName)
	assert.Equal(t, domain.VerticalVeterinary, dto.Vertical)
	assert.Equal(t, domain.SubjectStatusActive, dto.Status)
	require.NotNil(t, dto.VetDetails)
	assert.Equal(t, domain.VetSpeciesDog, dto.VetDetails.Species)
	assert.Equal(t, &breed, dto.VetDetails.Breed)
}

func TestService_CreateSubject_VetVertical_MissingVetDetails_ReturnsValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    uuid.New(),
		CallerID:    uuid.New(),
		Vertical:    domain.VerticalVeterinary,
		DisplayName: "No Details",
		VetDetails:  nil,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestService_CreateSubject_WithContact_LinksContact(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	contact := seedContact(t, svc, clinicID, "Pet Owner", "owner@clinic.co.nz")
	contactID, err := uuid.Parse(contact.ID)
	require.NoError(t, err)

	species := domain.VetSpeciesCat
	dto, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalVeterinary,
		DisplayName: "Whiskers",
		ContactID:   &contactID,
		VetDetails:  &VetDetailsInput{Species: species},
	})

	require.NoError(t, err)
	require.NotNil(t, dto.Contact)
	assert.Equal(t, "Pet Owner", dto.Contact.FullName)
}

// ── Subject: GetByID ──────────────────────────────────────────────────────────

func TestService_GetSubjectByID_ViewAllPatients_ReturnsAny(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	creatorID := uuid.New()
	callerID := uuid.New() // different staff member

	created := seedSubject(t, svc, clinicID, creatorID, "Max")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	dto, err := svc.GetSubjectByID(context.Background(), id, clinicID, callerID, true)
	require.NoError(t, err)
	assert.Equal(t, "Max", dto.DisplayName)
}

func TestService_GetSubjectByID_ViewOwnPatients_OtherStaff_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	creatorID := uuid.New()
	otherStaffID := uuid.New()

	created := seedSubject(t, svc, clinicID, creatorID, "Rex")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	_, err = svc.GetSubjectByID(context.Background(), id, clinicID, otherStaffID, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestService_GetSubjectByID_ViewOwnPatients_Creator_Succeeds(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	creatorID := uuid.New()

	created := seedSubject(t, svc, clinicID, creatorID, "Bella")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	dto, err := svc.GetSubjectByID(context.Background(), id, clinicID, creatorID, false)
	require.NoError(t, err)
	assert.Equal(t, "Bella", dto.DisplayName)
}

// ── Subject: List ─────────────────────────────────────────────────────────────

func TestService_ListSubjects_ReturnsOnlyClinicSubjects(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicA := uuid.New()
	clinicB := uuid.New()
	callerID := uuid.New()

	seedSubject(t, svc, clinicA, callerID, "Dog A1")
	seedSubject(t, svc, clinicA, callerID, "Dog A2")
	seedSubject(t, svc, clinicB, callerID, "Dog B1")

	page, err := svc.ListSubjects(context.Background(), clinicA, ListSubjectsInput{Limit: 20, ViewAll: true})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	assert.Len(t, page.Items, 2)
	for _, item := range page.Items {
		assert.Equal(t, clinicA.String(), item.ClinicID)
	}
}

func TestService_ListSubjects_OwnerScope_OnlyCreatorSubjects(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	vet1 := uuid.New()
	vet2 := uuid.New()

	seedSubject(t, svc, clinicID, vet1, "Vet1 Dog")
	seedSubject(t, svc, clinicID, vet1, "Vet1 Cat")
	seedSubject(t, svc, clinicID, vet2, "Vet2 Dog")

	page, err := svc.ListSubjects(context.Background(), clinicID, ListSubjectsInput{
		Limit:      20,
		OwnerScope: true,
		CallerID:   vet1,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	for _, item := range page.Items {
		assert.Equal(t, vet1.String(), item.CreatedBy)
	}
}

func TestService_ListSubjects_DefaultsInvalidLimit(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	page, err := svc.ListSubjects(context.Background(), uuid.New(), ListSubjectsInput{Limit: -1, ViewAll: true})
	require.NoError(t, err)
	assert.Equal(t, 20, page.Limit)

	page2, err := svc.ListSubjects(context.Background(), uuid.New(), ListSubjectsInput{Limit: 9999, ViewAll: true})
	require.NoError(t, err)
	assert.Equal(t, 20, page2.Limit)
}

func TestService_ListSubjects_NoPermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.ListSubjects(context.Background(), uuid.New(), ListSubjectsInput{
		Limit:      20,
		ViewAll:    false,
		OwnerScope: false,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrForbidden)
}

// ── Subject: Update ───────────────────────────────────────────────────────────

func TestService_UpdateSubject_UpdatesDisplayName(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Old Name")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	newName := "New Name"
	dto, err := svc.UpdateSubject(context.Background(), id, clinicID, callerID, UpdateSubjectInput{DisplayName: &newName})
	require.NoError(t, err)
	assert.Equal(t, "New Name", dto.DisplayName)
}

func TestService_UpdateSubject_UpdatesStatus(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Buddy")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	status := domain.SubjectStatusDeceased
	dto, err := svc.UpdateSubject(context.Background(), id, clinicID, callerID, UpdateSubjectInput{Status: &status})
	require.NoError(t, err)
	assert.Equal(t, domain.SubjectStatusDeceased, dto.Status)
}

func TestService_UpdateSubject_UpdatesVetDetails(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Buddy")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	newBreed := "Golden Retriever"
	newWeight := 32.5
	dto, err := svc.UpdateSubject(context.Background(), id, clinicID, callerID, UpdateSubjectInput{
		VetDetails: &UpdateVetDetailsInput{
			Breed:    &newBreed,
			WeightKg: &newWeight,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, dto.VetDetails)
	assert.Equal(t, &newBreed, dto.VetDetails.Breed)
	assert.Equal(t, &newWeight, dto.VetDetails.WeightKg)
}

// ── Subject: Archive ──────────────────────────────────────────────────────────

func TestService_ArchiveSubject_SetsArchivedStatus(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Buddy")

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	dto, err := svc.ArchiveSubject(context.Background(), id, clinicID, callerID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubjectStatusArchived, dto.Status)
}

func TestService_ArchiveSubject_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.ArchiveSubject(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Subject: LinkContact ──────────────────────────────────────────────────────

func TestService_LinkContact_LinksContactToSubject(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	created := seedSubject(t, svc, clinicID, callerID, "Buddy")
	assert.Nil(t, created.Contact)

	contact := seedContact(t, svc, clinicID, "John Smith", "john@example.com")

	subjectID, err := uuid.Parse(created.ID)
	require.NoError(t, err)
	contactID, err := uuid.Parse(contact.ID)
	require.NoError(t, err)

	dto, err := svc.LinkContact(context.Background(), subjectID, clinicID, contactID, callerID)
	require.NoError(t, err)
	require.NotNil(t, dto.Contact)
	assert.Equal(t, "John Smith", dto.Contact.FullName)
}

func TestService_LinkContact_ContactWrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicA := uuid.New()
	clinicB := uuid.New()
	callerID := uuid.New()

	subject := seedSubject(t, svc, clinicA, callerID, "Buddy")
	contact := seedContact(t, svc, clinicB, "Other Clinic Owner", "other@example.com")

	subjectID, _ := uuid.Parse(subject.ID)
	contactID, _ := uuid.Parse(contact.ID)

	_, err := svc.LinkContact(context.Background(), subjectID, clinicA, contactID, callerID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Contact: GetWithSubjects ──────────────────────────────────────────────────

func TestService_GetContactWithSubjects_ReturnsBothEntities(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	contact := seedContact(t, svc, clinicID, "Multi-Pet Owner", "multi@example.com")
	contactID, _ := uuid.Parse(contact.ID)

	species := domain.VetSpeciesDog
	_, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalVeterinary,
		DisplayName: "Dog 1",
		ContactID:   &contactID,
		VetDetails:  &VetDetailsInput{Species: species},
	})
	require.NoError(t, err)

	species2 := domain.VetSpeciesCat
	_, err = svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalVeterinary,
		DisplayName: "Cat 1",
		ContactID:   &contactID,
		VetDetails:  &VetDetailsInput{Species: species2},
	})
	require.NoError(t, err)

	dto, err := svc.GetContactWithSubjects(context.Background(), contactID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, "Multi-Pet Owner", dto.FullName)
	assert.Len(t, dto.Subjects, 2)
}

// ── Access log ────────────────────────────────────────────────────────────────

func TestService_CreateSubject_WritesAccessLog(t *testing.T) {
	t.Parallel()
	svc, r := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	dto := seedSubject(t, svc, clinicID, callerID, "Buddy")

	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Len(t, r.accessLog, 1)
	entry := r.accessLog[0]
	assert.Equal(t, dto.ID, entry.SubjectID.String())
	assert.Equal(t, callerID, entry.StaffID)
	assert.Equal(t, clinicID, entry.ClinicID)
	assert.Equal(t, domain.SubjectAccessActionCreate, entry.Action)
}

func TestService_GetSubjectByID_WritesViewAccessLog(t *testing.T) {
	t.Parallel()
	svc, r := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Buddy")
	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	_, err = svc.GetSubjectByID(context.Background(), id, clinicID, callerID, true)
	require.NoError(t, err)

	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Len(t, r.accessLog, 2)
	assert.Equal(t, domain.SubjectAccessActionView, r.accessLog[1].Action)
}

func TestService_UnmaskPII_DecryptsAndLogsAccess(t *testing.T) {
	t.Parallel()
	svc, r := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	allergies := "peanuts"
	policy := "POL-42"
	species := domain.VetSpeciesDog
	created, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalVeterinary,
		DisplayName: "Buddy",
		VetDetails: &VetDetailsInput{
			Species:               species,
			Allergies:             &allergies,
			InsurancePolicyNumber: &policy,
		},
	})
	require.NoError(t, err)

	subjectID, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	purpose := "owner requested"
	got, err := svc.UnmaskPII(context.Background(), UnmaskPIIInput{
		SubjectID: subjectID,
		ClinicID:  clinicID,
		CallerID:  callerID,
		Field:     "insurance_policy_number",
		Purpose:   &purpose,
	})
	require.NoError(t, err)
	assert.Equal(t, "insurance_policy_number", got.Field)
	assert.Equal(t, policy, got.Value)

	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Len(t, r.accessLog, 2)
	last := r.accessLog[1]
	assert.Equal(t, domain.SubjectAccessActionUnmaskPII, last.Action)
	require.NotNil(t, last.Purpose)
	assert.Equal(t, "owner requested", *last.Purpose)
}

func TestService_UnmaskPII_UnknownField_ReturnsValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Buddy")
	subjectID, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	_, err = svc.UnmaskPII(context.Background(), UnmaskPIIInput{
		SubjectID: subjectID,
		ClinicID:  clinicID,
		CallerID:  callerID,
		Field:     "display_name",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestService_UnmaskPII_FieldNotSet_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedSubject(t, svc, clinicID, callerID, "Buddy")
	subjectID, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	_, err = svc.UnmaskPII(context.Background(), UnmaskPIIInput{
		SubjectID: subjectID,
		ClinicID:  clinicID,
		CallerID:  callerID,
		Field:     "allergies",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Vertical routing: Dental ──────────────────────────────────────────────────

func TestService_CreateSubject_Dental_EncryptsAndRoundTrips(t *testing.T) {
	t.Parallel()
	svc, r := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	sex := domain.DentalSexFemale
	alerts := "latex allergy"
	meds := "lisinopril 10mg"
	policy := "POL-777"
	dto, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalDental,
		DisplayName: "Jane Doe",
		DentalDetails: &DentalDetailsInput{
			Sex:                   &sex,
			MedicalAlerts:         &alerts,
			Medications:           &meds,
			InsurancePolicyNumber: &policy,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, dto.DentalDetails)
	assert.Equal(t, &alerts, dto.DentalDetails.MedicalAlerts)
	assert.Equal(t, &meds, dto.DentalDetails.Medications)
	assert.Equal(t, &policy, dto.DentalDetails.InsurancePolicyNumber)

	// Storage layer should hold ciphertext, not plaintext.
	subjectID, err := uuid.Parse(dto.ID)
	require.NoError(t, err)
	r.mu.RLock()
	stored := r.dentalDetails[subjectID]
	r.mu.RUnlock()
	require.NotNil(t, stored)
	require.NotNil(t, stored.MedicalAlerts)
	assert.NotEqual(t, alerts, *stored.MedicalAlerts)
	assert.NotEqual(t, policy, *stored.InsurancePolicyNumber)
}

func TestService_CreateSubject_Dental_MissingDetails_ReturnsValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    uuid.New(),
		CallerID:    uuid.New(),
		Vertical:    domain.VerticalDental,
		DisplayName: "Jane Doe",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrValidation)
}

// ── Vertical routing: General clinic ──────────────────────────────────────────

func TestService_CreateSubject_GeneralClinic_EncryptsAndRoundTrips(t *testing.T) {
	t.Parallel()
	svc, r := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()

	sex := domain.GeneralSexMale
	alerts := "penicillin allergy"
	conditions := "type 2 diabetes"
	dto, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    domain.VerticalGeneralClinic,
		DisplayName: "John Doe",
		GeneralDetails: &GeneralDetailsInput{
			Sex:               &sex,
			MedicalAlerts:     &alerts,
			ChronicConditions: &conditions,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, dto.GeneralDetails)
	assert.Equal(t, &alerts, dto.GeneralDetails.MedicalAlerts)
	assert.Equal(t, &conditions, dto.GeneralDetails.ChronicConditions)

	subjectID, err := uuid.Parse(dto.ID)
	require.NoError(t, err)
	r.mu.RLock()
	stored := r.generalDetails[subjectID]
	r.mu.RUnlock()
	require.NotNil(t, stored)
	require.NotNil(t, stored.MedicalAlerts)
	assert.NotEqual(t, alerts, *stored.MedicalAlerts)
}

func TestService_CreateSubject_GeneralClinic_MissingDetails_ReturnsValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.CreateSubject(context.Background(), CreateSubjectInput{
		ClinicID:    uuid.New(),
		CallerID:    uuid.New(),
		Vertical:    domain.VerticalGeneralClinic,
		DisplayName: "John Doe",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrValidation)
}
