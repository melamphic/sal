package pain

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

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

type painIDPath struct {
	ID string `path:"id" doc:"Pain score UUID."`
}

type painPagination struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"200" default:"50"`
	Offset int `query:"offset" minimum:"0" default:"0"`
}

type painHTTPResponse struct {
	Body *PainScoreResponse
}

type painListHTTPResponse struct {
	Body *PainScoreListResponse
}

type subjectTrendHTTPResponse struct {
	Body *SubjectTrendResponse
}

// ── Record / list / get ──────────────────────────────────────────────────────

type recordPainBody struct {
	Body struct {
		SubjectID     string  `json:"subject_id"      minLength:"36"`
		NoteID        *string `json:"note_id,omitempty"`
		Score         int     `json:"score"           minimum:"0" maximum:"10"`
		Note          *string `json:"note,omitempty"`
		Method        string  `json:"method"          enum:"manual,painchek,extracted_from_audio,flacc_observed,wong_baker"`
		PainScaleUsed string  `json:"pain_scale_used" enum:"nrs,flacc,painad,wong_baker,vrs,vas"`
		AssessedAt    *string `json:"assessed_at,omitempty" doc:"RFC3339; defaults to now."`

		// 4-mode witness shape — same widget/payload as drugs/consent/
		// incidents. Optional for routine pain observations; the
		// service enforces shape rules when WitnessKind is set.
		WitnessKind         *string `json:"witness_kind,omitempty" enum:"staff,pending,external,self"`
		WitnessID           *string `json:"witness_id,omitempty"`
		ExternalWitnessName *string `json:"external_witness_name,omitempty"`
		ExternalWitnessRole *string `json:"external_witness_role,omitempty"`
		WitnessAttestation  *string `json:"witness_attestation,omitempty"`

		// SubmitForReview queues the score for second-rater verification.
		// Used for PainAD (aged-care non-verbal) and high-score entries.
		SubmitForReview bool    `json:"submit_for_review,omitempty"`
		ReviewNote      *string `json:"review_note,omitempty" doc:"Optional context for the approver."`
	}
}

func (h *Handler) recordPain(ctx context.Context, input *recordPainBody) (*painHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.Body.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	in := RecordPainScoreInput{
		ClinicID:            clinicID,
		StaffID:             staffID,
		StaffRole:           string(mw.RoleFromContext(ctx)),
		SubjectID:           subjectID,
		Score:               input.Body.Score,
		Note:                input.Body.Note,
		Method:              input.Body.Method,
		PainScaleUsed:       input.Body.PainScaleUsed,
		WitnessKind:         input.Body.WitnessKind,
		ExternalWitnessName: input.Body.ExternalWitnessName,
		ExternalWitnessRole: input.Body.ExternalWitnessRole,
		WitnessAttestation:  input.Body.WitnessAttestation,
		SubmitForReview:     input.Body.SubmitForReview,
		ReviewNote:          input.Body.ReviewNote,
	}
	if input.Body.NoteID != nil && *input.Body.NoteID != "" {
		id, err := uuid.Parse(*input.Body.NoteID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid note_id")
		}
		in.NoteID = &id
	}
	if input.Body.WitnessID != nil && *input.Body.WitnessID != "" {
		id, err := uuid.Parse(*input.Body.WitnessID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid witness_id")
		}
		in.WitnessID = &id
	}
	if input.Body.AssessedAt != nil && *input.Body.AssessedAt != "" {
		t, err := time.Parse(time.RFC3339, *input.Body.AssessedAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid assessed_at (RFC3339)")
		}
		in.AssessedAt = t
	}

	resp, err := h.svc.RecordPainScore(ctx, in)
	if err != nil {
		return nil, mapPainError(err)
	}
	return &painHTTPResponse{Body: resp}, nil
}

type listPainQuery struct {
	painPagination
	SubjectID string `query:"subject_id"`
	Since     string `query:"since" doc:"RFC3339"`
	Until     string `query:"until" doc:"RFC3339"`
}

func (h *Handler) listPain(ctx context.Context, input *listPainQuery) (*painListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	in := ListPainScoresInput{
		Limit:  input.Limit,
		Offset: input.Offset,
	}
	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		in.SubjectID = &id
	}
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid since")
		}
		in.Since = &t
	}
	if input.Until != "" {
		t, err := time.Parse(time.RFC3339, input.Until)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid until")
		}
		in.Until = &t
	}
	resp, err := h.svc.ListPainScores(ctx, clinicID, staffID, in)
	if err != nil {
		return nil, mapPainError(err)
	}
	return &painListHTTPResponse{Body: resp}, nil
}

func (h *Handler) getPain(ctx context.Context, input *painIDPath) (*painHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetPainScore(ctx, id, clinicID, staffID)
	if err != nil {
		return nil, mapPainError(err)
	}
	return &painHTTPResponse{Body: resp}, nil
}

// ── Subject trend ────────────────────────────────────────────────────────────

type subjectTrendQuery struct {
	SubjectID string `path:"subject_id"`
	Since     string `query:"since" doc:"RFC3339; defaults to 30d ago."`
	Until     string `query:"until" doc:"RFC3339; defaults to now."`
}

func (h *Handler) subjectTrend(ctx context.Context, input *subjectTrendQuery) (*subjectTrendHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	until := domain.TimeNow()
	since := until.AddDate(0, 0, -30)
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid since")
		}
		since = t
	}
	if input.Until != "" {
		t, err := time.Parse(time.RFC3339, input.Until)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid until")
		}
		until = t
	}
	resp, err := h.svc.SubjectTrend(ctx, clinicID, subjectID, staffID, since, until)
	if err != nil {
		return nil, mapPainError(err)
	}
	return &subjectTrendHTTPResponse{Body: resp}, nil
}

func mapPainError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		slog.Warn("pain: not found", "error", err.Error())
		return huma.Error404NotFound("pain score not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		slog.Error("pain: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

var _ = http.MethodPost
