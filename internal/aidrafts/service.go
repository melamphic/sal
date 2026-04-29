package aidrafts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// jobEnqueuer is the subset of river.Client used by the service to
// enqueue ExtractAIDraftArgs.
type jobEnqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RecordingProvider checks whether a recording's transcript has already
// been persisted at draft-creation time. If yes the service enqueues
// extraction immediately; if no it leaves the row at status =
// `pending_transcript` and waits for the audio listener to fire.
//
// Same shape as notes.RecordingProvider — wired by an app.go adapter.
type RecordingProvider interface {
	GetTranscript(ctx context.Context, recordingID uuid.UUID) (*string, error)
}

// ── Wire types ────────────────────────────────────────────────────────────────

//nolint:revive
type DraftResponse struct {
	ID             string  `json:"id"`
	TargetType     string  `json:"target_type"`
	RecordingID    *string `json:"recording_id,omitempty"`
	Status         string  `json:"status"`
	DraftPayload   *string `json:"draft_payload,omitempty"` // raw JSON string; UI parses by target_type
	ContextPayload *string `json:"context_payload,omitempty"`
	ErrorMessage   *string `json:"error_message,omitempty"`
	AIProvider     *string `json:"ai_provider,omitempty"`
	AIModel        *string `json:"ai_model,omitempty"`
	PromptHash     *string `json:"prompt_hash,omitempty"`
	CreatedAt      string  `json:"created_at"`
	CompletedAt    *string `json:"completed_at,omitempty"`
}

// ── Service ──────────────────────────────────────────────────────────────────

type Service struct {
	repo      *Repository
	enqueue   jobEnqueuer
	recording RecordingProvider
}

func NewService(r *Repository, enqueue jobEnqueuer, recording RecordingProvider) *Service {
	return &Service{repo: r, enqueue: enqueue, recording: recording}
}

// CreateDraftInput is the service input for starting a new draft.
type CreateDraftInput struct {
	ClinicID       uuid.UUID
	StaffID        uuid.UUID
	TargetType     string
	RecordingID    *uuid.UUID
	ContextPayload *string // JSONB as text — passed through verbatim
}

// CreateDraft validates input, persists the row, and decides whether
// to enqueue extraction now (transcript already done) or wait for the
// listener (transcript still in progress).
func (s *Service) CreateDraft(ctx context.Context, in CreateDraftInput) (*DraftResponse, error) {
	if !validTargetType(in.TargetType) {
		return nil, fmt.Errorf("aidrafts.service.CreateDraft: invalid target_type: %w", domain.ErrValidation)
	}
	if in.RecordingID == nil {
		return nil, fmt.Errorf("aidrafts.service.CreateDraft: recording_id required (no audio = no AI draft): %w", domain.ErrValidation)
	}

	rec, err := s.repo.CreateDraft(ctx, CreateDraftParams{
		ID:             domain.NewID(),
		ClinicID:       in.ClinicID,
		TargetType:     in.TargetType,
		RecordingID:    in.RecordingID,
		ContextPayload: in.ContextPayload,
		RequestedBy:    in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("aidrafts.service.CreateDraft: %w", err)
	}

	// If the transcript is already on the recording (e.g. user uploaded
	// audio earlier and only now requested an AI draft), fire extraction
	// immediately. Otherwise leave at pending_transcript — the audio
	// TranscribeAudioWorker fan-out will fire OnRecordingTranscribed
	// when the transcript lands.
	transcript, _ := s.recording.GetTranscript(ctx, *in.RecordingID)
	if transcript != nil && *transcript != "" {
		s.enqueueExtract(ctx, rec.ID)
	}

	return draftToResponse(rec), nil
}

// GetDraft — clinic-scoped read.
func (s *Service) GetDraft(ctx context.Context, id, clinicID uuid.UUID) (*DraftResponse, error) {
	rec, err := s.repo.GetDraft(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("aidrafts.service.GetDraft: %w", err)
	}
	return draftToResponse(rec), nil
}

// OnRecordingTranscribed satisfies audio.TranscriptListener. Called by
// the audio TranscribeAudioWorker the instant a transcript lands —
// every pending_transcript draft for this recording fires its
// extraction worker.
func (s *Service) OnRecordingTranscribed(ctx context.Context, recordingID uuid.UUID) error {
	ids, err := s.repo.ListPendingByRecording(ctx, recordingID)
	if err != nil {
		return fmt.Errorf("aidrafts.service.OnRecordingTranscribed: %w", err)
	}
	for _, id := range ids {
		s.enqueueExtract(ctx, id)
	}
	return nil
}

// enqueueExtract fires the worker. UniqueOpts keeps the create-time
// enqueue + listener-time enqueue from racing into two jobs.
func (s *Service) enqueueExtract(ctx context.Context, draftID uuid.UUID) {
	opts := &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
	_, _ = s.enqueue.Insert(ctx, ExtractAIDraftArgs{DraftID: draftID}, opts)
}

// ── Validators ───────────────────────────────────────────────────────────────

func validTargetType(t string) bool {
	switch t {
	case "incident", "consent", "pain", "pre_encounter_brief":
		return true
	}
	return false
}

// ── Converter ────────────────────────────────────────────────────────────────

func draftToResponse(r *DraftRecord) *DraftResponse {
	out := &DraftResponse{
		ID:             r.ID.String(),
		TargetType:     r.TargetType,
		Status:         r.Status,
		DraftPayload:   r.DraftPayload,
		ContextPayload: r.ContextPayload,
		ErrorMessage:   r.ErrorMessage,
		AIProvider:     r.AIProvider,
		AIModel:        r.AIModel,
		PromptHash:     r.PromptHash,
		CreatedAt:      r.CreatedAt.Format(time.RFC3339),
	}
	if r.RecordingID != nil {
		s := r.RecordingID.String()
		out.RecordingID = &s
	}
	if r.CompletedAt != nil {
		s := r.CompletedAt.Format(time.RFC3339)
		out.CompletedAt = &s
	}
	return out
}

