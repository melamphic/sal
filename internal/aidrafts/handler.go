package aidrafts

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

type createDraftBody struct {
	Body struct {
		TargetType     string  `json:"target_type"   enum:"incident,consent,pain,pre_encounter_brief"`
		RecordingID    string  `json:"recording_id"  minLength:"36" doc:"UUID of the recording. The transcript drives the AI extraction; create the recording first via /api/v1/recordings."`
		ContextPayload *string `json:"context_payload,omitempty" doc:"Optional JSON string with target-specific context. For consent: {procedure, consent_type, audience}. For incident: empty — the transcript is the entire input."`
	}
}

type draftHTTPResponse struct {
	Body *DraftResponse
}

type idPathInput struct {
	ID string `path:"id"`
}

func (h *Handler) createDraft(ctx context.Context, input *createDraftBody) (*draftHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	recID, err := uuid.Parse(input.Body.RecordingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid recording_id")
	}

	resp, err := h.svc.CreateDraft(ctx, CreateDraftInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		TargetType:     input.Body.TargetType,
		RecordingID:    &recID,
		ContextPayload: input.Body.ContextPayload,
	})
	if err != nil {
		return nil, mapDraftError(err)
	}
	return &draftHTTPResponse{Body: resp}, nil
}

func (h *Handler) getDraft(ctx context.Context, input *idPathInput) (*draftHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetDraft(ctx, id, clinicID)
	if err != nil {
		return nil, mapDraftError(err)
	}
	return &draftHTTPResponse{Body: resp}, nil
}

func mapDraftError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("draft not found")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		slog.Error("aidrafts: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

var _ = http.MethodPost
