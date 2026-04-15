package audio

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
)

// downloadTTLForJob is how long the pre-signed URL given to Deepgram is valid.
const downloadTTLForJob = 1 * time.Hour

// ── TranscribeAudio job ───────────────────────────────────────────────────────

// TranscribeAudioArgs is the job payload for the TranscribeAudio River worker.
// River serialises this to JSON when enqueueing and deserialises it at work time.
type TranscribeAudioArgs struct {
	RecordingID uuid.UUID `json:"recording_id"`
}

// Kind returns the unique job kind string River uses to route jobs to workers.
func (TranscribeAudioArgs) Kind() string { return "transcribe_audio" }

// TranscribeAudioWorker is the River worker that transcribes an uploaded audio
// file and stores the result on the recording row.
// The transcription provider is injected — Deepgram in production, Gemini in dev.
//
// Retry behaviour is governed by River's default exponential backoff policy
// (up to 25 retries). The first retry fires after ~1 minute, the fifth after
// ~30 minutes — safe for transient provider outages.
type TranscribeAudioWorker struct {
	river.WorkerDefaults[TranscribeAudioArgs]
	repo        repo
	store       blobStore
	transcriber Transcriber // nil = skip transcription (no provider configured)
}

// NewTranscribeAudioWorker constructs a TranscribeAudioWorker.
// Pass nil for transcriber to skip transcription gracefully (dev without any API key).
func NewTranscribeAudioWorker(r repo, store blobStore, transcriber Transcriber) *TranscribeAudioWorker {
	return &TranscribeAudioWorker{repo: r, store: store, transcriber: transcriber}
}

// Work is called by River for each TranscribeAudio job.
func (w *TranscribeAudioWorker) Work(ctx context.Context, job *river.Job[TranscribeAudioArgs]) error {
	recID := job.Args.RecordingID

	rec, err := w.repo.UpdateRecordingStatus(ctx, recID, domain.RecordingStatusTranscribing, nil)
	if err != nil {
		return fmt.Errorf("transcribe_audio: mark transcribing: %w", err)
	}

	if w.transcriber == nil {
		msg := "no transcription provider configured (set TRANSCRIPTION_PROVIDER and the corresponding API key)"
		_, _ = w.repo.UpdateRecordingStatus(ctx, recID, domain.RecordingStatusFailed, &msg)
		return nil
	}

	presignedURL, err := w.store.PresignDownload(ctx, rec.FileKey, downloadTTLForJob)
	if err != nil {
		return fmt.Errorf("transcribe_audio: presign download: %w", err)
	}

	result, err := w.transcriber.Transcribe(ctx, presignedURL, rec.ContentType)
	if err != nil {
		errMsg := err.Error()
		_, _ = w.repo.UpdateRecordingStatus(ctx, recID, domain.RecordingStatusFailed, &errMsg)
		return fmt.Errorf("transcribe_audio: transcribe: %w", err)
	}

	if _, err := w.repo.UpdateRecordingTranscript(ctx, recID, result.Transcript, result.DurationSeconds); err != nil {
		return fmt.Errorf("transcribe_audio: save transcript: %w", err)
	}

	return nil
}
