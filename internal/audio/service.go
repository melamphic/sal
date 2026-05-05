package audio

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/storage"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// blobStore is the subset of storage.Store used by the service.
// The interface lets unit tests inject a fake without an AWS/MinIO connection.
type blobStore interface {
	PresignUpload(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignDownload(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// jobEnqueuer is the subset of river.Client used by the service.
// Keeping it as an interface lets unit tests inject a fake without
// spinning up a real River client and Postgres connection.
type jobEnqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// uploadURLTTL is how long a pre-signed upload URL is valid.
// 15 minutes is enough for any mobile upload on a reasonable connection.
const uploadURLTTL = 15 * time.Minute

// downloadURLTTL is how long a pre-signed download URL is valid.
// Deepgram must be able to fetch the file within this window.
const downloadURLTTL = 1 * time.Hour

// Service handles business logic for the audio module.
type Service struct {
	repo    repo
	store   blobStore
	enqueue jobEnqueuer
}

// NewService constructs an audio Service.
func NewService(r repo, store *storage.Store, enqueue jobEnqueuer) *Service {
	return &Service{repo: r, store: store, enqueue: enqueue}
}

// ── Response types ────────────────────────────────────────────────────────────

// RecordingResponse is the API-safe representation of a recording.
//
//nolint:revive
type RecordingResponse struct {
	ID              string                 `json:"id"`
	ClinicID        string                 `json:"clinic_id"`
	StaffID         string                 `json:"staff_id"`
	SubjectID       *string                `json:"subject_id,omitempty"`
	Status          domain.RecordingStatus `json:"status"`
	ContentType     string                 `json:"content_type"`
	DurationSeconds *int                   `json:"duration_seconds,omitempty"`
	Transcript      *string                `json:"transcript,omitempty"`
	CreatedAt       string                 `json:"created_at"`
	UpdatedAt       string                 `json:"updated_at"`
}

// CreateRecordingResponse includes the RecordingResponse plus the one-time upload URL.
//
//nolint:revive
type CreateRecordingResponse struct {
	Recording *RecordingResponse `json:"recording"`
	// UploadURL is a pre-signed PUT URL for the client to upload audio directly
	// to object storage. Valid for 15 minutes. The client must PUT with the
	// Content-Type matching recording.content_type.
	UploadURL string `json:"upload_url"`
}

// RecordingListResponse is a paginated list of recordings.
//
//nolint:revive
type RecordingListResponse struct {
	Items  []*RecordingResponse `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

// DownloadURLResponse holds a short-lived download URL.
//
//nolint:revive
type DownloadURLResponse struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateRecordingInput holds validated input for initiating a recording upload.
type CreateRecordingInput struct {
	ClinicID    uuid.UUID
	StaffID     uuid.UUID
	SubjectID   *uuid.UUID // optional — can be linked later
	ContentType string     // audio MIME type, e.g. "audio/mp4"
}

// ListRecordingsInput holds filter and pagination parameters.
type ListRecordingsInput struct {
	Limit     int
	Offset    int
	SubjectID *uuid.UUID
	StaffID   *uuid.UUID
	Status    *domain.RecordingStatus
}

// ── Service methods ───────────────────────────────────────────────────────────

// CreateRecording creates a recording row and returns a pre-signed upload URL.
// The client uploads the audio file directly to storage using the returned URL.
// After uploading, the client must call ConfirmUpload to trigger transcription.
func (s *Service) CreateRecording(ctx context.Context, input CreateRecordingInput) (*CreateRecordingResponse, error) {
	id := domain.NewID()
	fileKey := buildFileKey(input.ClinicID, id, input.ContentType)

	rec, err := s.repo.CreateRecording(ctx, CreateRecordingParams{
		ID:          id,
		ClinicID:    input.ClinicID,
		StaffID:     input.StaffID,
		SubjectID:   input.SubjectID,
		FileKey:     fileKey,
		ContentType: input.ContentType,
	})
	if err != nil {
		return nil, fmt.Errorf("audio.service.CreateRecording: %w", err)
	}

	uploadURL, err := s.store.PresignUpload(ctx, fileKey, input.ContentType, uploadURLTTL)
	if err != nil {
		return nil, fmt.Errorf("audio.service.CreateRecording: presign: %w", err)
	}

	return &CreateRecordingResponse{
		Recording: toRecordingResponse(rec),
		UploadURL: uploadURL,
	}, nil
}

// GetRecordingByID fetches a recording scoped to the clinic.
func (s *Service) GetRecordingByID(ctx context.Context, id, clinicID uuid.UUID) (*RecordingResponse, error) {
	rec, err := s.repo.GetRecordingByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("audio.service.GetRecordingByID: %w", err)
	}
	return toRecordingResponse(rec), nil
}

// ListRecordings returns a paginated list of recordings for a clinic.
func (s *Service) ListRecordings(ctx context.Context, clinicID uuid.UUID, input ListRecordingsInput) (*RecordingListResponse, error) {
	input.Limit = clampLimit(input.Limit)

	recs, total, err := s.repo.ListRecordings(ctx, clinicID, ListRecordingsParams(input))
	if err != nil {
		return nil, fmt.Errorf("audio.service.ListRecordings: %w", err)
	}

	items := make([]*RecordingResponse, len(recs))
	for i, rec := range recs {
		items[i] = toRecordingResponse(rec)
	}

	return &RecordingListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// RetryTranscription re-runs the TranscribeAudio job for a recording whose
// transcription previously failed (e.g. transient provider error or quota
// exhaustion). Resets status to uploaded, clears the prior error, and
// re-enqueues the worker. The downstream extract_note worker is woken via
// the OnRecordingTranscribed listener once the new transcript lands, so the
// caller does not need to touch the note state directly.
//
// Rejects recordings that are not in the failed state with ErrConflict —
// retrying a transcribed or in-flight recording would race the existing job.
func (s *Service) RetryTranscription(ctx context.Context, id, clinicID uuid.UUID) error {
	rec, err := s.repo.GetRecordingByID(ctx, id, clinicID)
	if err != nil {
		return fmt.Errorf("audio.service.RetryTranscription: %w", err)
	}
	if rec.Status != domain.RecordingStatusFailed {
		return fmt.Errorf("audio.service.RetryTranscription: %w", domain.ErrConflict)
	}
	if _, err := s.repo.UpdateRecordingStatus(ctx, id, domain.RecordingStatusUploaded, nil); err != nil {
		return fmt.Errorf("audio.service.RetryTranscription: reset status: %w", err)
	}
	if _, err := s.enqueue.Insert(ctx, TranscribeAudioArgs{RecordingID: id}, nil); err != nil {
		return fmt.Errorf("audio.service.RetryTranscription: enqueue: %w", err)
	}
	return nil
}

// ConfirmUpload transitions a recording from pending_upload → uploaded and
// enqueues a TranscribeAudio River job. This must be called by the client after
// the direct S3 upload completes.
func (s *Service) ConfirmUpload(ctx context.Context, id, clinicID uuid.UUID) (*RecordingResponse, error) {
	rec, err := s.repo.GetRecordingByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("audio.service.ConfirmUpload: %w", err)
	}

	if rec.Status != domain.RecordingStatusPendingUpload {
		return nil, fmt.Errorf("audio.service.ConfirmUpload: %w", domain.ErrConflict)
	}

	rec, err = s.repo.UpdateRecordingStatus(ctx, id, domain.RecordingStatusUploaded, nil)
	if err != nil {
		return nil, fmt.Errorf("audio.service.ConfirmUpload: %w", err)
	}

	// Enqueue transcription job. River persists the job in the DB transactionally;
	// if this fails the status update still committed (idempotent re-confirm allowed).
	if _, err := s.enqueue.Insert(ctx, TranscribeAudioArgs{RecordingID: id}, nil); err != nil {
		return nil, fmt.Errorf("audio.service.ConfirmUpload: enqueue: %w", err)
	}

	return toRecordingResponse(rec), nil
}

// GetDownloadURL returns a short-lived pre-signed GET URL for the recording.
// Clients use this to stream audio for playback in the review screen.
func (s *Service) GetDownloadURL(ctx context.Context, id, clinicID uuid.UUID) (*DownloadURLResponse, error) {
	rec, err := s.repo.GetRecordingByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("audio.service.GetDownloadURL: %w", err)
	}

	expiresAt := domain.TimeNow().Add(downloadURLTTL)
	url, err := s.store.PresignDownload(ctx, rec.FileKey, downloadURLTTL)
	if err != nil {
		return nil, fmt.Errorf("audio.service.GetDownloadURL: %w", err)
	}

	return &DownloadURLResponse{
		URL:       url,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}, nil
}

// LinkSubject links a recording to a patient subject after the fact.
func (s *Service) LinkSubject(ctx context.Context, id, clinicID, subjectID uuid.UUID) (*RecordingResponse, error) {
	rec, err := s.repo.LinkSubject(ctx, id, clinicID, subjectID)
	if err != nil {
		return nil, fmt.Errorf("audio.service.LinkSubject: %w", err)
	}
	return toRecordingResponse(rec), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// buildFileKey constructs a deterministic, UUID-based object storage path.
// Format: clinics/{clinicID}/recordings/{recordingID}.{ext}
// The UUID path ensures no PII is embedded in storage paths.
func buildFileKey(clinicID, recordingID uuid.UUID, contentType string) string {
	ext := extensionFor(contentType)
	return fmt.Sprintf("clinics/%s/recordings/%s%s", clinicID, recordingID, ext)
}

// extensionFor maps common audio MIME types to file extensions.
func extensionFor(contentType string) string {
	switch contentType {
	case "audio/mp4", "audio/m4a":
		return ".m4a"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	default:
		return ".audio"
	}
}

func toRecordingResponse(rec *RecordingRecord) *RecordingResponse {
	r := &RecordingResponse{
		ID:              rec.ID.String(),
		ClinicID:        rec.ClinicID.String(),
		StaffID:         rec.StaffID.String(),
		Status:          rec.Status,
		ContentType:     rec.ContentType,
		DurationSeconds: rec.DurationSeconds,
		Transcript:      rec.Transcript,
		CreatedAt:       rec.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       rec.UpdatedAt.Format(time.RFC3339),
	}
	if rec.SubjectID != nil {
		s := rec.SubjectID.String()
		r.SubjectID = &s
	}
	return r
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}
