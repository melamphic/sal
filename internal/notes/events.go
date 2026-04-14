package notes

import (
	"context"

	"github.com/google/uuid"
)

// NoteEventType categorises what happened to a note.
type NoteEventType string

const (
	NoteEventCreated            NoteEventType = "note.created"
	NoteEventFieldChanged       NoteEventType = "note.field_changed"
	NoteEventSubmitted          NoteEventType = "note.submitted"
	NoteEventArchived           NoteEventType = "note.archived"
	NoteEventExtractionComplete NoteEventType = "note.extraction_complete"
	NoteEventExtractionFailed   NoteEventType = "note.extraction_failed"
)

// NoteEvent carries data about a single note lifecycle transition.
type NoteEvent struct {
	NoteID    uuid.UUID
	SubjectID *uuid.UUID
	ClinicID  uuid.UUID
	EventType NoteEventType
	FieldID   *uuid.UUID
	OldValue  *string
	NewValue  *string
	Reason    *string
	ActorID   uuid.UUID
	ActorRole string
}

// EventEmitter receives note lifecycle events.
// Implementations must not block the caller; errors are logged internally.
type EventEmitter interface {
	Emit(ctx context.Context, e NoteEvent)
}

// noopEmitter discards all events. Used when no emitter is wired.
type noopEmitter struct{}

func (noopEmitter) Emit(_ context.Context, _ NoteEvent) {}
