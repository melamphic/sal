package audio

import (
	"context"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/confidence"
)

// repo is the internal data-access interface for the audio module.
// The concrete implementation is in repository.go; tests use fakeRepo.
type repo interface {
	CreateRecording(ctx context.Context, p CreateRecordingParams) (*RecordingRecord, error)
	GetRecordingByID(ctx context.Context, id, clinicID uuid.UUID) (*RecordingRecord, error)
	ListRecordings(ctx context.Context, clinicID uuid.UUID, p ListRecordingsParams) ([]*RecordingRecord, int, error)
	UpdateRecordingStatus(ctx context.Context, id uuid.UUID, status domain.RecordingStatus, errorMsg *string) (*RecordingRecord, error)
	UpdateRecordingTranscript(ctx context.Context, id uuid.UUID, transcript string, durationSeconds *int, wordConfidences []confidence.WordConfidence) (*RecordingRecord, error)
	LinkSubject(ctx context.Context, id, clinicID, subjectID uuid.UUID) (*RecordingRecord, error)
}
