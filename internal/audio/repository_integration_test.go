//go:build integration

package audio_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/audio"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.IntegrationMain(m)
}

func newRepo(t *testing.T) *audio.Repository {
	t.Helper()
	return audio.NewRepository(testutil.NewTestDB(t))
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

// seedStaff inserts a minimal staff row to satisfy FK constraints.
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

// ── CreateRecording ───────────────────────────────────────────────────────────

func TestRepository_CreateRecording_Roundtrip(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	rec, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID:          domain.NewID(),
		ClinicID:    clinicID,
		StaffID:     staffID,
		FileKey:     "clinics/" + clinicID.String() + "/recordings/test.m4a",
		ContentType: "audio/mp4",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.RecordingStatusPendingUpload, rec.Status)
	assert.Nil(t, rec.SubjectID)
	assert.False(t, rec.CreatedAt.IsZero())
}

// ── GetRecordingByID ──────────────────────────────────────────────────────────

func TestRepository_GetRecordingByID_Found(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	created, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicID, StaffID: staffID,
		FileKey: "test.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	got, err := r.GetRecordingByID(ctx, created.ID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
}

func TestRepository_GetRecordingByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	created, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicID, StaffID: staffID,
		FileKey: "test.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	_, err = r.GetRecordingByID(ctx, created.ID, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── UpdateRecordingStatus ─────────────────────────────────────────────────────

func TestRepository_UpdateRecordingStatus_TransitionsStatus(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	created, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicID, StaffID: staffID,
		FileKey: "test.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	updated, err := r.UpdateRecordingStatus(ctx, created.ID, domain.RecordingStatusTranscribing, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.RecordingStatusTranscribing, updated.Status)
	assert.Nil(t, updated.ErrorMessage)
}

func TestRepository_UpdateRecordingStatus_SetsErrorMessage(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	created, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicID, StaffID: staffID,
		FileKey: "test.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	errMsg := "deepgram timeout"
	updated, err := r.UpdateRecordingStatus(ctx, created.ID, domain.RecordingStatusFailed, &errMsg)
	require.NoError(t, err)
	assert.Equal(t, domain.RecordingStatusFailed, updated.Status)
	require.NotNil(t, updated.ErrorMessage)
	assert.Equal(t, errMsg, *updated.ErrorMessage)
}

// ── UpdateRecordingTranscript ─────────────────────────────────────────────────

func TestRepository_UpdateRecordingTranscript_StoresTranscript(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	created, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicID, StaffID: staffID,
		FileKey: "test.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	dur := 142
	updated, err := r.UpdateRecordingTranscript(ctx, created.ID, "The patient presented with lameness.", &dur)
	require.NoError(t, err)
	assert.Equal(t, domain.RecordingStatusTranscribed, updated.Status)
	require.NotNil(t, updated.Transcript)
	assert.Equal(t, "The patient presented with lameness.", *updated.Transcript)
	require.NotNil(t, updated.DurationSeconds)
	assert.Equal(t, 142, *updated.DurationSeconds)
}

// ── ListRecordings ────────────────────────────────────────────────────────────

func TestRepository_ListRecordings_ReturnsOnlyClinicRecordings(t *testing.T) {
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
		_, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
			ID: domain.NewID(), ClinicID: clinicA, StaffID: staffA,
			FileKey: "a.m4a", ContentType: "audio/mp4",
		})
		require.NoError(t, err)
	}
	_, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicB, StaffID: staffB,
		FileKey: "b.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	recs, total, err := r.ListRecordings(ctx, clinicA, audio.ListRecordingsParams{Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, recs, 3)
}

// ── LinkSubject ───────────────────────────────────────────────────────────────

func TestRepository_LinkSubject_UpdatesSubjectID(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	ctx := context.Background()
	clinicID := domain.NewID()
	staffID := domain.NewID()
	seedClinic(t, clinicID)
	seedStaff(t, clinicID, staffID)

	// Also seed a subject so FK is satisfied.
	subjectID := domain.NewID()
	pool := testutil.NewTestDB(t)
	_, err := pool.Exec(ctx, `
		INSERT INTO subjects (id, clinic_id, display_name, status, vertical, created_by)
		VALUES ($1, $2, 'Buddy', 'active', 'veterinary', $3)
	`, subjectID, clinicID, staffID)
	require.NoError(t, err)

	created, err := r.CreateRecording(ctx, audio.CreateRecordingParams{
		ID: domain.NewID(), ClinicID: clinicID, StaffID: staffID,
		FileKey: "test.m4a", ContentType: "audio/mp4",
	})
	require.NoError(t, err)
	assert.Nil(t, created.SubjectID)

	updated, err := r.LinkSubject(ctx, created.ID, clinicID, subjectID)
	require.NoError(t, err)
	require.NotNil(t, updated.SubjectID)
	assert.Equal(t, subjectID, *updated.SubjectID)
}
