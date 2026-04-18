package notes

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires notes HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new notes Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared input types ────────────────────────────────────────────────────────

type noteIDInput struct {
	NoteID string `path:"note_id" doc:"The note's UUID."`
}

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Number of results to skip."`
}

// ── Notes ─────────────────────────────────────────────────────────────────────

type createNoteBodyInput struct {
	Body struct {
		RecordingID    *string `json:"recording_id,omitempty"   doc:"UUID of the recording to fill from. Omit for manual notes."`
		FormVersionID  string  `json:"form_version_id"          doc:"UUID of the published form version to use."`
		SubjectID      *string `json:"subject_id,omitempty"     doc:"Optional patient UUID."`
		SkipExtraction bool    `json:"skip_extraction,omitempty" doc:"If true, creates a manual note without AI extraction. recording_id may be omitted."`
	}
}

type noteHTTPResponse struct {
	Body *NoteResponse
}

type noteListHTTPResponse struct {
	Body *NoteListResponse
}

type listNotesInput struct {
	paginationInput
	RecordingID     string `query:"recording_id"     doc:"Filter notes by recording UUID."`
	SubjectID       string `query:"subject_id"       doc:"Filter notes by patient UUID."`
	Status          string `query:"status"           enum:"extracting,draft,submitted,failed" doc:"Filter by status."`
	IncludeArchived bool   `query:"include_archived" doc:"Include archived notes in results. Default false."`
}

// createNote handles POST /api/v1/notes.
func (h *Handler) createNote(ctx context.Context, input *createNoteBodyInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	// recording_id required unless skip_extraction is set.
	if !input.Body.SkipExtraction && input.Body.RecordingID == nil {
		return nil, huma.Error400BadRequest("recording_id is required when skip_extraction is false")
	}

	formVersionID, err := uuid.Parse(input.Body.FormVersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_version_id")
	}

	svcInput := CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		ActorRole:      role,
		FormVersionID:  formVersionID,
		SkipExtraction: input.Body.SkipExtraction,
	}

	if input.Body.RecordingID != nil {
		id, err := uuid.Parse(*input.Body.RecordingID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid recording_id")
		}
		svcInput.RecordingID = &id
	}

	if input.Body.SubjectID != nil {
		id, err := uuid.Parse(*input.Body.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		svcInput.SubjectID = &id
	}

	resp, err := h.svc.CreateNote(ctx, svcInput)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// getNote handles GET /api/v1/notes/{note_id}.
func (h *Handler) getNote(ctx context.Context, input *noteIDInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.GetNote(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// listNotes handles GET /api/v1/notes.
func (h *Handler) listNotes(ctx context.Context, input *listNotesInput) (*noteListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	svcInput := ListNotesInput{
		Limit:           input.Limit,
		Offset:          input.Offset,
		IncludeArchived: input.IncludeArchived,
	}
	if input.RecordingID != "" {
		id, err := uuid.Parse(input.RecordingID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid recording_id")
		}
		svcInput.RecordingID = &id
	}
	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		svcInput.SubjectID = &id
	}
	if input.Status != "" {
		s := domain.NoteStatus(input.Status)
		svcInput.Status = &s
	}

	resp, err := h.svc.ListNotes(ctx, clinicID, svcInput)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteListHTTPResponse{Body: resp}, nil
}

// ── Field update ──────────────────────────────────────────────────────────────

type updateFieldBodyInput struct {
	NoteID  string `path:"note_id"`
	FieldID string `path:"field_id"`
	Body    struct {
		Value *string `json:"value" doc:"JSON-encoded new value. Send null to clear."`
	}
}

type fieldHTTPResponse struct {
	Body *NoteFieldResponse
}

// updateField handles PATCH /api/v1/notes/{note_id}/fields/{field_id}.
func (h *Handler) updateField(ctx context.Context, input *updateFieldBodyInput) (*fieldHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}
	fieldID, err := uuid.Parse(input.FieldID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid field_id")
	}

	resp, err := h.svc.UpdateField(ctx, UpdateFieldInput{
		NoteID:    noteID,
		ClinicID:  clinicID,
		StaffID:   staffID,
		ActorRole: role,
		FieldID:   fieldID,
		Value:     input.Body.Value,
	})
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &fieldHTTPResponse{Body: resp}, nil
}

// ── Submit ────────────────────────────────────────────────────────────────────

// submitNote handles POST /api/v1/notes/{note_id}/submit.
func (h *Handler) submitNote(ctx context.Context, input *noteIDInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.SubmitNote(ctx, noteID, clinicID, staffID, role)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// ── Archive ───────────────────────────────────────────────────────────────────

// archiveNote handles POST /api/v1/notes/{note_id}/archive.
func (h *Handler) archiveNote(ctx context.Context, input *noteIDInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.ArchiveNote(ctx, noteID, clinicID, staffID, role)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapNoteError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
