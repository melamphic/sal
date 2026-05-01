package notes

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// ── Shared types ──────────────────────────────────────────────────────────

type materialisedRefHTTPResponse struct {
	Body struct {
		EntityID string `json:"entity_id"`
	}
}

// parsePath turns the raw {note_id, field_id} URL segments into UUIDs.
// Path fields are inlined on each request struct (NOT via an embedded
// helper) — huma's path-tag binding doesn't traverse embedded structs
// reliably across versions, and an empty string parses as "invalid
// UUID", so the inline form is the only safe pattern.
func parsePath(noteIDRaw, fieldIDRaw string) (uuid.UUID, uuid.UUID, error) {
	noteID, err := uuid.Parse(noteIDRaw)
	if err != nil {
		return uuid.Nil, uuid.Nil, huma.Error400BadRequest("invalid note_id")
	}
	fieldID, err := uuid.Parse(fieldIDRaw)
	if err != nil {
		return uuid.Nil, uuid.Nil, huma.Error400BadRequest("invalid field_id")
	}
	return noteID, fieldID, nil
}

func optUUID(s *string, name string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil //nolint:nilnil // optional pointer, not error
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid " + name)
	}
	return &id, nil
}

func optTime(s *string, name string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil //nolint:nilnil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid " + name + " (RFC3339)")
	}
	return &t, nil
}

func refToResponse(ref *MaterialisedRef) *materialisedRefHTTPResponse {
	resp := &materialisedRefHTTPResponse{}
	resp.Body.EntityID = ref.EntityID.String()
	return resp
}

// ── system.consent ────────────────────────────────────────────────────────

type materialiseConsentBody struct {
	NoteID  string `path:"note_id"  doc:"Note UUID."`
	FieldID string `path:"field_id" doc:"Form field UUID — must be a system.* widget."`
	Body    struct {
		ConsentType                 string  `json:"consent_type" enum:"audio_recording,ai_processing,telemedicine,sedation,euthanasia,invasive_procedure,mhr_write,photography,data_sharing,controlled_drug_administration,treatment_plan,other"`
		Scope                       string  `json:"scope" minLength:"1" maxLength:"500"`
		CapturedVia                 string  `json:"captured_via" enum:"verbal_clinic,verbal_telehealth,written_signature,electronic_signature,guardian"`
		RisksDiscussed              *string `json:"risks_discussed,omitempty"`
		AlternativesDiscussed       *string `json:"alternatives_discussed,omitempty"`
		ConsentingPartyName         *string `json:"consenting_party_name,omitempty"`
		ConsentingPartyRelationship *string `json:"consenting_party_relationship,omitempty" enum:"self,owner,guardian,epoa,nok,authorised_representative,other"`
		WitnessID                   *string `json:"witness_id,omitempty"`
		ExpiresAt                   *string `json:"expires_at,omitempty" doc:"RFC3339"`
	}
}

func (h *Handler) materialiseConsent(ctx context.Context, input *materialiseConsentBody) (*materialisedRefHTTPResponse, error) {
	noteID, fieldID, err := parsePath(input.NoteID, input.FieldID)
	if err != nil {
		return nil, err
	}
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	witness, err := optUUID(input.Body.WitnessID, "witness_id")
	if err != nil {
		return nil, err
	}
	expires, err := optTime(input.Body.ExpiresAt, "expires_at")
	if err != nil {
		return nil, err
	}

	in := MaterialiseConsentInput{
		ConsentType:                 input.Body.ConsentType,
		Scope:                       input.Body.Scope,
		CapturedVia:                 input.Body.CapturedVia,
		RisksDiscussed:              input.Body.RisksDiscussed,
		AlternativesDiscussed:       input.Body.AlternativesDiscussed,
		ConsentingPartyName:         input.Body.ConsentingPartyName,
		ConsentingPartyRelationship: input.Body.ConsentingPartyRelationship,
		WitnessID:                   witness,
		ExpiresAt:                   expires,
	}
	ref, err := h.svc.MaterialiseConsent(ctx, noteID, fieldID, clinicID, staffID, in)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return refToResponse(ref), nil
}

// ── system.drug_op ────────────────────────────────────────────────────────

type materialiseDrugOpBody struct {
	NoteID  string `path:"note_id"  doc:"Note UUID."`
	FieldID string `path:"field_id" doc:"Form field UUID — must be a system.* widget."`
	Body    struct {
		ShelfID          string  `json:"shelf_id" minLength:"36" doc:"Shelf entry the drug is being drawn from."`
		Operation        string  `json:"operation" enum:"administer,dispense,discard,receive,transfer,adjust"`
		Quantity         float64 `json:"quantity" minimum:"0"`
		Unit             string  `json:"unit"`
		Dose             *string `json:"dose,omitempty"`
		Route            *string `json:"route,omitempty"`
		ReasonIndication *string `json:"reason_indication,omitempty"`
		WitnessedBy      *string `json:"witnessed_by,omitempty" doc:"Staff UUID. Required for controlled drugs; service re-checks."`
	}
}

