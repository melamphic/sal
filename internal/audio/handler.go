package audio

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires audio HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new audio Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared input types ────────────────────────────────────────────────────────

type recordingIDInput struct {
	RecordingID string `path:"recording_id" doc:"The recording's UUID."`
}

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Number of results to skip."`
}

// ── Request / response types ──────────────────────────────────────────────────

type createRecordingInput struct {
	Body struct {
		SubjectID   *string `json:"subject_id,omitempty" doc:"UUID of the patient to link. Can be set later via PATCH."`
		ContentType string  `json:"content_type" enum:"audio/mp4,audio/m4a,audio/mpeg,audio/webm,audio/ogg,audio/wav" doc:"MIME type of the audio file the client will upload."`
	}
}

type recordingResponse struct {
	Body *RecordingResponse
}

type createRecordingHTTPResponse struct {
	Body *CreateRecordingResponse
}

type recordingListResponse struct {
	Body *RecordingListResponse
}

type listRecordingsInput struct {
	paginationInput
	SubjectID string `query:"subject_id" doc:"Filter recordings by patient UUID."`
	StaffID   string `query:"staff_id"   doc:"Filter recordings by staff UUID."`
	Status    string `query:"status"     enum:"pending_upload,uploaded,transcribing,transcribed,failed" doc:"Filter by processing status."`
}

type downloadURLHTTPResponse struct {
	Body *DownloadURLResponse
}

type linkSubjectInput struct {
	RecordingID string `path:"recording_id" doc:"The recording's UUID."`
	Body        struct {
		SubjectID string `json:"subject_id" doc:"UUID of the patient to link."`
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// createRecording handles POST /api/v1/recordings.
func (h *Handler) createRecording(ctx context.Context, input *createRecordingInput) (*createRecordingHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	svcInput := CreateRecordingInput{
		ClinicID:    clinicID,
		StaffID:     staffID,
		ContentType: input.Body.ContentType,
	}

	if input.Body.SubjectID != nil {
		id, err := uuid.Parse(*input.Body.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		svcInput.SubjectID = &id
	}

	resp, err := h.svc.CreateRecording(ctx, svcInput)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &createRecordingHTTPResponse{Body: resp}, nil
}

// getRecording handles GET /api/v1/recordings/{recording_id}.
func (h *Handler) getRecording(ctx context.Context, input *recordingIDInput) (*recordingResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	recID, err := uuid.Parse(input.RecordingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid recording_id")
	}

	resp, err := h.svc.GetRecordingByID(ctx, recID, clinicID)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &recordingResponse{Body: resp}, nil
}

// listRecordings handles GET /api/v1/recordings.
func (h *Handler) listRecordings(ctx context.Context, input *listRecordingsInput) (*recordingListResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	svcInput := ListRecordingsInput{
		Limit:  input.Limit,
		Offset: input.Offset,
	}

	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		svcInput.SubjectID = &id
	}
	if input.StaffID != "" {
		id, err := uuid.Parse(input.StaffID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid staff_id")
		}
		svcInput.StaffID = &id
	}
	if input.Status != "" {
		s := domain.RecordingStatus(input.Status)
		svcInput.Status = &s
	}

	resp, err := h.svc.ListRecordings(ctx, clinicID, svcInput)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &recordingListResponse{Body: resp}, nil
}

// confirmUpload handles POST /api/v1/recordings/{recording_id}/confirm-upload.
func (h *Handler) confirmUpload(ctx context.Context, input *recordingIDInput) (*recordingResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	recID, err := uuid.Parse(input.RecordingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid recording_id")
	}

	resp, err := h.svc.ConfirmUpload(ctx, recID, clinicID)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &recordingResponse{Body: resp}, nil
}

// retryTranscription handles POST /api/v1/recordings/{recording_id}/retry-transcription.
func (h *Handler) retryTranscription(ctx context.Context, input *recordingIDInput) (*recordingResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	recID, err := uuid.Parse(input.RecordingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid recording_id")
	}

	if err := h.svc.RetryTranscription(ctx, recID, clinicID); err != nil {
		return nil, mapAudioError(err)
	}
	resp, err := h.svc.GetRecordingByID(ctx, recID, clinicID)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &recordingResponse{Body: resp}, nil
}

// getDownloadURL handles GET /api/v1/recordings/{recording_id}/download-url.
func (h *Handler) getDownloadURL(ctx context.Context, input *recordingIDInput) (*downloadURLHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	recID, err := uuid.Parse(input.RecordingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid recording_id")
	}

	resp, err := h.svc.GetDownloadURL(ctx, recID, clinicID)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &downloadURLHTTPResponse{Body: resp}, nil
}

// linkSubject handles PATCH /api/v1/recordings/{recording_id}/subject.
func (h *Handler) linkSubject(ctx context.Context, input *linkSubjectInput) (*recordingResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	recID, err := uuid.Parse(input.RecordingID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid recording_id")
	}

	subjectID, err := uuid.Parse(input.Body.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	resp, err := h.svc.LinkSubject(ctx, recID, clinicID, subjectID)
	if err != nil {
		return nil, mapAudioError(err)
	}
	return &recordingResponse{Body: resp}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapAudioError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("recording already in this state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
