package staff

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInviteCreator satisfies InviteCreator in tests.
type fakeInviteCreator struct{}

func (f *fakeInviteCreator) CreateInvite(_ context.Context, _ CreateInviteTokenParams) (string, error) {
	return "fake-invite-token", nil
}

// fakeClinicNameProvider satisfies ClinicNameProvider in tests.
type fakeClinicNameProvider struct{}

func (f *fakeClinicNameProvider) GetClinicName(_ context.Context, _ uuid.UUID) (string, error) {
	return "Riverside Vets", nil
}

func newTestService(t *testing.T) (*Service, *fakeRepo, *testutil.FakeMailer) {
	t.Helper()
	r := newFakeRepo()
	m := &testutil.FakeMailer{}
	c := testutil.TestCipher(t)
	svc := NewService(r, c, m, "https://app.salvia.test", &fakeInviteCreator{}, &fakeClinicNameProvider{})
	return svc, r, m
}

// seedStaff creates a staff member via the service and returns the DTO.
func seedStaff(t *testing.T, svc *Service, clinicID uuid.UUID, email, name string, role domain.StaffRole) *StaffResponse {
	t.Helper()
	dto, err := svc.Create(context.Background(), CreateStaffInput{
		ClinicID:    clinicID,
		Email:       email,
		FullName:    name,
		Role:        role,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(role),
	})
	require.NoError(t, err)
	return dto
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestService_Create_ReturnsDecryptedDTO(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	dto, err := svc.Create(context.Background(), CreateStaffInput{
		ClinicID:    clinicID,
		Email:       "dr.sarah@clinic.co.nz",
		FullName:    "Dr. Sarah Chen",
		Role:        domain.StaffRoleVet,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(domain.StaffRoleVet),
	})

	require.NoError(t, err)
	require.NotNil(t, dto)
	assert.Equal(t, "dr.sarah@clinic.co.nz", dto.Email)
	assert.Equal(t, "Dr. Sarah Chen", dto.FullName)
	assert.Equal(t, clinicID.String(), dto.ClinicID)
	assert.Equal(t, domain.StaffRoleVet, dto.Role)
	assert.Equal(t, domain.StaffStatusActive, dto.Status)
	assert.NotEmpty(t, dto.ID)
}

func TestService_Create_EmailStoredEncrypted(t *testing.T) {
	t.Parallel()
	svc, r, _ := newTestService(t)
	clinicID := uuid.New()
	email := "secret@clinic.co.nz"

	dto, err := svc.Create(context.Background(), CreateStaffInput{
		ClinicID:    clinicID,
		Email:       email,
		FullName:    "Secret Staff",
		Role:        domain.StaffRoleVet,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(domain.StaffRoleVet),
	})
	require.NoError(t, err)

	id, err := uuid.Parse(dto.ID)
	require.NoError(t, err)
	stored := r.byID[id]
	require.NotNil(t, stored)
	assert.NotEqual(t, email, stored.Email, "raw email must not be stored")
	assert.NotEmpty(t, stored.EmailHash, "email hash must be stored for lookup")
	assert.NotEqual(t, email, stored.FullName, "full name must be encrypted")
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestService_GetByID_ReturnsDecryptedData(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedStaff(t, svc, clinicID, "get@clinic.co.nz", "Get Tester", domain.StaffRoleVet)

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	dto, err := svc.GetByID(context.Background(), id, clinicID)
	require.NoError(t, err)
	assert.Equal(t, "get@clinic.co.nz", dto.Email)
	assert.Equal(t, "Get Tester", dto.FullName)
}

func TestService_GetByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	otherClinic := uuid.New()
	created := seedStaff(t, svc, clinicID, "crossclinic@test.co.nz", "Cross Clinic", domain.StaffRoleVet)

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	_, err = svc.GetByID(context.Background(), id, otherClinic)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound, "staff from another clinic must not be visible")
}