func (h *Handler) materialiseDrugOp(ctx context.Context, input *materialiseDrugOpBody) (*materialisedRefHTTPResponse, error) {
	noteID, fieldID, err := parsePath(input.NoteID, input.FieldID)
	if err != nil {
		return nil, err
	}
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	shelfID, err := uuid.Parse(input.Body.ShelfID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid shelf_id")
	}
	witness, err := optUUID(input.Body.WitnessedBy, "witnessed_by")
	if err != nil {
		return nil, err
	}

	in := MaterialiseDrugOpInput{
		ShelfID:          shelfID,
		Operation:        input.Body.Operation,
		Quantity:         input.Body.Quantity,
		Unit:             input.Body.Unit,
		Dose:             input.Body.Dose,
		Route:            input.Body.Route,
		ReasonIndication: input.Body.ReasonIndication,
		WitnessedBy:      witness,
	}
	ref, err := h.svc.MaterialiseDrugOp(ctx, noteID, fieldID, clinicID, staffID, in)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return refToResponse(ref), nil
}

// ── system.incident ───────────────────────────────────────────────────────

type materialiseIncidentBody struct {
	NoteID  string `path:"note_id"  doc:"Note UUID."`
	FieldID string `path:"field_id" doc:"Form field UUID — must be a system.* widget."`
	Body    struct {
		IncidentType     string  `json:"incident_type"`
		Severity         string  `json:"severity" enum:"low,medium,high,critical"`
		OccurredAt       string  `json:"occurred_at" doc:"RFC3339. Defaults to now if empty."`
		Location         *string `json:"location,omitempty"`
		BriefDescription string  `json:"brief_description" minLength:"1"`
		ImmediateActions *string `json:"immediate_actions,omitempty"`
		WitnessesText    *string `json:"witnesses_text,omitempty"`
		SubjectOutcome   *string `json:"subject_outcome,omitempty" enum:"no_harm,minor_injury,moderate_injury,hospitalised,deceased,complaint_resolved,unknown"`
	}
}

func (h *Handler) materialiseIncident(ctx context.Context, input *materialiseIncidentBody) (*materialisedRefHTTPResponse, error) {
	noteID, fieldID, err := parsePath(input.NoteID, input.FieldID)
	if err != nil {
		return nil, err
	}
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	occurredAt := time.Now()
	if input.Body.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, input.Body.OccurredAt)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid occurred_at (RFC3339)")
		}
		occurredAt = t
	}

	in := MaterialiseIncidentInput{
		IncidentType:     input.Body.IncidentType,
		Severity:         input.Body.Severity,
		OccurredAt:       occurredAt,
		Location:         input.Body.Location,
		BriefDescription: input.Body.BriefDescription,
		ImmediateActions: input.Body.ImmediateActions,
		WitnessesText:    input.Body.WitnessesText,
		SubjectOutcome:   input.Body.SubjectOutcome,
	}
	ref, err := h.svc.MaterialiseIncident(ctx, noteID, fieldID, clinicID, staffID, in)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return refToResponse(ref), nil
}

// ── system.pain_score ─────────────────────────────────────────────────────

type materialisePainBody struct {
	NoteID  string `path:"note_id"  doc:"Note UUID."`
	FieldID string `path:"field_id" doc:"Form field UUID — must be a system.* widget."`
	Body    struct {
		Score         int     `json:"score" minimum:"0" maximum:"10"`
		PainScaleUsed string  `json:"pain_scale_used" enum:"nrs,flacc,painad,wong_baker,vrs,vas"`
		Method        string  `json:"method" enum:"manual,painchek,extracted_from_audio,flacc_observed,wong_baker"`
		Note          *string `json:"note,omitempty"`
	}
}

func (h *Handler) materialisePain(ctx context.Context, input *materialisePainBody) (*materialisedRefHTTPResponse, error) {
	noteID, fieldID, err := parsePath(input.NoteID, input.FieldID)
	if err != nil {
		return nil, err
	}
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	in := MaterialisePainInput{
		Score:         input.Body.Score,
		PainScaleUsed: input.Body.PainScaleUsed,
		Method:        input.Body.Method,
		Note:          input.Body.Note,
	}
	ref, err := h.svc.MaterialisePain(ctx, noteID, fieldID, clinicID, staffID, in)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return refToResponse(ref), nil
}
