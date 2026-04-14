package notes

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// jobEnqueuer is the subset of river.Client used by the service.
type jobEnqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// maxNotesPerRecording is the maximum number of notes (form fills) per recording.
const maxNotesPerRecording = 3

// Service handles business logic for the notes module.
type Service struct {
	repo    repo
	enqueue jobEnqueuer
}

// NewService constructs a notes Service.
func NewService(r repo, riverClient jobEnqueuer) *Service {
	return &Service{repo: r, enqueue: riverClient}
}

// ── Response types ────────────────────────────────────────────────────────────

// NoteFieldResponse is the API-safe representation of a single note field.
type NoteFieldResponse struct {
	FieldID      string   `json:"field_id"`
	Value        *string  `json:"value,omitempty"`      // JSON-encoded
	Confidence   *float64 `json:"confidence,omitempty"` // 0.0–1.0
	SourceQuote  *string  `json:"source_quote,omitempty"`
	OverriddenBy *string  `json:"overridden_by,omitempty"`
	OverriddenAt *string  `json:"overridden_at,omitempty"`
}

// NoteResponse is the API-safe representation of a clinical note.
//
//nolint:revive
type NoteResponse struct {
	ID            string               `json:"id"`
	ClinicID      string               `json:"clinic_id"`
	RecordingID   string               `json:"recording_id"`
	FormVersionID string               `json:"form_version_id"`
	SubjectID     *string              `json:"subject_id,omitempty"`
	CreatedBy     string               `json:"created_by"`
	Status        domain.NoteStatus    `json:"status"`
	ErrorMessage  *string              `json:"error_message,omitempty"`
	SubmittedAt   *string              `json:"submitted_at,omitempty"`
	SubmittedBy   *string              `json:"submitted_by,omitempty"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
	Fields        []*NoteFieldResponse `json:"fields,omitempty"`
}

// NoteListResponse is a paginated list of notes.
//
//nolint:revive
type NoteListResponse struct {
	Items  []*NoteResponse `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateNoteInput holds validated input for creating a new note.
type CreateNoteInput struct {
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	RecordingID   uuid.UUID
	FormVersionID uuid.UUID
	SubjectID     *uuid.UUID
}

// ListNotesInput holds filter and pagination parameters.
type ListNotesInput struct {
	Limit       int
	Offset      int
	RecordingID *uuid.UUID
	SubjectID   *uuid.UUID
	Status      *domain.NoteStatus
}

// UpdateFieldInput holds validated input for a staff override of a single field.
type UpdateFieldInput struct {
	NoteID   uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	FieldID  uuid.UUID
	Value    *string // JSON-encoded
}

// ── Service methods ───────────────────────────────────────────────────────────

// CreateNote creates a note and enqueues the extraction job.
// Enforces the 3-notes-per-recording cap.
func (s *Service) CreateNote(ctx context.Context, input CreateNoteInput) (*NoteResponse, error) {
	count, err := s.repo.CountNotesByRecording(ctx, input.RecordingID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.CreateNote: count: %w", err)
	}
	if count >= maxNotesPerRecording {
		return nil, fmt.Errorf("notes.service.CreateNote: max %d notes per recording: %w",
			maxNotesPerRecording, domain.ErrConflict)
	}

	noteID := domain.NewID()
	note, err := s.repo.CreateNote(ctx, CreateNoteParams{
		ID:            noteID,
		ClinicID:      input.ClinicID,
		RecordingID:   input.RecordingID,
		FormVersionID: input.FormVersionID,
		SubjectID:     input.SubjectID,
		CreatedBy:     input.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.CreateNote: %w", err)
	}

	// Enqueue extraction job. If enqueue fails the note stays in 'extracting'
	// and can be retried by the caller or a background sweep.
	if _, err := s.enqueue.Insert(ctx, ExtractNoteArgs{NoteID: noteID}, nil); err != nil {
		return nil, fmt.Errorf("notes.service.CreateNote: enqueue: %w", err)
	}

	return toNoteResponse(note, nil), nil
}

// GetNote fetches a note with its current field values.
func (s *Service) GetNote(ctx context.Context, id, clinicID uuid.UUID) (*NoteResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.GetNote: %w", err)
	}

	fields, err := s.repo.GetNoteFields(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("notes.service.GetNote: fields: %w", err)
	}

	return toNoteResponse(note, fields), nil
}

// ListNotes returns a paginated list of notes for a clinic.
func (s *Service) ListNotes(ctx context.Context, clinicID uuid.UUID, input ListNotesInput) (*NoteListResponse, error) {
	input.Limit = clampLimit(input.Limit)

	notes, total, err := s.repo.ListNotes(ctx, clinicID, ListNotesParams(input))
	if err != nil {
		return nil, fmt.Errorf("notes.service.ListNotes: %w", err)
	}

	items := make([]*NoteResponse, len(notes))
	for i, n := range notes {
		items[i] = toNoteResponse(n, nil)
	}

	return &NoteListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// UpdateField records a staff override for a single note field.
// Only allowed when the note is in 'draft' status.
func (s *Service) UpdateField(ctx context.Context, input UpdateFieldInput) (*NoteFieldResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, input.NoteID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.UpdateField: %w", err)
	}
	if note.Status != domain.NoteStatusDraft {
		return nil, fmt.Errorf("notes.service.UpdateField: note not in draft: %w", domain.ErrConflict)
	}

	f, err := s.repo.UpdateNoteField(ctx, UpdateNoteFieldParams{
		NoteID:       input.NoteID,
		FieldID:      input.FieldID,
		ClinicID:     input.ClinicID,
		Value:        input.Value,
		OverriddenBy: input.StaffID,
		OverriddenAt: domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.UpdateField: %w", err)
	}

	return toFieldResponse(f), nil
}

// SubmitNote transitions a note from draft → submitted.
func (s *Service) SubmitNote(ctx context.Context, noteID, clinicID, staffID uuid.UUID) (*NoteResponse, error) {
	note, err := s.repo.SubmitNote(ctx, SubmitNoteParams{
		ID:          noteID,
		ClinicID:    clinicID,
		SubmittedBy: staffID,
		SubmittedAt: domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.SubmitNote: %w", err)
	}
	return toNoteResponse(note, nil), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func toNoteResponse(n *NoteRecord, fields []*NoteFieldRecord) *NoteResponse {
	r := &NoteResponse{
		ID:            n.ID.String(),
		ClinicID:      n.ClinicID.String(),
		RecordingID:   n.RecordingID.String(),
		FormVersionID: n.FormVersionID.String(),
		CreatedBy:     n.CreatedBy.String(),
		Status:        n.Status,
		ErrorMessage:  n.ErrorMessage,
		CreatedAt:     n.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     n.UpdatedAt.Format(time.RFC3339),
	}
	if n.SubjectID != nil {
		s := n.SubjectID.String()
		r.SubjectID = &s
	}
	if n.SubmittedAt != nil {
		s := n.SubmittedAt.Format(time.RFC3339)
		r.SubmittedAt = &s
	}
	if n.SubmittedBy != nil {
		s := n.SubmittedBy.String()
		r.SubmittedBy = &s
	}
	if fields != nil {
		r.Fields = make([]*NoteFieldResponse, len(fields))
		for i, f := range fields {
			r.Fields[i] = toFieldResponse(f)
		}
	}
	return r
}

func toFieldResponse(f *NoteFieldRecord) *NoteFieldResponse {
	r := &NoteFieldResponse{
		FieldID:     f.FieldID.String(),
		Value:       f.Value,
		Confidence:  f.Confidence,
		SourceQuote: f.SourceQuote,
	}
	if f.OverriddenBy != nil {
		s := f.OverriddenBy.String()
		r.OverriddenBy = &s
	}
	if f.OverriddenAt != nil {
		s := f.OverriddenAt.Format(time.RFC3339)
		r.OverriddenAt = &s
	}
	return r
}
