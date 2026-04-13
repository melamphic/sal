//go:build integration

package clinic_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/clinic"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.IntegrationMain(m)
}

func newRepo(t *testing.T) *clinic.Repository {
	t.Helper()
	return clinic.NewRepository(testutil.NewTestDB(t))
}

func makeCreateParams(name, email, emailHash string) clinic.CreateParams {
	return clinic.CreateParams{
		ID:          domain.NewID(),
		Name:        name,
		Slug:        "test-slug-" + uuid.New().String()[:8],
		Email:       email,
		EmailHash:   emailHash,
		Vertical:    domain.VerticalVeterinary,
		Status:      domain.ClinicStatusTrial,
		TrialEndsAt: time.Now().UTC().Add(14 * 24 * time.Hour),
		DataRegion:  "ap-southeast-2",
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestRepository_Create_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()

	p := makeCreateParams("Riverside Vets", "enc-email", "abc123hash")
	created, err := r.Create(ctx, p)

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, p.ID, created.ID)
	assert.Equal(t, p.Name, created.Name)
	assert.Equal(t, p.Email, created.Email)
	assert.Equal(t, p.EmailHash, created.EmailHash)
	assert.Equal(t, domain.ClinicStatusTrial, created.Status)
	assert.False(t, created.CreatedAt.IsZero())
}

func TestRepository_Create_DuplicateEmailHash_Fails(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()

	p := makeCreateParams("Clinic A", "enc-email", "samehash")
	_, err := r.Create(ctx, p)
	require.NoError(t, err)

	p2 := makeCreateParams("Clinic B", "enc-email-2", "samehash")
	_, err = r.Create(ctx, p2)
	require.Error(t, err, "duplicate email_hash must be rejected by the DB")
	assert.ErrorIs(t, err, domain.ErrConflict)
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestRepository_GetByID_Found(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()

	p := makeCreateParams("Get Test", "enc-get", "gethash")
	created, err := r.Create(ctx, p)
	require.NoError(t, err)

	got, err := r.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Get Test", got.Name)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	_, err := r.GetByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── GetByEmailHash ────────────────────────────────────────────────────────────

func TestRepository_GetByEmailHash_Found(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()

	p := makeCreateParams("Email Lookup", "enc-email-lu", "uniquehashlu")
	_, err := r.Create(ctx, p)
	require.NoError(t, err)

	got, err := r.GetByEmailHash(ctx, "uniquehashlu")
	require.NoError(t, err)
	assert.Equal(t, "Email Lookup", got.Name)
}

func TestRepository_GetByEmailHash_NotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	_, err := r.GetByEmailHash(context.Background(), "nonexistenthash")
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestRepository_Update_Name(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()

	p := makeCreateParams("Old Name", "enc-upd", "updhash")
	created, err := r.Create(ctx, p)
	require.NoError(t, err)

	newName := "New Name"
	updated, err := r.Update(ctx, created.ID, clinic.UpdateParams{Name: &newName})
	require.NoError(t, err)
	assert.Equal(t, "New Name", updated.Name)
}

func TestRepository_Update_NotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	name := "Ghost"
	_, err := r.Update(context.Background(), uuid.New(), clinic.UpdateParams{Name: &name})
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
