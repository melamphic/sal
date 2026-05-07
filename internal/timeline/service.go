package timeline

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Service handles timeline business logic.
type Service struct {
	repo *Repository
}

// NewService constructs a timeline Service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// ── Response types ────────────────────────────────────────────────────────────

// TimelineEventResponse is the API-safe representation of a note_events row.
//
//nolint:revive
type TimelineEventResponse struct {
	ID         string  `json:"id"`
	NoteID     string  `json:"note_id"`
	SubjectID  *string `json:"subject_id,omitempty"`
	ClinicID   string  `json:"clinic_id"`
	EventType  string  `json:"event_type"`
	FieldID    *string `json:"field_id,omitempty"`
	OldValue   *string `json:"old_value,omitempty"`
	NewValue   *string `json:"new_value,omitempty"`
	ActorID    string  `json:"actor_id"`
	ActorRole  string  `json:"actor_role"`
	Reason     *string `json:"reason,omitempty"`
	OccurredAt string  `json:"occurred_at"`
}

// TimelineResponse is a paginated list of timeline events.
//
//nolint:revive
type TimelineResponse struct {
	Items  []*TimelineEventResponse `json:"items"`
	Total  int                      `json:"total"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
}

// ── Service methods ───────────────────────────────────────────────────────────

// GetNoteTimeline returns paginated events for a single note.
func (s *Service) GetNoteTimeline(ctx context.Context, noteID, clinicID uuid.UUID, limit, offset int) (*TimelineResponse, error) {
	limit = clampLimit(limit)
	events, total, err := s.repo.ListNoteTimeline(ctx, noteID, clinicID, ListParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("timeline.service.GetNoteTimeline: %w", err)
	}
	return toTimelineResponse(events, total, limit, offset), nil
}

// GetSubjectTimeline returns paginated events for a subject.
func (s *Service) GetSubjectTimeline(ctx context.Context, subjectID, clinicID uuid.UUID, limit, offset int) (*TimelineResponse, error) {
	limit = clampLimit(limit)
	events, total, err := s.repo.ListSubjectTimeline(ctx, subjectID, clinicID, ListParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("timeline.service.GetSubjectTimeline: %w", err)
	}
	return toTimelineResponse(events, total, limit, offset), nil
}

// GetStaffActivity returns paginated events authored by a specific
// staff member. Backs the team page's per-staff activity drawer.
// Newest-first ordering — activity feeds want recent-on-top, unlike
// the chronological note/subject timelines.
func (s *Service) GetStaffActivity(ctx context.Context, staffID, clinicID uuid.UUID, limit, offset int) (*TimelineResponse, error) {
	limit = clampLimit(limit)
	events, total, err := s.repo.ListByActor(ctx, staffID, clinicID, ListParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("timeline.service.GetStaffActivity: %w", err)
	}
	return toTimelineResponse(events, total, limit, offset), nil
}

// GetClinicAuditLog returns paginated clinic-wide audit events.
// Requires generate_audit_export permission — enforced at the handler layer.
func (s *Service) GetClinicAuditLog(ctx context.Context, clinicID uuid.UUID, limit, offset int) (*TimelineResponse, error) {
	limit = clampLimit(limit)
	events, total, err := s.repo.ListClinicAuditLog(ctx, clinicID, ListParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("timeline.service.GetClinicAuditLog: %w", err)
	}
	return toTimelineResponse(events, total, limit, offset), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func toTimelineResponse(events []*EventRecord, total, limit, offset int) *TimelineResponse {
	items := make([]*TimelineEventResponse, len(events))
	for i, e := range events {
		items[i] = toEventResponse(e)
	}
	return &TimelineResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}
}

func toEventResponse(e *EventRecord) *TimelineEventResponse {
	r := &TimelineEventResponse{
		ID:         e.ID.String(),
		NoteID:     e.NoteID.String(),
		ClinicID:   e.ClinicID.String(),
		EventType:  e.EventType,
		ActorID:    e.ActorID.String(),
		ActorRole:  e.ActorRole,
		Reason:     e.Reason,
		OccurredAt: e.OccurredAt.Format(time.RFC3339),
		OldValue:   e.OldValue,
		NewValue:   e.NewValue,
	}
	if e.SubjectID != nil {
		s := e.SubjectID.String()
		r.SubjectID = &s
	}
	if e.FieldID != nil {
		s := e.FieldID.String()
		r.FieldID = &s
	}
	return r
}
