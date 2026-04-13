package audio

import (
	"context"
	"fmt"
	"time"

	listenapi "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest"
	interfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	"github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"
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

// TranscribeAudioWorker is the River worker that calls Deepgram Nova-3 Medical
// to transcribe an uploaded audio file and stores the result on the recording row.
//
// Retry behaviour is governed by River's default exponential backoff policy
// (up to 25 retries). The first retry fires after ~1 minute, the fifth after
// ~30 minutes — safe for transient Deepgram outages.
type TranscribeAudioWorker struct {
	river.WorkerDefaults[TranscribeAudioArgs]
	repo           repo
	store          blobStore
	deepgramAPIKey string
}

// NewTranscribeAudioWorker constructs a TranscribeAudioWorker.
// deepgramAPIKey may be empty — the worker skips transcription gracefully when
// no key is set (useful in development without a Deepgram account).
func NewTranscribeAudioWorker(r repo, store blobStore, deepgramAPIKey string) *TranscribeAudioWorker {
	return &TranscribeAudioWorker{
		repo:           r,
		store:          store,
		deepgramAPIKey: deepgramAPIKey,
	}
}

// Work is called by River for each TranscribeAudio job.
func (w *TranscribeAudioWorker) Work(ctx context.Context, job *river.Job[TranscribeAudioArgs]) error {
	recID := job.Args.RecordingID

	// Mark as transcribing so the UI can show a spinner.
	rec, err := w.repo.UpdateRecordingStatus(ctx, recID, domain.RecordingStatusTranscribing, nil)
	if err != nil {
		return fmt.Errorf("transcribe_audio: mark transcribing: %w", err)
	}

	// If no API key is configured, skip transcription. Status stays transcribing
	// so developers can see the job ran without needing a live Deepgram account.
	if w.deepgramAPIKey == "" {
		return nil
	}

	// Get a pre-signed download URL so Deepgram can fetch the audio file.
	downloadURL, err := w.store.PresignDownload(ctx, rec.FileKey, downloadTTLForJob)
	if err != nil {
		return fmt.Errorf("transcribe_audio: presign download: %w", err)
	}

	// Initialise Deepgram SDK (idempotent — safe to call multiple times).
	listen.InitWithDefault()

	// Call Deepgram Nova-3 Medical (pre-recorded REST API).
	c := listen.NewREST(w.deepgramAPIKey, &interfaces.ClientOptions{})
	dg := listenapi.New(c)

	opts := &interfaces.PreRecordedTranscriptionOptions{
		Model:      "nova-3-medical",
		Punctuate:  true,
		Diarize:    true,
		Utterances: true,
	}

	resp, err := dg.FromURL(ctx, downloadURL, opts)
	if err != nil {
		errMsg := err.Error()
		_, _ = w.repo.UpdateRecordingStatus(ctx, recID, domain.RecordingStatusFailed, &errMsg)
		return fmt.Errorf("transcribe_audio: deepgram: %w", err)
	}

	// Extract the full transcript and duration from the Deepgram response.
	transcript := ""
	var durationSeconds *int

	if resp.Results != nil && len(resp.Results.Channels) > 0 {
		ch := resp.Results.Channels[0]
		if len(ch.Alternatives) > 0 {
			transcript = ch.Alternatives[0].Transcript
		}
	}
	if resp.Metadata != nil {
		d := int(resp.Metadata.Duration)
		durationSeconds = &d
	}

	if _, err := w.repo.UpdateRecordingTranscript(ctx, recID, transcript, durationSeconds); err != nil {
		return fmt.Errorf("transcribe_audio: save transcript: %w", err)
	}

	return nil
}
