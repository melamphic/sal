package audio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	listenapi "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest"
	interfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	"github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"
	"google.golang.org/genai"
)

// TranscriptResult holds the output of a transcription call.
type TranscriptResult struct {
	Transcript      string
	DurationSeconds *int
}

// Transcriber is the interface for audio-to-text providers.
// DeepgramTranscriber is used in production; GeminiTranscriber in dev/staging.
type Transcriber interface {
	// Transcribe downloads audio from presignedURL and returns a transcript.
	// contentType is the audio MIME type (e.g. "audio/mp4", "audio/wav").
	Transcribe(ctx context.Context, presignedURL, contentType string) (*TranscriptResult, error)
}

// ── Deepgram ──────────────────────────────────────────────────────────────────

// DeepgramTranscriber calls Deepgram Nova-3 Medical for production transcription.
// Passes the presigned URL directly — no download needed on the server side.
type DeepgramTranscriber struct {
	apiKey string
}

// NewDeepgramTranscriber constructs a DeepgramTranscriber.
func NewDeepgramTranscriber(apiKey string) *DeepgramTranscriber {
	return &DeepgramTranscriber{apiKey: apiKey}
}

// Transcribe calls Deepgram with the presigned URL and returns the transcript.
func (t *DeepgramTranscriber) Transcribe(ctx context.Context, presignedURL, _ string) (*TranscriptResult, error) {
	listen.InitWithDefault()

	c := listen.NewREST(t.apiKey, &interfaces.ClientOptions{})
	dg := listenapi.New(c)

	opts := &interfaces.PreRecordedTranscriptionOptions{
		Model:      "nova-3-medical",
		Punctuate:  true,
		Diarize:    true,
		Utterances: true,
	}

	resp, err := dg.FromURL(ctx, presignedURL, opts)
	if err != nil {
		return nil, fmt.Errorf("audio.deepgram.Transcribe: %w", err)
	}

	result := &TranscriptResult{}
	if resp.Results != nil && len(resp.Results.Channels) > 0 {
		ch := resp.Results.Channels[0]
		if len(ch.Alternatives) > 0 {
			result.Transcript = ch.Alternatives[0].Transcript
		}
	}
	if resp.Metadata != nil {
		d := int(resp.Metadata.Duration)
		result.DurationSeconds = &d
	}
	return result, nil
}

// ── Gemini ────────────────────────────────────────────────────────────────────

const geminiTranscribeModel = "gemini-2.5-flash"

// GeminiTranscriber uses Gemini's audio understanding for transcription.
// Suitable for development and staging — free tier, no Deepgram account needed.
// Downloads the audio bytes from the presigned URL then passes inline to Gemini.
// Note: does not produce word-level confidence scores (Deepgram-only feature).
type GeminiTranscriber struct {
	client *genai.Client
}

// NewGeminiTranscriber constructs a GeminiTranscriber from an API key.
func NewGeminiTranscriber(ctx context.Context, apiKey string) (*GeminiTranscriber, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("audio.gemini.NewGeminiTranscriber: %w", err)
	}
	return &GeminiTranscriber{client: client}, nil
}

// Transcribe downloads the audio from presignedURL and transcribes it via Gemini.
func (t *GeminiTranscriber) Transcribe(ctx context.Context, presignedURL, contentType string) (*TranscriptResult, error) {
	audioBytes, err := downloadBytes(presignedURL)
	if err != nil {
		return nil, fmt.Errorf("audio.gemini.Transcribe: download: %w", err)
	}

	if contentType == "" {
		contentType = "audio/mp4"
	}

	contents := []*genai.Content{
		genai.NewContentFromParts(
			[]*genai.Part{
				genai.NewPartFromText("Transcribe this clinical audio recording verbatim. Return only the transcript text, no commentary or formatting."),
				genai.NewPartFromBytes(audioBytes, contentType),
			},
			genai.RoleUser,
		),
	}
	resp, err := t.client.Models.GenerateContent(
		ctx,
		geminiTranscribeModel,
		contents,
		&genai.GenerateContentConfig{
			Temperature:    genai.Ptr[float32](0),
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("audio.gemini.Transcribe: generate: %w", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("audio.gemini.Transcribe: empty response from model")
	}

	return &TranscriptResult{
		Transcript: resp.Candidates[0].Content.Parts[0].Text,
	}, nil
}

// downloadBytes fetches the content at url into memory.
// Audio files in dev are small (< 50 MB); not suited for large production files.
func downloadBytes(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("downloadBytes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloadBytes: unexpected status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("downloadBytes: read: %w", err)
	}
	return b, nil
}
