package audio

import (
	"context"
	"fmt"
	"strings"
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

// TranscribeFailedListener fires when the TranscribeAudioWorker exhausts
// its retries (or hits a non-retryable error). Downstream modules (notes,
// ai_drafts) implement this to mark any draft that was waiting on the
// transcript as failed, so the UI can surface the error and offer retry
// instead of staying stuck on a "queued / transcribing" spinner forever.
//
// Listener errors are swallowed — transcription's permanent-failure stamp
// is the load-bearing side effect; downstream cascades are best-effort.
type TranscribeFailedListener interface {
	OnRecordingTranscribeFailed(ctx context.Context, recordingID uuid.UUID, errorMessage string) error
}

// TranscribeAudioWorker is the River worker that transcribes an uploaded audio
// file and stores the result on the recording row.
// The transcription provider is injected — Deepgram in production, Gemini in dev.
//
// Retry behaviour is governed by River's default exponential backoff policy
// (up to 25 retries). The first retry fires after ~1 minute, the fifth after
// ~30 minutes — safe for transient provider outages.
//
// Status transitions are designed to NOT oscillate during River's retry
// backoff window. The recording stays in `transcribing` state across
// transient failures (the user understands "still trying"); only the very
// last attempt or a definitively non-retryable error flips status to
// `failed`. Without this rule, FE clients refreshing mid-backoff see the
// status flicker between `transcribing` and `failed`, and downstream
// listeners (notes' extract_note worker) repeatedly mark drafts failed
// only to flip them back to extracting on the next attempt.
type TranscribeAudioWorker struct {
	river.WorkerDefaults[TranscribeAudioArgs]
	repo            repo
	store           blobStore
	transcriber     Transcriber          // nil = skip transcription (no provider configured)
	listeners       []TranscriptListener // fan-out after transcript is persisted
	failedListeners []TranscribeFailedListener
}

// NewTranscribeAudioWorker constructs a TranscribeAudioWorker.
// Pass nil for transcriber to skip transcription gracefully (dev without any API key).
// Listeners can be empty; downstream extractors register via app.go wiring.
func NewTranscribeAudioWorker(r repo, store blobStore, transcriber Transcriber, listeners ...TranscriptListener) *TranscribeAudioWorker {
	return &TranscribeAudioWorker{repo: r, store: store, transcriber: transcriber, listeners: listeners}
}

// AddFailedListener registers a listener that fires when transcription
// permanently fails (last attempt or non-retryable error).
func (w *TranscribeAudioWorker) AddFailedListener(l TranscribeFailedListener) {
	w.failedListeners = append(w.failedListeners, l)
}

// isRetryableTranscribeError matches the same set classified as transient
// by the extraction pipeline — 429 quota / 503 unavailable / 504 timeout.
// Other errors (auth, schema, malformed audio) are permanent and surface
// immediately rather than burning 25 retries.
func isRetryableTranscribeError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "429"),
		strings.Contains(low, "rate limit"),
		strings.Contains(low, "quota"),
		strings.Contains(low, "503"),
		strings.Contains(low, "504"),
		strings.Contains(low, "unavailable"),
		strings.Contains(low, "timed out"),
		strings.Contains(low, "deadline exceeded"):
		return true
	}
	return false
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
		w.fireFailed(ctx, recID, msg)
		return nil
	}

	presignedURL, err := w.store.PresignDownload(ctx, rec.FileKey, downloadTTLForJob)
	if err != nil {
		return fmt.Errorf("transcribe_audio: presign download: %w", err)
	}

	result, err := w.transcriber.Transcribe(ctx, presignedURL, rec.ContentType)
	if err != nil {
		// Only stamp recording=failed on the last attempt or a non-retryable
		// error. During River's backoff between transient failures the row
		// stays in `transcribing` so the FE doesn't oscillate between
		// "transcribing" and "failed" each cycle. Downstream listeners are
		// only fired on permanent failure for the same reason.
		lastAttempt := job.Attempt >= job.MaxAttempts
		retryable := isRetryableTranscribeError(err)
		if !retryable || lastAttempt {
			errMsg := err.Error()
			_, _ = w.repo.UpdateRecordingStatus(ctx, recID, domain.RecordingStatusFailed, &errMsg)
			w.fireFailed(ctx, recID, errMsg)
		}
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

func (w *TranscribeAudioWorker) fireFailed(ctx context.Context, recID uuid.UUID, errMsg string) {
	for _, l := range w.failedListeners {
		_ = l.OnRecordingTranscribeFailed(ctx, recID, errMsg)
	}
}