func TestService_GetByID_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	_, err := svc.GetByID(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Invite ────────────────────────────────────────────────────────────────────

func TestService_Invite_SendsEmail(t *testing.T) {
	t.Parallel()
	svc, _, m := newTestService(t)
	clinicID := uuid.New()

	url, err := svc.Invite(context.Background(), clinicID, uuid.New(), InviteInput{
		Email:       "newstaff@clinic.co.nz",
		FullName:    "New Staff",
		Role:        domain.StaffRoleVet,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(domain.StaffRoleVet),
		InviterName: "Dr. Admin",
		SendEmail:   true,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, url, "Invite must always return the invite URL")
	assert.Contains(t, url, "/invite/accept?token=")
	assert.Equal(t, 1, m.Count("invite"), "should send exactly one invite email")
	last := m.Last()
	require.NotNil(t, last)
	assert.Equal(t, "newstaff@clinic.co.nz", last.To)
}

func TestService_Invite_SendEmailFalse_SkipsMailer(t *testing.T) {
	t.Parallel()
	svc, _, m := newTestService(t)
	clinicID := uuid.New()

	url, err := svc.Invite(context.Background(), clinicID, uuid.New(), InviteInput{
		Email:       "linkonly@clinic.co.nz",
		FullName:    "Link-only Staff",
		Role:        domain.StaffRoleVet,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(domain.StaffRoleVet),
		InviterName: "Dr. Admin",
		SendEmail:   false,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, url)
	assert.Equal(t, 0, m.Count("invite"), "must not send email when SendEmail=false")
}

func TestService_Invite_DuplicateEmail_ReturnsConflict(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	email := "dup@clinic.co.nz"

	// Create the staff member first.
	seedStaff(t, svc, clinicID, email, "Existing Staff", domain.StaffRoleVet)

	// Now try to invite the same email to the same clinic.
	_, err := svc.Invite(context.Background(), clinicID, uuid.New(), InviteInput{
		Email:       email,
		FullName:    "Duplicate Staff",
		Role:        domain.StaffRoleVet,
		InviterName: "Dr. Admin",
		SendEmail:   true,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestService_Invite_SameEmailDifferentClinic_Allowed(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicA := uuid.New()
	clinicB := uuid.New()
	email := "shared@clinic.co.nz"

	seedStaff(t, svc, clinicA, email, "Clinic A Staff", domain.StaffRoleVet)

	_, err := svc.Invite(context.Background(), clinicB, uuid.New(), InviteInput{
		Email:       email,
		FullName:    "Clinic B Staff",
		Role:        domain.StaffRoleVet,
		InviterName: "Dr. Admin",
		SendEmail:   true,
	})

	require.NoError(t, err, "same email must be allowed in a different clinic")
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestService_List_ReturnsOnlyClinicStaff(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicA := uuid.New()
	clinicB := uuid.New()

	seedStaff(t, svc, clinicA, "a1@clinic.co.nz", "Staff A1", domain.StaffRoleVet)
	seedStaff(t, svc, clinicA, "a2@clinic.co.nz", "Staff A2", domain.StaffRoleVet)
	seedStaff(t, svc, clinicB, "b1@clinic.co.nz", "Staff B1", domain.StaffRoleVet)

	page, err := svc.List(context.Background(), clinicA, 20, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	assert.Len(t, page.Items, 2)
	for _, item := range page.Items {
		assert.Equal(t, clinicA.String(), item.ClinicID, "must only return staff from requested clinic")
	}
}

func TestService_List_DefaultsInvalidLimit(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	page, err := svc.List(context.Background(), clinicID, -1, 0)
	require.NoError(t, err)
	assert.Equal(t, 20, page.Limit, "negative limit should be defaulted to 20")

	page2, err := svc.List(context.Background(), clinicID, 999, 0)
	require.NoError(t, err)
	assert.Equal(t, 20, page2.Limit, "oversized limit should be capped at 20")
}

func TestService_List_ReturnsPaginatedResults(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	for i := range 5 {
		seedStaff(t, svc, clinicID,
			"staff"+string(rune('a'+i))+"@clinic.co.nz",
			"Staff "+string(rune('A'+i)),
			domain.StaffRoleVet,
		)
	}

	page, err := svc.List(context.Background(), clinicID, 3, 0)
	require.NoError(t, err)
	assert.Equal(t, 5, page.Total)
	assert.Len(t, page.Items, 3)

	page2, err := svc.List(context.Background(), clinicID, 3, 3)
	require.NoError(t, err)
	assert.Equal(t, 5, page2.Total)
	assert.Len(t, page2.Items, 2)
}

// ── UpdatePermissions ─────────────────────────────────────────────────────────

func TestService_UpdatePermissions_SuperAdminCanGrantBilling(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedStaff(t, svc, clinicID, "vet@clinic.co.nz", "Dr. Vet", domain.StaffRoleVet)

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	newPerms := domain.DefaultPermissions(domain.StaffRoleVet)
	newPerms.ManageBilling = true

	dto, err := svc.UpdatePermissions(context.Background(), id, clinicID, domain.StaffRoleSuperAdmin, newPerms)
	require.NoError(t, err)
	assert.True(t, dto.Permissions.ManageBilling, "super_admin should be able to grant manage_billing")
}

func TestService_UpdatePermissions_NonSuperAdminCannotGrantBilling(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedStaff(t, svc, clinicID, "vet@clinic.co.nz", "Dr. Vet", domain.StaffRoleVet)

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	newPerms := domain.DefaultPermissions(domain.StaffRoleVet)
	newPerms.ManageBilling = true    // attempting to grant billing
	newPerms.RollbackPolicies = true // attempting to grant rollback

	dto, err := svc.UpdatePermissions(context.Background(), id, clinicID, domain.StaffRoleVet, newPerms)
	require.NoError(t, err)
	assert.False(t, dto.Permissions.ManageBilling, "non-super_admin must not be able to grant manage_billing")
	assert.False(t, dto.Permissions.RollbackPolicies, "non-super_admin must not be able to grant rollback_policies")
}

func TestService_UpdatePermissions_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	_, err := svc.UpdatePermissions(context.Background(), uuid.New(), uuid.New(), domain.StaffRoleSuperAdmin, domain.Permissions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Deactivate ────────────────────────────────────────────────────────────────

func TestService_Deactivate_MarksStaffDeactivated(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	callerID := uuid.New()
	created := seedStaff(t, svc, clinicID, "target@clinic.co.nz", "Target Staff", domain.StaffRoleReceptionist)

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	dto, err := svc.Deactivate(context.Background(), id, clinicID, callerID)
	require.NoError(t, err)
	assert.Equal(t, domain.StaffStatusDeactivated, dto.Status)
}

func TestService_Deactivate_CannotDeactivateSelf(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	created := seedStaff(t, svc, clinicID, "self@clinic.co.nz", "Self Staff", domain.StaffRoleVet)

	id, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	// Pass the same ID as both staffID and callerID.
	_, err = svc.Deactivate(context.Background(), id, clinicID, id)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrForbidden, "staff must not be able to deactivate themselves")
}

func TestService_Deactivate_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	_, err := svc.Deactivate(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── EnsureOwner ───────────────────────────────────────────────────────────────

func TestService_EnsureOwner_CreatesNewSuperAdmin(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	dto, err := svc.EnsureOwner(context.Background(), clinicID, "founder@clinic.test", "Jane Founder")
	require.NoError(t, err)

	assert.Equal(t, "founder@clinic.test", dto.Email)
	assert.Equal(t, "Jane Founder", dto.FullName)
	assert.Equal(t, domain.StaffRoleSuperAdmin, dto.Role)
	assert.Equal(t, domain.NoteTierStandard, dto.NoteTier)
	assert.Equal(t, domain.StaffStatusActive, dto.Status)
}

func TestService_EnsureOwner_Idempotent_ReturnsExisting(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	first, err := svc.EnsureOwner(context.Background(), clinicID, "founder@clinic.test", "Jane Founder")
	require.NoError(t, err)

	// Replay — must return the same staff row.
	second, err := svc.EnsureOwner(context.Background(), clinicID, "founder@clinic.test", "Different Name")
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "same email must return same staff")
	assert.Equal(t, "Jane Founder", second.FullName, "existing name must not be overwritten")
}
