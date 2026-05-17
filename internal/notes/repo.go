package notes

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// repo is the internal data-access interface for the notes module.
// The concrete implementation is in repository.go; tests use fakeRepo.
type repo interface {
	CreateNote(ctx context.Context, p CreateNoteParams) (*NoteRecord, error)
	GetNoteByID(ctx context.Context, id, clinicID uuid.UUID) (*NoteRecord, error)
	ListNotes(ctx context.Context, clinicID uuid.UUID, p ListNotesParams) ([]*NoteRecord, int, error)
	UpdateNoteStatus(ctx context.Context, id, clinicID uuid.UUID, status domain.NoteStatus, errMsg *string) (*NoteRecord, error)
	SubmitNote(ctx context.Context, p SubmitNoteParams) (*NoteRecord, error)
	OverrideUnlock(ctx context.Context, p OverrideUnlockParams) (*NoteRecord, error)
	ArchiveNote(ctx context.Context, p ArchiveNoteParams) (*NoteRecord, error)
	// CountNotesByRecording returns how many notes exist for a recording within a clinic.
	// Used to enforce the 3-note-per-recording cap at service layer.
	CountNotesByRecording(ctx context.Context, clinicID, recordingID uuid.UUID) (int, error)

	// ListExtractingNoteIDsByRecording — system-internal lookup used by
	// the audio-transcribe listener. Returns notes still in `extracting`
	// status for a recording so they can be re-enqueued for AI extraction
	// the instant the transcript lands.
	ListExtractingNoteIDsByRecording(ctx context.Context, recordingID uuid.UUID) ([]uuid.UUID, error)

	// UpdatePolicyAlignment persists the computed alignment score on a note.
	UpdatePolicyAlignment(ctx context.Context, noteID, clinicID uuid.UUID, pct float64) error
	// UpdatePolicyCheckResult persists per-clause check results as JSONB on a note.
	UpdatePolicyCheckResult(ctx context.Context, noteID, clinicID uuid.UUID, resultJSON string) error
	// UpdatePDFKey sets the pdf_storage_key on a note after PDF generation.
	UpdatePDFKey(ctx context.Context, noteID, clinicID uuid.UUID, key string) error
	// ClearPDFKey nulls pdf_storage_key so the next render produces a
	// fresh artifact. Force-rerender path.
	ClearPDFKey(ctx context.Context, noteID, clinicID uuid.UUID) error

	// Note fields.
	UpsertNoteFields(ctx context.Context, noteID uuid.UUID, fields []UpsertFieldParams) ([]*NoteFieldRecord, error)
	GetNoteFields(ctx context.Context, noteID uuid.UUID) ([]*NoteFieldRecord, error)
	UpdateNoteField(ctx context.Context, p UpdateNoteFieldParams) (*NoteFieldRecord, error)
	// SeedNoteFields inserts a NULL-value row for each (noteID, fieldID)
	// pair, ON CONFLICT DO NOTHING. Called at note creation time so the
	// FE can PATCH any field by id without a 404 for fields the AI
	// produced no value for (or for manual notes where the AI never ran).
	SeedNoteFields(ctx context.Context, noteID uuid.UUID, fieldIDs []uuid.UUID) error

	// System widget materialise support — joins note_fields with
	// form_fields.type + form_fields.title.
	GetNoteFieldWithType(ctx context.Context, noteID, fieldID, clinicID uuid.UUID) (*NoteFieldWithType, error)
	ListSystemFieldStates(ctx context.Context, noteID, clinicID uuid.UUID) ([]NoteFieldWithType, error)
	// WriteMaterialisedPointer updates note_fields.value to the
	// id-pointer JSON without touching overridden_by/at — this is a
	// system action, not a staff override.
	WriteMaterialisedPointer(ctx context.Context, noteID, fieldID, clinicID uuid.UUID, pointer string) error

	// LookupFormNamesByNoteIDs batches a (note → form.name) fetch in a
	// single round-trip. Missing/archived ids are absent. Used by cross-
	// domain feeds (staff activity) to decorate notes with a readable label.
	LookupFormNamesByNoteIDs(ctx context.Context, clinicID uuid.UUID, noteIDs []uuid.UUID) (map[uuid.UUID]string, error)

	// InsertPolicyCheck appends a policy check run to the note_policy_checks table.
	InsertPolicyCheck(ctx context.Context, noteID, clinicID uuid.UUID, resultJSON string) error
	// ListPolicyChecks returns all policy check runs for a note, newest first.
	ListPolicyChecks(ctx context.Context, noteID, clinicID uuid.UUID) ([]PolicyCheckRecord, error)

	// Attachments — photos and documents linked to a note. Storage of the
	// underlying bytes lives in the AttachmentUploader injected at service
	// level; the repo only persists metadata + s3_key pointers.
	InsertAttachment(ctx context.Context, p CreateAttachmentParams) (*NoteAttachmentRecord, error)
	ListAttachmentsByNote(ctx context.Context, noteID, clinicID uuid.UUID) ([]*NoteAttachmentRecord, error)
	GetAttachment(ctx context.Context, id, clinicID uuid.UUID) (*NoteAttachmentRecord, error)
	ArchiveAttachment(ctx context.Context, id, clinicID uuid.UUID) error
}

// PolicyCheckRecord is one row from note_policy_checks.
type PolicyCheckRecord struct {
	ID        uuid.UUID
	NoteID    uuid.UUID
	ClinicID  uuid.UUID
	Result    string    // raw JSONB
	CheckedAt time.Time
}

// NoteFieldWithType is a denormalised join — the row from note_fields +
// the field's type and title from form_fields. Used by the materialise
// flow to validate field type without a second round-trip and by the
// submit gate to surface the field title in error messages. Required
// is read so the gate can let optional system widgets through with
// unconfirmed AI values without blocking submit.
type NoteFieldWithType struct {
	FieldID   uuid.UUID
	FieldType string
	Title     string
	Required  bool
	Value     *string
	NoteID    uuid.UUID
	SubjectID *uuid.UUID
}
