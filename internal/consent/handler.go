package consent

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

type consentIDPath struct {
	ID string `path:"id" doc:"Consent record UUID."`
}

type consentPagination struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"200" default:"50"`
	Offset int `query:"offset" minimum:"0" default:"0"`
}

type consentHTTPResponse struct {
	Body *ConsentResponse
}

type consentListHTTPResponse struct {
	Body *ConsentListResponse
}

// ── Capture / list / get ─────────────────────────────────────────────────────

type captureConsentBody struct {
	Body struct {
		SubjectID                   string  `json:"subject_id"   minLength:"36"`
		NoteID                      *string `json:"note_id,omitempty"`
		ConsentType                 string  `json:"consent_type" enum:"audio_recording,ai_processing,telemedicine,sedation,euthanasia,invasive_procedure,mhr_write,photography,data_sharing,controlled_drug_administration,treatment_plan,other"`
		Scope                       string  `json:"scope"        minLength:"1" maxLength:"500"`
		ProcedureOrFormID           *string `json:"procedure_or_form_id,omitempty"`
		RisksDiscussed              *string `json:"risks_discussed,omitempty"`
		AlternativesDiscussed       *string `json:"alternatives_discussed,omitempty"`
		CapturedVia                 string  `json:"captured_via" enum:"verbal_clinic,verbal_telehealth,written_signature,electronic_signature,guardian"`
		SignatureImageKey           *string `json:"signature_image_key,omitempty"`
		TranscriptRecordingID       *string `json:"transcript_recording_id,omitempty"`
		ConsentingPartyRelationship *string `json:"consenting_party_relationship,omitempty" enum:"self,owner,guardian,epoa,nok,authorised_representative,other"`
		ConsentingPartyName         *string `json:"consenting_party_name,omitempty"`
		CapacityAssessmentID        *string `json:"capacity_assessment_id,omitempty"`
		WitnessID                   *string `json:"witness_id,omitempty"`
		CapturedAt                  *string `json:"captured_at,omitempty"   doc:"RFC3339; defaults to now."`
		ExpiresAt                   *string `json:"expires_at,omitempty"    doc:"RFC3339; default applied per consent_type."`
		RenewalDueAt                *string `json:"renewal_due_at,omitempty" doc:"RFC3339"`
	}
}

func (h *Handler) captureConsent(ctx context.Context, input *captureConsentBody) (*consentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.Body.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	in := CaptureConsentInput{
		ClinicID:                    clinicID,
		StaffID:                     staffID,
		SubjectID:                   subjectID,
		ConsentType:                 input.Body.ConsentType,
		Scope:                       input.Body.Scope,
		RisksDiscussed:              input.Body.RisksDiscussed,
		AlternativesDiscussed:       input.Body.AlternativesDiscussed,
		CapturedVia:                 input.Body.CapturedVia,
		SignatureImageKey:           input.Body.SignatureImageKey,
		ConsentingPartyRelationship: input.Body.ConsentingPartyRelationship,
		ConsentingPartyName:         input.Body.ConsentingPartyName,
	}
	if id, err := optUUID(input.Body.NoteID, "note_id"); err != nil {
		return nil, err
	} else {
		in.NoteID = id
	}
	if id, err := optUUID(input.Body.ProcedureOrFormID, "procedure_or_form_id"); err != nil {
		return nil, err
	} else {
		in.ProcedureOrFormID = id
	}
	if id, err := optUUID(input.Body.TranscriptRecordingID, "transcript_recording_id"); err != nil {
		return nil, err
	} else {
		in.TranscriptRecordingID = id
	}
	if id, err := optUUID(input.Body.CapacityAssessmentID, "capacity_assessment_id"); err != nil {
		return nil, err
	} else {
		in.CapacityAssessmentID = id
	}
	if id, err := optUUID(input.Body.WitnessID, "witness_id"); err != nil {
		return nil, err
	} else {
		in.WitnessID = id
	}
	if t, err := optTime(input.Body.CapturedAt, "captured_at"); err != nil {
		return nil, err
	} else if t != nil {
		in.CapturedAt = *t
	}
	if t, err := optTime(input.Body.ExpiresAt, "expires_at"); err != nil {
		return nil, err
	} else {
		in.ExpiresAt = t
	}
	if t, err := optTime(input.Body.RenewalDueAt, "renewal_due_at"); err != nil {
		return nil, err
	} else {
		in.RenewalDueAt = t
	}

	resp, err := h.svc.CaptureConsent(ctx, in)
	if err != nil {
		return nil, mapConsentError(err)
	}
	return &consentHTTPResponse{Body: resp}, nil
}

type listConsentQuery struct {
	consentPagination
	SubjectID      string `query:"subject_id"`
	ConsentType    string `query:"consent_type"`
	OnlyActive     bool   `query:"only_active"`
	ExpiringWithin string `query:"expiring_within" doc:"Go duration (e.g. '720h' for 30 days)."`
}

func (h *Handler) listConsents(ctx context.Context, input *listConsentQuery) (*consentListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	p := ListConsentParams{
		Limit:      input.Limit,
		Offset:     input.Offset,
		OnlyActive: input.OnlyActive,
	}
	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		p.SubjectID = &id
	}
	if input.ConsentType != "" {
		p.ConsentType = &input.ConsentType
	}
	if input.ExpiringWithin != "" {
		d, err := time.ParseDuration(input.ExpiringWithin)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid expiring_within (use Go duration: 720h, 168h, …)")
		}
		p.ExpiringWithin = &d
	}
	resp, err := h.svc.ListConsents(ctx, clinicID, staffID, p)
	if err != nil {
		return nil, mapConsentError(err)
	}
	return &consentListHTTPResponse{Body: resp}, nil
}

func (h *Handler) getConsent(ctx context.Context, input *consentIDPath) (*consentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetConsent(ctx, id, clinicID, staffID)
	if err != nil {
		return nil, mapConsentError(err)
	}
	return &consentHTTPResponse{Body: resp}, nil
}

type updateConsentBody struct {
	ID   string `path:"id"`
	Body struct {
		RisksDiscussed        *string `json:"risks_discussed,omitempty"`
		AlternativesDiscussed *string `json:"alternatives_discussed,omitempty"`
		ExpiresAt             *string `json:"expires_at,omitempty"`
		RenewalDueAt          *string `json:"renewal_due_at,omitempty"`
		SignatureImageKey     *string `json:"signature_image_key,omitempty"`
		WitnessID             *string `json:"witness_id,omitempty"`
	}
}

func (h *Handler) updateConsent(ctx context.Context, input *updateConsentBody) (*consentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	in := UpdateConsentInput{
		ID:                    id,
		ClinicID:              clinicID,
		StaffID:               staffID,
		RisksDiscussed:        input.Body.RisksDiscussed,
		AlternativesDiscussed: input.Body.AlternativesDiscussed,
		SignatureImageKey:     input.Body.SignatureImageKey,
	}
	if t, err := optTime(input.Body.ExpiresAt, "expires_at"); err != nil {
		return nil, err
	} else {
		in.ExpiresAt = t
	}
	if t, err := optTime(input.Body.RenewalDueAt, "renewal_due_at"); err != nil {
		return nil, err
	} else {
		in.RenewalDueAt = t
	}
	if u, err := optUUID(input.Body.WitnessID, "witness_id"); err != nil {
		return nil, err
	} else {
		in.WitnessID = u
	}
	resp, err := h.svc.UpdateConsent(ctx, in)
	if err != nil {
		return nil, mapConsentError(err)
	}
	return &consentHTTPResponse{Body: resp}, nil
}

type withdrawConsentBody struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason" minLength:"1" maxLength:"2000"`
	}
}

func (h *Handler) withdrawConsent(ctx context.Context, input *withdrawConsentBody) (*consentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.WithdrawConsent(ctx, WithdrawConsentInput{
		ID:       id,
		ClinicID: clinicID,
		StaffID:  staffID,
		Reason:   input.Body.Reason,
	})
	if err != nil {
		return nil, mapConsentError(err)
	}
	return &consentHTTPResponse{Body: resp}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func optUUID(s *string, label string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid " + label)
	}
	return &id, nil
}

func optTime(s *string, label string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid " + label + " (use RFC3339)")
	}
	return &t, nil
}

func mapConsentError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		slog.Warn("consent: not found", "error", err.Error())
		return huma.Error404NotFound("consent record not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		slog.Error("consent: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

var _ = http.MethodPost
