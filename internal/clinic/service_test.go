package clinic

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustParseUUID parses a UUID string and fails the test if it is invalid.
func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return id
}

func newTestClinicService(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	r := newFakeClinicRepo()
	c := testutil.TestCipher(t)
	return NewService(r, c, nil, nil, nil), r
}

// ── generateSlug ──────────────────────────────────────────────────────────────

func TestGenerateSlug_BasicName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "riverside-veterinary", generateSlug("Riverside Veterinary"))
}

func TestGenerateSlug_SpecialCharacters(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "wanaka-vets-co-nz", generateSlug("Wānaka Vets & Co. (NZ)"))
}

func TestGenerateSlug_LeadingTrailingSpaces(t *testing.T) {
	t.Parallel()
	slug := generateSlug("  Auckland Animal Hospital  ")
	assert.False(t, len(slug) > 0 && slug[0] == '-', "slug must not start with hyphen")
	assert.False(t, slug[len(slug)-1] == '-', "slug must not end with hyphen")
}

func TestGenerateSlug_TruncatesLongNames(t *testing.T) {
	t.Parallel()
	name := "A Very Long Veterinary Clinic Name That Exceeds The Maximum Length Allowed By The Slug Generator Function"
	slug := generateSlug(name)
	assert.LessOrEqual(t, len(slug), 60)
}

func TestGenerateSlug_NumericOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "24-7-vets", generateSlug("24/7 Vets"))
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestService_Register_Success(t *testing.T) {
	t.Parallel()
	testutil.FreezeTime(t)
	svc, _ := newTestClinicService(t)

	phone := "+64 9 123 4567"
	dto, err := svc.Register(context.Background(), RegisterInput{
		Name:       "Riverside Vet",
		Email:      "info@riverside.co.nz",
		Phone:      &phone,
		Vertical:   domain.VerticalVeterinary,
		DataRegion: "ap-southeast-2",
	})

	require.NoError(t, err)
	require.NotNil(t, dto)
	assert.Equal(t, "Riverside Vet", dto.Name)
	assert.Equal(t, "info@riverside.co.nz", dto.Email, "service should return decrypted email")
	assert.Equal(t, phone, *dto.Phone, "service should return decrypted phone")
	assert.Equal(t, domain.VerticalVeterinary, dto.Vertical)
	assert.Equal(t, domain.ClinicStatusTrial, dto.Status)
	assert.Equal(t, "ap-southeast-2", dto.DataRegion)
	assert.NotEmpty(t, dto.Slug)
	assert.NotEmpty(t, dto.ID)

	// Trial ends 14 days from now.
	expectedEnd := testutil.FixedTime.Add(14 * 24 * time.Hour)
	assert.Equal(t, expectedEnd, dto.TrialEndsAt)
}

func TestService_Register_DefaultsVerticalToVeterinary(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)

	dto, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Auckland Vets",
		Email: "info@akl-vets.co.nz",
		// Vertical intentionally omitted.
	})

	require.NoError(t, err)
	assert.Equal(t, domain.VerticalVeterinary, dto.Vertical)
}

func TestService_Register_DefaultsDataRegion(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)

	dto, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Dunedin Vets",
		Email: "info@dun-vets.co.nz",
		// DataRegion intentionally omitted.
	})

	require.NoError(t, err)
	assert.Equal(t, "ap-southeast-2", dto.DataRegion)
}

func TestService_Register_DuplicateEmail_ReturnsConflict(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)
	input := RegisterInput{Name: "Clinic A", Email: "same@email.co.nz"}

	_, err := svc.Register(context.Background(), input)
	require.NoError(t, err)

	_, err = svc.Register(context.Background(), input)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConflict, "duplicate email must return ErrConflict")
}

func TestService_Register_EmailStoredEncrypted(t *testing.T) {
	t.Parallel()
	svc, r := newTestClinicService(t)
	email := "secret@clinic.co.nz"

	_, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Secret Clinic",
		Email: email,
	})
	require.NoError(t, err)

	// Find the stored row and confirm the raw email is NOT in the DB field.
	var found *Clinic
	for _, c := range r.byID {
		found = c
		break
	}
	require.NotNil(t, found)
	assert.NotEqual(t, email, found.Email, "raw email must not be stored in the database field")
	assert.NotEmpty(t, found.EmailHash, "email hash must be stored for lookup")
}

func TestService_Register_OptionalFieldsNilByDefault(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)

	dto, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Minimal Clinic",
		Email: "min@clinic.co.nz",
	})

	require.NoError(t, err)
	assert.Nil(t, dto.Phone)
	assert.Nil(t, dto.Address)
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestService_GetByID_ReturnsDecryptedData(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)
	email := "gettest@clinic.co.nz"
	created, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Get Test Clinic",
		Email: email,
	})
	require.NoError(t, err)

	id := mustParseUUID(t, created.ID)

	dto, err := svc.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, email, dto.Email, "GetByID should return decrypted email")
	assert.Equal(t, created.Name, dto.Name)
}

func TestService_GetByID_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)
	id := testutil.NewID()

	_, err := svc.GetByID(context.Background(), id)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestService_Update_Name(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)
	created, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Old Name",
		Email: "update@clinic.co.nz",
	})
	require.NoError(t, err)

	id := mustParseUUID(t, created.ID)
	newName := "New Name"
	dto, err := svc.Update(context.Background(), id, UpdateInput{Name: &newName})
	require.NoError(t, err)
	assert.Equal(t, "New Name", dto.Name)
}

func TestService_Update_PhoneStoredEncrypted(t *testing.T) {
	t.Parallel()
	svc, r := newTestClinicService(t)
	created, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Phone Test",
		Email: "phone@clinic.co.nz",
	})
	require.NoError(t, err)

	id := mustParseUUID(t, created.ID)
	newPhone := "+64 9 999 0000"
	dto, err := svc.Update(context.Background(), id, UpdateInput{Phone: &newPhone})
	require.NoError(t, err)
	assert.Equal(t, newPhone, *dto.Phone)

	// Raw phone must not appear in the stored row.
	stored := r.byID[id]
	require.NotNil(t, stored)
	assert.NotEqual(t, newPhone, *stored.Phone, "phone must be encrypted in the DB field")
}

func TestService_Update_NotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)
	name := "Ghost"
	_, err := svc.Update(context.Background(), testutil.NewID(), UpdateInput{Name: &name})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestService_Update_NilFieldsLeaveDataUnchanged(t *testing.T) {
	t.Parallel()
	svc, _ := newTestClinicService(t)
	phone := "+64 9 123 0001"
	created, err := svc.Register(context.Background(), RegisterInput{
		Name:  "Stable Clinic",
		Email: "stable@clinic.co.nz",
		Phone: &phone,
	})
	require.NoError(t, err)

	id := mustParseUUID(t, created.ID)
	// Update with all nil — nothing should change.
	dto, err := svc.Update(context.Background(), id, UpdateInput{})
	require.NoError(t, err)
	assert.Equal(t, phone, *dto.Phone)
	assert.Equal(t, "Stable Clinic", dto.Name)
}
