package incidents

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

// Handler wires incidents HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared ───────────────────────────────────────────────────────────────────

type incidentIDPath struct {
	ID string `path:"id" doc:"The incident UUID."`
}

type incidentPagination struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"200" default:"50"`
	Offset int `query:"offset" minimum:"0" default:"0"`
}

type incidentHTTPResponse struct {
	Body *IncidentResponse
}

type incidentListHTTPResponse struct {
	Body *IncidentListResponse
}

type emptyHTTPResponse struct{}

// ── Create / list / get / update ─────────────────────────────────────────────

type createIncidentBody struct {
	Body struct {
		SubjectID        string  `json:"subject_id"        minLength:"36"`
		NoteID           *string `json:"note_id,omitempty"`
		IncidentType     string  `json:"incident_type"     enum:"fall,medication_error,restraint,behaviour,skin_injury,unexplained_injury,pressure_injury,unauthorised_absence,death,complaint,sexual_misconduct,neglect,psychological_abuse,physical_abuse,financial_abuse,other"`
		Severity         string  `json:"severity"          enum:"low,medium,high,critical"`
		OccurredAt       string  `json:"occurred_at"       doc:"RFC3339"`
		Location         *string `json:"location,omitempty"`
		BriefDescription string  `json:"brief_description" minLength:"1" maxLength:"2000"`
		ImmediateActions *string `json:"immediate_actions,omitempty"`
		WitnessesText    *string `json:"witnesses_text,omitempty"`
		SubjectOutcome   *string `json:"subject_outcome,omitempty" enum:"no_harm,minor_injury,moderate_injury,hospitalised,deceased,complaint_resolved,unknown"`
	}
}

func (h *Handler) createIncident(ctx context.Context, input *createIncidentBody) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.Body.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	occurred, err := time.Parse(time.RFC3339, input.Body.OccurredAt)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid occurred_at (RFC3339)")
	}
	in := CreateIncidentInput{
		ClinicID:         clinicID,
		StaffID:          staffID,
		SubjectID:        subjectID,
		IncidentType:     input.Body.IncidentType,
		Severity:         input.Body.Severity,
		OccurredAt:       occurred,
		Location:         input.Body.Location,
		BriefDescription: input.Body.BriefDescription,
		ImmediateActions: input.Body.ImmediateActions,
		WitnessesText:    input.Body.WitnessesText,
		SubjectOutcome:   input.Body.SubjectOutcome,
	}
	if input.Body.NoteID != nil && *input.Body.NoteID != "" {
		id, err := uuid.Parse(*input.Body.NoteID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid note_id")
		}
		in.NoteID = &id
	}
	resp, err := h.svc.CreateIncident(ctx, in)
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

type listIncidentsQuery struct {
	incidentPagination
	SubjectID string `query:"subject_id"`
	Status    string `query:"status"`
	Type      string `query:"type"`
	Since     string `query:"since" doc:"RFC3339"`
	Until     string `query:"until" doc:"RFC3339"`
	OnlyOpen  bool   `query:"only_open"`
}

func (h *Handler) listIncidents(ctx context.Context, input *listIncidentsQuery) (*incidentListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	p := ListIncidentsParams{
		Limit:    input.Limit,
		Offset:   input.Offset,
		OnlyOpen: input.OnlyOpen,
	}
	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		p.SubjectID = &id
	}
	if input.Status != "" {
		p.Status = &input.Status
	}
	if input.Type != "" {
		p.Type = &input.Type
	}
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid since")
		}
		p.Since = &t
	}
	if input.Until != "" {
		t, err := time.Parse(time.RFC3339, input.Until)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid until")
		}
		p.Until = &t
	}
	resp, err := h.svc.ListIncidents(ctx, clinicID, staffID, p)
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentListHTTPResponse{Body: resp}, nil
}

func (h *Handler) getIncident(ctx context.Context, input *incidentIDPath) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetIncident(ctx, id, clinicID, staffID)
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

type updateIncidentBody struct {
	ID   string `path:"id"`
	Body struct {
		Severity              *string `json:"severity,omitempty"               enum:"low,medium,high,critical"`
		Location              *string `json:"location,omitempty"`
		BriefDescription      *string `json:"brief_description,omitempty"`
		ImmediateActions      *string `json:"immediate_actions,omitempty"`
		WitnessesText         *string `json:"witnesses_text,omitempty"`
		SubjectOutcome        *string `json:"subject_outcome,omitempty"        enum:"no_harm,minor_injury,moderate_injury,hospitalised,deceased,complaint_resolved,unknown"`
		Status                *string `json:"status,omitempty"                 enum:"open,investigating,closed,escalated,reported_to_regulator"`
		NOKNotifiedAt         *string `json:"nok_notified_at,omitempty"        doc:"RFC3339"`
		GPNotifiedAt          *string `json:"gp_notified_at,omitempty"         doc:"RFC3339"`
		PreventivePlanSummary *string `json:"preventive_plan_summary,omitempty"`
		CarePlanUpdatedAt     *string `json:"care_plan_updated_at,omitempty"   doc:"RFC3339"`
		Reviewed              bool    `json:"reviewed,omitempty"               doc:"Stamp reviewed_by/reviewed_at to caller + now."`
	}
}

func (h *Handler) updateIncident(ctx context.Context, input *updateIncidentBody) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}

	in := UpdateIncidentInput{
		ID:                    id,
		ClinicID:              clinicID,
		StaffID:               staffID,
		Severity:              input.Body.Severity,
		Location:              input.Body.Location,
		BriefDescription:      input.Body.BriefDescription,
		ImmediateActions:      input.Body.ImmediateActions,
		WitnessesText:         input.Body.WitnessesText,
		SubjectOutcome:        input.Body.SubjectOutcome,
		Status:                input.Body.Status,
		PreventivePlanSummary: input.Body.PreventivePlanSummary,
		Reviewed:              input.Body.Reviewed,
	}
	if input.Body.NOKNotifiedAt != nil && *input.Body.NOKNotifiedAt != "" {
		t, err := time.Parse(time.RFC3339, *input.Body.NOKNotifiedAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid nok_notified_at")
		}
		in.NOKNotifiedAt = &t
	}
	if input.Body.GPNotifiedAt != nil && *input.Body.GPNotifiedAt != "" {
		t, err := time.Parse(time.RFC3339, *input.Body.GPNotifiedAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid gp_notified_at")
		}
		in.GPNotifiedAt = &t
	}
	if input.Body.CarePlanUpdatedAt != nil && *input.Body.CarePlanUpdatedAt != "" {
		t, err := time.Parse(time.RFC3339, *input.Body.CarePlanUpdatedAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid care_plan_updated_at")
		}
		in.CarePlanUpdatedAt = &t
	}
	resp, err := h.svc.UpdateIncident(ctx, in)
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

// ── Escalate / notify regulator ──────────────────────────────────────────────

type escalateIncidentBody struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason" minLength:"1" maxLength:"2000" doc:"Why escalation is necessary — required for audit defensibility."`
	}
}

