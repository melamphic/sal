package notes

import (
	"context"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// repo is the internal data-access interface for the notes module.
// The concrete implementation is in repository.go; tests use fakeRepo.
type repo interface {
	CreateNote(ctx context.Context, p CreateNoteParams) (*NoteRecord, error)
	GetNoteByID(ctx context.Context, id, clinicID uuid.UUID) (*NoteRecord, error)
	ListNotes(ctx context.Context, clinicID uuid.UUID, p ListNotesParams) ([]*NoteRecord, int, error)
	UpdateNoteStatus(ctx context.Context, id uuid.UUID, status domain.NoteStatus, errMsg *string) (*NoteRecord, error)
	SubmitNote(ctx context.Context, p SubmitNoteParams) (*NoteRecord, error)
	ArchiveNote(ctx context.Context, p ArchiveNoteParams) (*NoteRecord, error)
	// CountNotesByRecording returns how many notes exist for a recording within a clinic.
	// Used to enforce the 3-note-per-recording cap at service layer.
	CountNotesByRecording(ctx context.Context, clinicID, recordingID uuid.UUID) (int, error)

	// UpdatePolicyAlignment persists the computed alignment score on a note.
	UpdatePolicyAlignment(ctx context.Context, noteID, clinicID uuid.UUID, pct float64) error
	// UpdatePolicyCheckResult persists per-clause check results as JSONB on a note.
	UpdatePolicyCheckResult(ctx context.Context, noteID, clinicID uuid.UUID, resultJSON string) error
	// UpdatePDFKey sets the pdf_storage_key on a note after PDF generation.
	UpdatePDFKey(ctx context.Context, noteID, clinicID uuid.UUID, key string) error

	// Note fields.
	UpsertNoteFields(ctx context.Context, noteID uuid.UUID, fields []UpsertFieldParams) ([]*NoteFieldRecord, error)
	GetNoteFields(ctx context.Context, noteID uuid.UUID) ([]*NoteFieldRecord, error)
	UpdateNoteField(ctx context.Context, p UpdateNoteFieldParams) (*NoteFieldRecord, error)
}
