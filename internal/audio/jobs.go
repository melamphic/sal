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
// River's exponential backoff reaches ~3h cumulative by retry 6, so 6h gives
// safe margin for the URL to outlast all realistic retry attempts.
const downloadTTLForJob = 6 * time.Hour

// ── TranscribeAudio job ───────────────────────────────────────────────────────

// TranscribeAudioArgs is the job payload for the TranscribeAudio River worker.
// River serialises this to JSON when enqueueing and deserialises it at work time.
type TranscribeAudioArgs struct {
	RecordingID uuid.UUID `json:"recording_id"`
}

// Kind returns the unique job kind string River uses to route jobs to workers.
func (TranscribeAudioArgs) Kind() string { return "transcribe_audio" }

// TranscriptListener is fired by the TranscribeAudioWorker the moment a
// transcript lands on the recording row. Downstream modules (notes,
// incidents, consent, pain) implement this to enqueue their own AI
// extraction without polling. Replaces the 8-second-guess-and-race
// pattern that lived in notes.Service.CreateNote.
//
// Listeners run synchronously inside the worker — keep the impl narrow
// (typically: look up "is there a draft of mine waiting for this
// recording?" and Insert a downstream River job). Listener errors are
// swallowed so a downstream registration failure doesn't roll back the
// transcript persistence; downstream modules are responsible for their
// own retry path via River.
type TranscriptListener interface {
	OnRecordingTranscribed(ctx context.Context, recordingID uuid.UUID) error
}

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
	transcriber Transcriber          // nil = skip transcription (no provider configured)
	listeners   []TranscriptListener // fan-out after transcript is persisted
}

// NewTranscribeAudioWorker constructs a TranscribeAudioWorker.
// Pass nil for transcriber to skip transcription gracefully (dev without any API key).
// Listeners can be empty; downstream extractors register via app.go wiring.
func NewTranscribeAudioWorker(r repo, store blobStore, transcriber Transcriber, listeners ...TranscriptListener) *TranscribeAudioWorker {
	return &TranscribeAudioWorker{repo: r, store: store, transcriber: transcriber, listeners: listeners}
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

	if _, err := w.repo.UpdateRecordingTranscript(ctx, recID, result.Transcript, result.DurationSeconds, result.WordConfidences); err != nil {
		return fmt.Errorf("transcribe_audio: save transcript: %w", err)
	}

	// Fan-out to downstream listeners (notes / incidents / consent / pain
	// AI extractors). The transcript is the load-bearing side effect —
	// listener errors don't roll back transcription. Each listener owns
	// its own retry path via River.
	for _, l := range w.listeners {
		_ = l.OnRecordingTranscribed(ctx, recID)
	}

	return nil
}
