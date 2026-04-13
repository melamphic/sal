//go:build integration

package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/auth"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.IntegrationMain(m)
}

// seedClinic inserts a minimal clinic row so staff foreign keys resolve.
func seedClinic(t *testing.T, db interface {
	Exec(ctx context.Context, sql string, args ...any) (interface{}, error)
}, clinicID uuid.UUID) {
	t.Helper()
	// Use the shared pool directly via a raw Exec — we're only seeding, not testing clinic logic.
	pool := testutil.NewTestDB(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1, 'Test Clinic', 'test-clinic', 'enc', 'hash-'||$1::text, 'veterinary', 'trial', NOW()+interval'14 days', 'ap-southeast-2')
		ON CONFLICT DO NOTHING
	`, clinicID)
	require.NoError(t, err)
}

// seedRow inserts a minimal staff row for auth tests.
func seedRow(t *testing.T, clinicID uuid.UUID, emailHash string) uuid.UUID {
	t.Helper()
	pool := testutil.NewTestDB(t)
	staffID := domain.NewID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, status
		) VALUES ($1,$2,'enc','`+emailHash+`','enc','super_admin','standard',
			true,true,true,true,true,true,true,true,false,false,true,'active')
	`, staffID, clinicID)
	require.NoError(t, err)
	return staffID
}

func newRepo(t *testing.T) *auth.Repository {
	t.Helper()
	return auth.NewRepository(testutil.NewTestDB(t))
}

// ── FindStaffByEmailHash ───────────────────────────────────────────────────────

func TestRepository_FindStaffByEmailHash_Found(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	_ = pool
	r := auth.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	emailHash := "hash-find-" + uuid.New().String()[:8]

	// Need clinic first for FK.
	_, err := pool.Exec(ctx, `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'TC','tc-slug','enc',$2,'veterinary','trial',NOW()+interval'14d','ap-southeast-2')
	`, clinicID, "clinic-"+emailHash)
	require.NoError(t, err)

	staffID := domain.NewID()
	_, err = pool.Exec(ctx, `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, status
		) VALUES ($1,$2,'enc',$3,'enc','super_admin','standard',
			true,true,true,true,true,true,true,true,false,false,true,'active')
	`, staffID, clinicID, emailHash)
	require.NoError(t, err)

	s, err := r.FindStaffByEmailHash(ctx, emailHash)
	require.NoError(t, err)
	assert.Equal(t, staffID, s.ID)
	assert.Equal(t, clinicID, s.ClinicID)
}

func TestRepository_FindStaffByEmailHash_NotFound(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := auth.NewRepository(pool)

	_, err := r.FindStaffByEmailHash(context.Background(), "nonexistent-hash")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── CreateAuthToken / GetAndConsumeAuthToken ───────────────────────────────────

func TestRepository_CreateAndConsumeAuthToken_Roundtrip(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := auth.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	_, err := pool.Exec(ctx, `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'TC2','tc2-slug','enc','clinic2hash','veterinary','trial',NOW()+interval'14d','ap-southeast-2')
	`, clinicID)
	require.NoError(t, err)

	staffID := domain.NewID()
	_, err = pool.Exec(ctx, `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, status
		) VALUES ($1,$2,'enc','staffhash2','enc','super_admin','standard',
			true,true,true,true,true,true,true,true,false,false,true,'active')
	`, staffID, clinicID)
	require.NoError(t, err)

	tokenHash := "test-token-hash-" + uuid.New().String()
	expiresAt := time.Now().UTC().Add(15 * time.Minute)

	err = r.CreateAuthToken(ctx, staffID, tokenHash, "magic_link", "127.0.0.1", expiresAt)
	require.NoError(t, err)

	row, err := r.GetAndConsumeAuthToken(ctx, tokenHash)
	require.NoError(t, err)
	assert.Equal(t, staffID, row.StaffID)
	assert.Equal(t, "magic_link", row.TokenType)
}

func TestRepository_GetAndConsumeAuthToken_Replay_Blocked(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := auth.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	_, _ = pool.Exec(ctx, `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'TC3','tc3-slug','enc','clinic3hash','veterinary','trial',NOW()+interval'14d','ap-southeast-2')
	`, clinicID)
	staffID := domain.NewID()
	_, _ = pool.Exec(ctx, `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, status
		) VALUES ($1,$2,'enc','staffhash3','enc','super_admin','standard',
			true,true,true,true,true,true,true,true,false,false,true,'active')
	`, staffID, clinicID)

	tokenHash := "replay-token-" + uuid.New().String()
	require.NoError(t, r.CreateAuthToken(ctx, staffID, tokenHash, "magic_link", "", time.Now().Add(15*time.Minute)))

	_, err := r.GetAndConsumeAuthToken(ctx, tokenHash)
	require.NoError(t, err)

	_, err = r.GetAndConsumeAuthToken(ctx, tokenHash)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenUsed)
}

func TestRepository_GetAndConsumeAuthToken_Expired(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := auth.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	_, _ = pool.Exec(ctx, `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'TC4','tc4-slug','enc','clinic4hash','veterinary','trial',NOW()+interval'14d','ap-southeast-2')
	`, clinicID)
	staffID := domain.NewID()
	_, _ = pool.Exec(ctx, `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, status
		) VALUES ($1,$2,'enc','staffhash4','enc','super_admin','standard',
			true,true,true,true,true,true,true,true,false,false,true,'active')
	`, staffID, clinicID)

	tokenHash := "expired-token-" + uuid.New().String()
	require.NoError(t, r.CreateAuthToken(ctx, staffID, tokenHash, "magic_link", "", time.Now().Add(-1*time.Hour)))

	_, err := r.GetAndConsumeAuthToken(ctx, tokenHash)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
}

func TestRepository_GetAndConsumeAuthToken_NotFound(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := auth.NewRepository(pool)

	_, err := r.GetAndConsumeAuthToken(context.Background(), "no-such-hash")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── DeleteRefreshTokensForStaff ───────────────────────────────────────────────

func TestRepository_DeleteRefreshTokensForStaff(t *testing.T) {
	t.Parallel()
	pool := testutil.NewTestDB(t)
	r := auth.NewRepository(pool)
	ctx := context.Background()

	clinicID := domain.NewID()
	_, _ = pool.Exec(ctx, `
		INSERT INTO clinics (id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region)
		VALUES ($1,'TC5','tc5-slug','enc','clinic5hash','veterinary','trial',NOW()+interval'14d','ap-southeast-2')
	`, clinicID)
	staffID := domain.NewID()
	_, _ = pool.Exec(ctx, `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, status
		) VALUES ($1,$2,'enc','staffhash5','enc','super_admin','standard',
			true,true,true,true,true,true,true,true,false,false,true,'active')
	`, staffID, clinicID)

	// Insert two refresh tokens.
	for i := range 2 {
		hash := uuid.New().String() + "-refresh-" + string(rune('0'+i))
		require.NoError(t, r.CreateAuthToken(ctx, staffID, hash, "refresh", "", time.Now().Add(time.Hour)))
	}

	require.NoError(t, r.DeleteRefreshTokensForStaff(ctx, staffID))

	// Both should be gone.
	var count int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM auth_tokens WHERE staff_id = $1 AND token_type = 'refresh'`, staffID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