func (h *Handler) escalateIncident(ctx context.Context, input *escalateIncidentBody) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.EscalateIncident(ctx, EscalateInput{
		ID:       id,
		ClinicID: clinicID,
		StaffID:  staffID,
		Reason:   input.Body.Reason,
	})
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

type notifyRegulatorBody struct {
	ID   string `path:"id"`
	Body struct {
		ReferenceNumber *string `json:"reference_number,omitempty" doc:"SIRS / CQC reference id assigned externally."`
		NotifiedAt      *string `json:"notified_at,omitempty"      doc:"RFC3339; defaults to now."`
	}
}

func (h *Handler) notifyRegulator(ctx context.Context, input *notifyRegulatorBody) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	in := NotifyRegulatorInput{
		ID:              id,
		ClinicID:        clinicID,
		StaffID:         staffID,
		ReferenceNumber: input.Body.ReferenceNumber,
	}
	if input.Body.NotifiedAt != nil && *input.Body.NotifiedAt != "" {
		t, err := time.Parse(time.RFC3339, *input.Body.NotifiedAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid notified_at")
		}
		in.NotifiedAt = &t
	}
	resp, err := h.svc.NotifyRegulator(ctx, in)
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

// ── Witnesses ────────────────────────────────────────────────────────────────

type witnessIDsPath struct {
	ID      string `path:"id"`
	StaffID string `path:"staff_id"`
}

type addWitnessBody struct {
	ID   string `path:"id"`
	Body struct {
		StaffID string `json:"staff_id" minLength:"36"`
	}
}

func (h *Handler) addWitness(ctx context.Context, input *addWitnessBody) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	staffID, err := uuid.Parse(input.Body.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}
	resp, err := h.svc.AddWitness(ctx, id, clinicID, staffID)
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

func (h *Handler) removeWitness(ctx context.Context, input *witnessIDsPath) (*emptyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}
	if _, err := h.svc.RemoveWitness(ctx, id, clinicID, staffID); err != nil {
		return nil, mapIncidentError(err)
	}
	return &emptyHTTPResponse{}, nil
}

// ── Addendums ────────────────────────────────────────────────────────────────

type addAddendumBody struct {
	ID   string `path:"id"`
	Body struct {
		Text string `json:"text" minLength:"1" maxLength:"4000"`
	}
}

func (h *Handler) addAddendum(ctx context.Context, input *addAddendumBody) (*incidentHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.AddAddendum(ctx, AddAddendumInput{
		IncidentID: id,
		ClinicID:   clinicID,
		StaffID:    staffID,
		Text:       input.Body.Text,
	})
	if err != nil {
		return nil, mapIncidentError(err)
	}
	return &incidentHTTPResponse{Body: resp}, nil
}

// ── Error mapping ────────────────────────────────────────────────────────────

func mapIncidentError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		slog.Warn("incidents: not found", "error", err.Error())
		return huma.Error404NotFound("incident not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		slog.Error("incidents: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

// Compile-time anchor — ensures http import stays alongside huma imports
// even if all the chi-method constants get inlined.
var _ = http.MethodPost
