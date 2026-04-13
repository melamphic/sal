package audio

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// fakeEnqueuer records Insert calls without a live River client or DB.
type fakeEnqueuer struct {
	inserted []river.JobArgs
}

func (f *fakeEnqueuer) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.inserted = append(f.inserted, args)
	return &rivertype.JobInsertResult{}, nil
}

// fakeStore is a no-op storage backend for unit tests.
// It returns a predictable URL without making any network calls.
type fakeStore struct{}

func (f *fakeStore) PresignUpload(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://storage.example.com/" + key + "?upload", nil
}
func (f *fakeStore) PresignDownload(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://storage.example.com/" + key + "?download", nil
}

func newTestService(t *testing.T) (*Service, *fakeRepo, *fakeEnqueuer) {
	t.Helper()
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := &Service{repo: repo, store: &fakeStore{}, enqueue: enq}
	return svc, repo, enq
}

// seedRecording creates a recording via the service and returns it.
func seedRecording(t *testing.T, svc *Service, clinicID, staffID uuid.UUID) *CreateRecordingResponse {
	t.Helper()
	resp, err := svc.CreateRecording(context.Background(), CreateRecordingInput{
		ClinicID:    clinicID,
		StaffID:     staffID,
		ContentType: "audio/mp4",
	})
	require.NoError(t, err)
	return resp
}

// ── CreateRecording ───────────────────────────────────────────────────────────

func TestService_CreateRecording_ReturnsUploadURL(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	resp, err := svc.CreateRecording(context.Background(), CreateRecordingInput{
		ClinicID:    uuid.New(),
		StaffID:     uuid.New(),
		ContentType: "audio/mp4",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.UploadURL)
	assert.Equal(t, string(domain.RecordingStatusPendingUpload), string(resp.Recording.Status))
}

func TestService_CreateRecording_NullableSubject(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	resp, err := svc.CreateRecording(context.Background(), CreateRecordingInput{
		ClinicID:    uuid.New(),
		StaffID:     uuid.New(),
		ContentType: "audio/mp4",
	})
	require.NoError(t, err)
	assert.Nil(t, resp.Recording.SubjectID)
}

func TestService_CreateRecording_WithSubject(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	subjectID := uuid.New()

	resp, err := svc.CreateRecording(context.Background(), CreateRecordingInput{
		ClinicID:    uuid.New(),
		StaffID:     uuid.New(),
		SubjectID:   &subjectID,
		ContentType: "audio/mp4",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Recording.SubjectID)
	assert.Equal(t, subjectID.String(), *resp.Recording.SubjectID)
}

func TestService_CreateRecording_FileKeyContainsClinicAndRecordingIDs(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newTestService(t)
	clinicID := uuid.New()

	resp, err := svc.CreateRecording(context.Background(), CreateRecordingInput{
		ClinicID:    clinicID,
		StaffID:     uuid.New(),
		ContentType: "audio/mp4",
	})
	require.NoError(t, err)

	recID, _ := uuid.Parse(resp.Recording.ID)
	rec, _ := repo.GetRecordingByID(context.Background(), recID, clinicID)
	assert.Contains(t, rec.FileKey, clinicID.String())
	assert.Contains(t, rec.FileKey, resp.Recording.ID)
}

// ── GetRecordingByID ──────────────────────────────────────────────────────────

func TestService_GetRecordingByID_Found(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	resp, err := svc.GetRecordingByID(context.Background(), recID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, created.Recording.ID, resp.ID)
}

func TestService_GetRecordingByID_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	_, err := svc.GetRecordingByID(context.Background(), recID, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── ListRecordings ────────────────────────────────────────────────────────────

func TestService_ListRecordings_ReturnsOnlyClinicRecordings(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicA := uuid.New()
	clinicB := uuid.New()
	staffID := uuid.New()

	seedRecording(t, svc, clinicA, staffID)
	seedRecording(t, svc, clinicA, staffID)
	seedRecording(t, svc, clinicB, staffID)

	page, err := svc.ListRecordings(context.Background(), clinicA, ListRecordingsInput{Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	assert.Len(t, page.Items, 2)
}

func TestService_ListRecordings_FilterByStaff(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	vet1 := uuid.New()
	vet2 := uuid.New()

	seedRecording(t, svc, clinicID, vet1)
	seedRecording(t, svc, clinicID, vet2)

	page, err := svc.ListRecordings(context.Background(), clinicID, ListRecordingsInput{
		Limit:   20,
		StaffID: &vet1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)
	assert.Equal(t, vet1.String(), page.Items[0].StaffID)
}

func TestService_ListRecordings_DefaultsInvalidLimit(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)

	page, err := svc.ListRecordings(context.Background(), uuid.New(), ListRecordingsInput{Limit: -5})
	require.NoError(t, err)
	assert.Equal(t, 20, page.Limit)

	page2, err := svc.ListRecordings(context.Background(), uuid.New(), ListRecordingsInput{Limit: 9999})
	require.NoError(t, err)
	assert.Equal(t, 20, page2.Limit)
}

// ── ConfirmUpload ─────────────────────────────────────────────────────────────

func TestService_ConfirmUpload_TransitionsToUploaded(t *testing.T) {
	t.Parallel()
	svc, _, enq := newTestService(t)
	clinicID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	resp, err := svc.ConfirmUpload(context.Background(), recID, clinicID)
	require.NoError(t, err)
	assert.Equal(t, domain.RecordingStatusUploaded, resp.Status)

	// Transcription job must be enqueued.
	require.Len(t, enq.inserted, 1)
	args, ok := enq.inserted[0].(TranscribeAudioArgs)
	require.True(t, ok)
	assert.Equal(t, recID, args.RecordingID)
}

func TestService_ConfirmUpload_AlreadyUploaded_ReturnsConflict(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	_, err := svc.ConfirmUpload(context.Background(), recID, clinicID)
	require.NoError(t, err)

	// Second confirm must fail.
	_, err = svc.ConfirmUpload(context.Background(), recID, clinicID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestService_ConfirmUpload_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	_, err := svc.ConfirmUpload(context.Background(), recID, uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── LinkSubject ───────────────────────────────────────────────────────────────

func TestService_LinkSubject_UpdatesSubjectID(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()
	subjectID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	resp, err := svc.LinkSubject(context.Background(), recID, clinicID, subjectID)
	require.NoError(t, err)
	require.NotNil(t, resp.SubjectID)
	assert.Equal(t, subjectID.String(), *resp.SubjectID)
}

func TestService_LinkSubject_WrongClinic_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t)
	clinicID := uuid.New()

	created := seedRecording(t, svc, clinicID, uuid.New())
	recID, _ := uuid.Parse(created.Recording.ID)

	_, err := svc.LinkSubject(context.Background(), recID, uuid.New(), uuid.New())
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// ── buildFileKey ──────────────────────────────────────────────────────────────

func TestBuildFileKey_ContainsClinicAndRecordingIDs(t *testing.T) {
	t.Parallel()
	clinicID := uuid.New()
	recID := uuid.New()

	key := buildFileKey(clinicID, recID, "audio/mp4")
	assert.Contains(t, key, clinicID.String())
	assert.Contains(t, key, recID.String())
	assert.Contains(t, key, ".m4a")
}

func TestBuildFileKey_UnknownContentType_FallsBackToAudioExt(t *testing.T) {
	t.Parallel()
	key := buildFileKey(uuid.New(), uuid.New(), "audio/flac")
	assert.Contains(t, key, ".audio")
}
