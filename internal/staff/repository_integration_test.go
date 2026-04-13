//go:build integration

package staff_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/staff"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.IntegrationMain(m)
}

// seedClinic inserts a minimal clinic row for FK satisfaction.
func seedClinic(t *testing.T, clinicID uuid.UUID) {
	t.Helper()
	pool := testutil.NewTestDB(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'Test Clinic','tc-'||$1::text,'enc','ch-'||$1::text,'veterinary','trial',NOW()+interval'14d','ap-southeast-2')
		ON CONFLICT DO NOTHING
	`, clinicID)
	require.NoError(t, err)
}

func makeParams(clinicID uuid.UUID, emailHash string, role domain.StaffRole) staff.CreateParams {
	return staff.CreateParams{
		ID:        domain.NewID(),
		ClinicID:  clinicID,
		Email:     "enc-email",
		EmailHash: emailHash,
		FullName:  "enc-name",
		Role:      role,
		NoteTier:  domain.NoteTierStandard,
		Perms:     domain.DefaultPermissions(role),
		Status:    domain.StaffStatusActive,
	}
}

func newRepo(t *testing.T) *staff.Repository {
	t.Helper()
	return staff.NewRepository(testutil.NewTestDB(t))
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestRepository_Create_Roundtrip(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := makeParams(clinicID, "hash-create-"+uuid.New().String()[:8], domain.StaffRoleVet)
	created, err := r.Create(ctx, p)

	require.NoError(t, err)
	assert.Equal(t, p.ID, created.ID)
	assert.Equal(t, clinicID, created.ClinicID)
	assert.Equal(t, domain.StaffStatusActive, created.Status)
	assert.Equal(t, p.EmailHash, created.EmailHash)
	assert.False(t, created.CreatedAt.IsZero())
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestRepository_GetByID_Found(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := makeParams(clinicID, "hash-getid-"+uuid.New().String()[:8], domain.StaffRoleVet)
	created, err := r.Create(ctx, p)
	require.NoError(t, err)

	got, err := r.GetByID(ctx, created.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
}

func TestRepository_GetByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := makeParams(clinicID, "hash-wrong-"+uuid.New().String()[:8], domain.StaffRoleVet)
	created, err := r.Create(ctx, p)
	require.NoError(t, err)

	_, err = r.GetByID(ctx, created.ID, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	_, err := r.GetByID(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── ExistsByEmailHash ─────────────────────────────────────────────────────────

func TestRepository_ExistsByEmailHash_True(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	hash := "exists-hash-" + uuid.New().String()[:8]
	p := makeParams(clinicID, hash, domain.StaffRoleVet)
	_, err := r.Create(ctx, p)
	require.NoError(t, err)

	exists, err := r.ExistsByEmailHash(ctx, hash, clinicID)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestRepository_ExistsByEmailHash_False(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)

	exists, err := r.ExistsByEmailHash(context.Background(), "no-such-hash", uuid.New())
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRepository_ExistsByEmailHash_DifferentClinic_ReturnsFalse(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicA := domain.NewID()
	seedClinic(t, clinicA)

	hash := "cross-clinic-hash-" + uuid.New().String()[:8]
	_, err := r.Create(ctx, makeParams(clinicA, hash, domain.StaffRoleVet))
	require.NoError(t, err)

	// Same email hash, different clinic — must return false.
	exists, err := r.ExistsByEmailHash(ctx, hash, uuid.New())
	require.NoError(t, err)
	assert.False(t, exists, "email hash from another clinic must not be detected as existing")
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestRepository_List_ReturnsOnlyClinicStaff(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicA := domain.NewID()
	clinicB := domain.NewID()
	seedClinic(t, clinicA)
	seedClinic(t, clinicB)

	for i := range 3 {
		_, err := r.Create(ctx, makeParams(clinicA, "la-"+uuid.New().String()[:8]+string(rune('0'+i)), domain.StaffRoleVet))
		require.NoError(t, err)
	}
	_, err := r.Create(ctx, makeParams(clinicB, "lb-"+uuid.New().String()[:8], domain.StaffRoleVet))
	require.NoError(t, err)

	rows, total, err := r.List(ctx, clinicA, staff.ListParams{Limit: 20, Offset: 0})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, rows, 3)
	for _, s := range rows {
		assert.Equal(t, clinicA, s.ClinicID)
	}
}

func TestRepository_List_Pagination(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	for i := range 5 {
		_, err := r.Create(ctx, makeParams(clinicID, "pg-"+uuid.New().String()[:8]+string(rune('0'+i)), domain.StaffRoleVet))
		require.NoError(t, err)
	}

	page1, total, err := r.List(ctx, clinicID, staff.ListParams{Limit: 3, Offset: 0})
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.Len(t, page1, 3)

	page2, _, err := r.List(ctx, clinicID, staff.ListParams{Limit: 3, Offset: 3})
	require.NoError(t, err)
	assert.Len(t, page2, 2)
}

// ── UpdatePermissions ─────────────────────────────────────────────────────────

func TestRepository_UpdatePermissions(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := makeParams(clinicID, "upd-perms-"+uuid.New().String()[:8], domain.StaffRoleVet)
	created, err := r.Create(ctx, p)
	require.NoError(t, err)
	assert.False(t, created.Perms.ManageBilling)

	newPerms := domain.DefaultPermissions(domain.StaffRoleVet)
	newPerms.ManageBilling = true

	updated, err := r.UpdatePermissions(ctx, created.ID, clinicID, staff.UpdatePermsParams{Perms: newPerms})
	require.NoError(t, err)
	assert.True(t, updated.Perms.ManageBilling)
}

func TestRepository_UpdatePermissions_NotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	_, err := r.UpdatePermissions(context.Background(), uuid.New(), uuid.New(), staff.UpdatePermsParams{})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Deactivate ────────────────────────────────────────────────────────────────

func TestRepository_Deactivate(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := staff.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	seedClinic(t, clinicID)

	p := makeParams(clinicID, "deact-"+uuid.New().String()[:8], domain.StaffRoleVet)
	created, err := r.Create(ctx, p)
	require.NoError(t, err)

	updated, err := r.Deactivate(ctx, created.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, domain.StaffStatusDeactivated, updated.Status)

	// Deactivated staff must not appear in GetByID (archived_at IS NULL filter).
	// Status is 'deactivated' — they should still be findable but with deactivated status.
	got, err := r.GetByID(ctx, created.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, domain.StaffStatusDeactivated, got.Status)
}

func TestRepository_Deactivate_NotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	_, err := r.Deactivate(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
