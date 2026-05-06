package timeline

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// EventRecord is the raw DB representation of a note_events row.
type EventRecord struct {
	ID         uuid.UUID
	NoteID     uuid.UUID
	SubjectID  *uuid.UUID
	ClinicID   uuid.UUID
	EventType  string
	FieldID    *uuid.UUID
	OldValue   *string
	NewValue   *string
	ActorID    uuid.UUID
	ActorRole  string
	Reason     *string
	OccurredAt time.Time
}

// InsertEventParams holds values for inserting a note_events row.
type InsertEventParams struct {
	ID         uuid.UUID
	NoteID     uuid.UUID
	SubjectID  *uuid.UUID
	ClinicID   uuid.UUID
	EventType  string
	FieldID    *uuid.UUID
	OldValue   *string
	NewValue   *string
	ActorID    uuid.UUID
	ActorRole  string
	Reason     *string
	OccurredAt time.Time
}

// ListParams holds pagination for timeline queries.
type ListParams struct {
	Limit  int
	Offset int
}

// Repository is the PostgreSQL implementation for timeline data.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a timeline Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// InsertNoteEvent persists a single note lifecycle event.
func (r *Repository) InsertNoteEvent(ctx context.Context, p InsertEventParams) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO note_events
			(id, note_id, subject_id, clinic_id, event_type, field_id,
			 old_value, new_value, actor_id, actor_role, reason, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,
		        $7::jsonb,$8::jsonb,$9,$10,$11,$12)`,
		p.ID, p.NoteID, p.SubjectID, p.ClinicID, p.EventType, p.FieldID,
		p.OldValue, p.NewValue, p.ActorID, p.ActorRole, p.Reason, p.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("timeline.repo.InsertNoteEvent: %w", err)
	}
	return nil
}

// ListNoteTimeline returns paginated events for a single note, oldest first.
func (r *Repository) ListNoteTimeline(ctx context.Context, noteID, clinicID uuid.UUID, p ListParams) ([]*EventRecord, int, error) {
	return r.listEvents(ctx, "note_id = $1 AND clinic_id = $2", []any{noteID, clinicID}, p)
}

// ListSubjectTimeline returns paginated events for all notes belonging to a subject.
func (r *Repository) ListSubjectTimeline(ctx context.Context, subjectID, clinicID uuid.UUID, p ListParams) ([]*EventRecord, int, error) {
	return r.listEvents(ctx, "subject_id = $1 AND clinic_id = $2", []any{subjectID, clinicID}, p)
}

// ListClinicAuditLog returns paginated events across the whole clinic (admin use).
func (r *Repository) ListClinicAuditLog(ctx context.Context, clinicID uuid.UUID, p ListParams) ([]*EventRecord, int, error) {
	return r.listEvents(ctx, "clinic_id = $1", []any{clinicID}, p)
}

// ListByActor returns paginated events authored by a specific staff
// member, newest first. Used by the team page's per-staff activity
// drawer ("what did this person do?"). Note: this scans note_events
// and orders DESC; the other list helpers order ASC for chronological
// timeline rendering. Activity feeds want newest-on-top.
func (r *Repository) ListByActor(ctx context.Context, actorID, clinicID uuid.UUID, p ListParams) ([]*EventRecord, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM note_events WHERE actor_id = $1 AND clinic_id = $2`,
		actorID, clinicID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("timeline.repo.ListByActor: count: %w", err)
	}
	q := fmt.Sprintf(`
		SELECT %s FROM note_events
		WHERE actor_id = $1 AND clinic_id = $2
		ORDER BY occurred_at DESC
		LIMIT $3 OFFSET $4`, eventCols)
	rows, err := r.db.Query(ctx, q, actorID, clinicID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("timeline.repo.ListByActor: %w", err)
	}
	defer rows.Close()
	var list []*EventRecord
	for rows.Next() {
		e, sErr := scanEvent(rows)
		if sErr != nil {
			return nil, 0, fmt.Errorf("timeline.repo.ListByActor: %w", sErr)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("timeline.repo.ListByActor: rows: %w", err)
	}
	return list, total, nil
}

const eventCols = `id, note_id, subject_id, clinic_id, event_type, field_id,
	old_value::text, new_value::text, actor_id, actor_role, reason, occurred_at`

func (r *Repository) listEvents(ctx context.Context, where string, args []any, p ListParams) ([]*EventRecord, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM note_events WHERE %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("timeline.repo.listEvents: count: %w", err)
	}

	args = append(args, p.Limit, p.Offset)
	q := fmt.Sprintf(`
		SELECT %s FROM note_events WHERE %s
		ORDER BY occurred_at ASC
		LIMIT $%d OFFSET $%d`, eventCols, where, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("timeline.repo.listEvents: %w", err)
	}
	defer rows.Close()

	var list []*EventRecord
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("timeline.repo.listEvents: %w", err)
		}
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("timeline.repo.listEvents: rows: %w", err)
	}
	return list, total, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanEvent(row scannable) (*EventRecord, error) {
	var e EventRecord
	err := row.Scan(
		&e.ID, &e.NoteID, &e.SubjectID, &e.ClinicID, &e.EventType, &e.FieldID,
		&e.OldValue, &e.NewValue, &e.ActorID, &e.ActorRole, &e.Reason, &e.OccurredAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scanEvent: %w", err)
	}
	return &e, nil
}
