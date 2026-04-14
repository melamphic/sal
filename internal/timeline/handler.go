package timeline

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires timeline HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new timeline Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared types ──────────────────────────────────────────────────────────────

type timelinePaginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Events per page."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Events to skip."`
}

type timelineHTTPResponse struct {
	Body *TimelineResponse
}

// ── Handlers ──────────────────────────────────────────────────────────────────

type noteTimelineInput struct {
	timelinePaginationInput
	NoteID string `path:"note_id" doc:"Note UUID."`
}

// getNoteTimeline handles GET /api/v1/notes/{note_id}/timeline.
func (h *Handler) getNoteTimeline(ctx context.Context, input *noteTimelineInput) (*timelineHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.GetNoteTimeline(ctx, noteID, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapTimelineError(err)
	}
	return &timelineHTTPResponse{Body: resp}, nil
}

type subjectTimelineInput struct {
	timelinePaginationInput
	SubjectID string `path:"subject_id" doc:"Subject UUID."`
}

// getSubjectTimeline handles GET /api/v1/subjects/{subject_id}/timeline.
func (h *Handler) getSubjectTimeline(ctx context.Context, input *subjectTimelineInput) (*timelineHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	resp, err := h.svc.GetSubjectTimeline(ctx, subjectID, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapTimelineError(err)
	}
	return &timelineHTTPResponse{Body: resp}, nil
}

type clinicAuditInput struct {
	timelinePaginationInput
}

// getClinicAuditLog handles GET /api/v1/timeline.
func (h *Handler) getClinicAuditLog(ctx context.Context, input *clinicAuditInput) (*timelineHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	perms := mw.PermissionsFromContext(ctx)
	if !perms.GenerateAuditExport {
		return nil, huma.Error403Forbidden("generate_audit_export permission required")
	}

	resp, err := h.svc.GetClinicAuditLog(ctx, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapTimelineError(err)
	}
	return &timelineHTTPResponse{Body: resp}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapTimelineError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
