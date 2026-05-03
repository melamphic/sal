package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/forms/schema"
)

// ── Adapter interfaces ─────────────────────────────────────────────────────
//
// Notes module materialises typed compliance entities by calling into
// adapter interfaces implemented by consent / drugs / incidents / pain
// services. Adapter pattern (not direct imports) keeps the dependency
// graph one-way: notes never imports those packages.

// MaterialisedRef is the pointer returned by every materialiser. The
// notes service writes a JSON-encoded form into note_fields.value (see
// IsMaterialisedValue) so the next read knows the field is wired to a
// real ledger row.
type MaterialisedRef struct {
	EntityID uuid.UUID
}

// MaterialiseConsentInput — payload passed to the consent adapter.
type MaterialiseConsentInput struct {
	ClinicID                    uuid.UUID
	StaffID                     uuid.UUID
	SubjectID                   uuid.UUID
	NoteID                      uuid.UUID
	NoteFieldID                 uuid.UUID
	ConsentType                 string
	Scope                       string
	CapturedVia                 string
	RisksDiscussed              *string
	AlternativesDiscussed       *string
	ConsentingPartyName         *string
	ConsentingPartyRelationship *string
	WitnessID                   *uuid.UUID
	ExpiresAt                   *time.Time
}

// MaterialiseDrugOpInput — payload passed to the drugs adapter. The
// shelf_id is required because drug ops bind to inventory; AI surfaces
// the drug name but the clinician picks the shelf entry from the
// witness picker / drug picker before tapping Confirm.
type MaterialiseDrugOpInput struct {
	ClinicID            uuid.UUID
	StaffID             uuid.UUID
	SubjectID           *uuid.UUID
	NoteID              uuid.UUID
	NoteFieldID         uuid.UUID
	ShelfID             uuid.UUID
	Operation           string
	Quantity            float64
	Unit                string
	Dose                *string
	Route               *string
	ReasonIndication    *string
	WitnessedBy         *uuid.UUID
	WitnessKind         *string
	ExternalWitnessName *string
	ExternalWitnessRole *string
	WitnessAttestation  *string
	WitnessNote         *string
}

// MaterialiseIncidentInput — payload passed to the incidents adapter.
type MaterialiseIncidentInput struct {
	ClinicID         uuid.UUID
	StaffID          uuid.UUID
	SubjectID        uuid.UUID
	NoteID           uuid.UUID
	NoteFieldID      uuid.UUID
	IncidentType     string
	Severity         string
	OccurredAt       time.Time
	Location         *string
	BriefDescription string
	ImmediateActions *string
	WitnessesText    *string
	SubjectOutcome   *string
}

// MaterialisePainInput — payload passed to the pain adapter.
type MaterialisePainInput struct {
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	SubjectID     uuid.UUID
	NoteID        uuid.UUID
	NoteFieldID   uuid.UUID
	Score         int
	PainScaleUsed string
	Method        string
	Note          *string
}

// ConsentMaterialiser is the consent.Service surface notes calls into.
type ConsentMaterialiser interface {
	MaterialiseConsentForNote(ctx context.Context, in MaterialiseConsentInput) (*MaterialisedRef, error)
}

// DrugOpMaterialiser is the drugs.Service surface notes calls into.
type DrugOpMaterialiser interface {
	MaterialiseDrugOpForNote(ctx context.Context, in MaterialiseDrugOpInput) (*MaterialisedRef, error)
}

// IncidentMaterialiser is the incidents.Service surface notes calls into.
type IncidentMaterialiser interface {
	MaterialiseIncidentForNote(ctx context.Context, in MaterialiseIncidentInput) (*MaterialisedRef, error)
}

// PainMaterialiser is the pain.Service surface notes calls into.
type PainMaterialiser interface {
	MaterialisePainForNote(ctx context.Context, in MaterialisePainInput) (*MaterialisedRef, error)
}

// SetSystemMaterialisers wires the four typed adapters. Pass the
// concrete services from app.go. Calling with any nil disables that
// path; submit gate still rejects unmaterialised fields, just at
// different precision.
func (s *Service) SetSystemMaterialisers(
	consent ConsentMaterialiser,
	drugOp DrugOpMaterialiser,
	incident IncidentMaterialiser,
	pain PainMaterialiser,
) {
	s.consentMat = consent
	s.drugOpMat = drugOp
	s.incidentMat = incident
	s.painMat = pain
}

// SystemSummaryItem is one labelled key/value pair to render on a
// materialised system widget — both the FE card and the PDF use the
// same shape so the summary stays consistent across surfaces.
type SystemSummaryItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SystemSummary is the read-side counterpart to the materialise
// adapters. Notes calls into each domain to fetch a small list of
// labelled fields to surface on the materialised card / PDF row, so
// the user sees what they actually captured (drug name + quantity,
// incident type + severity, …) instead of just an entity id.
//
// ReviewStatus, when non-empty, drives the witness/approval pill on
// the materialised system widget card and the equivalent footer line
// on the PDF. Values: "not_required" / "pending" / "approved" /
// "challenged". Empty string = the entity has no approval lifecycle
// (e.g. a non-controlled drug op, or a domain that hasn't opted in).
type SystemSummary struct {
	EntityID     uuid.UUID
	Items        []SystemSummaryItem
	ReviewStatus string
}

// ConsentSummariser returns a short labelled summary for a consent
// record id — used to render the materialised consent card / PDF row.
type ConsentSummariser interface {
	SummariseConsent(ctx context.Context, id, clinicID uuid.UUID) (*SystemSummary, error)
}

// DrugOpSummariser returns a short labelled summary for a drug op id.
type DrugOpSummariser interface {
	SummariseDrugOp(ctx context.Context, id, clinicID uuid.UUID) (*SystemSummary, error)
}

// IncidentSummariser returns a short labelled summary for an incident id.
type IncidentSummariser interface {
	SummariseIncident(ctx context.Context, id, clinicID uuid.UUID) (*SystemSummary, error)
}

// PainSummariser returns a short labelled summary for a pain score id.
type PainSummariser interface {
	SummarisePain(ctx context.Context, id, clinicID uuid.UUID) (*SystemSummary, error)
}

// SetSystemSummarisers wires the read-side adapters. Each adapter
// implementation lives in app.go and bridges to the corresponding
// domain service. Pass nil to disable summaries for a given kind —
// the FE card / PDF will fall back to "linked" without details.
func (s *Service) SetSystemSummarisers(
	consent ConsentSummariser,
	drugOp DrugOpSummariser,
	incident IncidentSummariser,
	pain PainSummariser,
) {
	s.consentSum = consent
	s.drugOpSum = drugOp
	s.incidentSum = incident
	s.painSum = pain
}

// ── id-pointer JSON helpers ────────────────────────────────────────────

// idPointerKeys — the well-known keys we look for to decide whether a
// note_fields.value already materialised. Each system widget uses one.
var idPointerKeys = map[schema.FieldType]string{
	schema.FieldTypeSystemConsent:   "consent_id",
	schema.FieldTypeSystemDrugOp:    "operation_id",
	schema.FieldTypeSystemIncident:  "incident_id",
	schema.FieldTypeSystemPainScore: "pain_score_id",
}

// IsMaterialisedValue reports whether a JSON-encoded note_fields.value
// is already an id-pointer (e.g. {"consent_id":"<uuid>"}). Empty / null
// values count as unmaterialised; arbitrary JSON payloads (the AI's
// extraction output) also count as unmaterialised.
func IsMaterialisedValue(t schema.FieldType, raw *string) bool {
	key, ok := idPointerKeys[t]
	if !ok {
		return false
	}
	if raw == nil {
		return false
	}
	s := strings.TrimSpace(*raw)
	if s == "" || s == "null" {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return false
	}
	id, ok := m[key].(string)
	if !ok || id == "" {
		return false
	}
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	return true
}

// idPointerJSON marshals an id-pointer for storage in note_fields.value.
func idPointerJSON(t schema.FieldType, id uuid.UUID) (string, error) {
	key, ok := idPointerKeys[t]
	if !ok {
		return "", fmt.Errorf("notes.materialise.idPointerJSON: not a system field type %q", t)
	}
	b, err := json.Marshal(map[string]string{key: id.String()})
	if err != nil {
		return "", fmt.Errorf("notes.materialise.idPointerJSON: %w", err)
	}
	return string(b), nil
}

// ── Submit-time materialisation ──────────────────────────────────────────
//
// System widgets ARE NOT written to their typed ledgers eagerly. While a
// note sits in `draft` (or `overriding`) the structured payload lives in
// note_fields.value; deleting the draft therefore leaves no ledger
// orphans. On Submit we walk every system.* field and call the typed
// materialiser adapter, replacing the JSON payload with an id-pointer on
// success.
//
// Standalone direct-from-patient ledger creates (POST /api/v1/drugs/
// operations etc.) are unaffected — they hit the entity service directly,
// no note involved.

// materialiseSystemFieldsForSubmit walks every system.* field on the
// note, parses its structured payload, calls the typed materialiser
// adapter, and writes the id-pointer back into note_fields.value. Called
// from SubmitNote so a draft never writes to a regulator-binding ledger.
//
// Behavior:
//   - NULL value → skip (validateForSubmission already rejects required
//     fields with no value).
//   - Already-materialised id-pointer → skip (re-submit after override
//     where the user kept the original entity).
//   - Structured payload → parse + materialise. Parse errors and
//     adapter errors are returned as ErrValidation tagged with the field
//     title so the FE can surface "Drug op (CD): missing shelf_id".
//
// Best-effort transactional: each materialisation writes its pointer
// before the next runs, so a failure midway leaves earlier ledger rows
// in place but flips no further fields. The note stays in draft /
// overriding; retrying Submit picks up where it left off (already-
// materialised fields are skipped).
func (s *Service) materialiseSystemFieldsForSubmit(ctx context.Context, noteID, clinicID, staffID uuid.UUID) error {
	rows, err := s.repo.ListSystemFieldStates(ctx, noteID, clinicID)
	if err != nil {
		return fmt.Errorf("notes.service.materialiseSystemFieldsForSubmit: %w", err)
	}
	for _, r := range rows {
		ft := schema.FieldType(r.FieldType)
		if !schema.IsSystemFieldType(ft) {
			continue
		}
		if r.Value == nil {
			continue
		}
		raw := strings.TrimSpace(*r.Value)
		if raw == "" || raw == "null" {
			continue
		}
		if IsMaterialisedValue(ft, r.Value) {
			continue
		}
		if err := s.materialiseSystemField(ctx, r, clinicID, staffID, raw); err != nil {
			return fmt.Errorf("notes.service.materialiseSystemFieldsForSubmit: %q: %w", r.Title, err)
		}
	}
	return nil
}

// materialiseSystemField dispatches one row's payload to the right
// adapter. Each branch parses the JSON payload, validates required
// fields, and calls materialiseTyped. The structured payload shape
// mirrors the legacy materialise-* HTTP body (the FE produces it
// directly when the user taps Confirm on the widget).
func (s *Service) materialiseSystemField(ctx context.Context, row NoteFieldWithType, clinicID, staffID uuid.UUID, raw string) error {
	ft := schema.FieldType(row.FieldType)
	switch ft {
	case schema.FieldTypeSystemConsent:
		if s.consentMat == nil {
			return fmt.Errorf("consent materialiser not wired: %w", domain.ErrConflict)
		}
		if row.SubjectID == nil {
			return fmt.Errorf("note has no subject — consent must attach to a patient: %w", domain.ErrValidation)
		}
		in, err := parseConsentPayload(raw)
		if err != nil {
			return err
		}
		in.ClinicID, in.StaffID, in.NoteID, in.NoteFieldID = clinicID, staffID, row.NoteID, row.FieldID
		in.SubjectID = *row.SubjectID
		err = s.materialiseTyped(ctx, row.NoteID, row.FieldID, clinicID, ft,
			func(_ *NoteFieldWithType) (*MaterialisedRef, error) {
				return s.consentMat.MaterialiseConsentForNote(ctx, in)
			})
		return err

	case schema.FieldTypeSystemDrugOp:
		if s.drugOpMat == nil {
			return fmt.Errorf("drug op materialiser not wired: %w", domain.ErrConflict)
		}
		in, err := parseDrugOpPayload(raw)
		if err != nil {
			return err
		}
		in.ClinicID, in.StaffID, in.NoteID, in.NoteFieldID = clinicID, staffID, row.NoteID, row.FieldID
		// Drug ops can be standalone (stocktake / receive) — subject is optional.
		in.SubjectID = row.SubjectID
		err = s.materialiseTyped(ctx, row.NoteID, row.FieldID, clinicID, ft,
			func(_ *NoteFieldWithType) (*MaterialisedRef, error) {
				return s.drugOpMat.MaterialiseDrugOpForNote(ctx, in)
			})
		return err

	case schema.FieldTypeSystemIncident:
		if s.incidentMat == nil {
			return fmt.Errorf("incident materialiser not wired: %w", domain.ErrConflict)
		}
		if row.SubjectID == nil {
			return fmt.Errorf("note has no subject — incident must attach to a patient: %w", domain.ErrValidation)
		}
		in, err := parseIncidentPayload(raw)
		if err != nil {
			return err
		}
		in.ClinicID, in.StaffID, in.NoteID, in.NoteFieldID = clinicID, staffID, row.NoteID, row.FieldID
		in.SubjectID = *row.SubjectID
		err = s.materialiseTyped(ctx, row.NoteID, row.FieldID, clinicID, ft,
			func(_ *NoteFieldWithType) (*MaterialisedRef, error) {
				return s.incidentMat.MaterialiseIncidentForNote(ctx, in)
			})
		return err

	case schema.FieldTypeSystemPainScore:
		if s.painMat == nil {
			return fmt.Errorf("pain materialiser not wired: %w", domain.ErrConflict)
		}
		if row.SubjectID == nil {
			return fmt.Errorf("note has no subject — pain score must attach to a patient: %w", domain.ErrValidation)
		}
		in, err := parsePainPayload(raw)
		if err != nil {
			return err
		}
		in.ClinicID, in.StaffID, in.NoteID, in.NoteFieldID = clinicID, staffID, row.NoteID, row.FieldID
		in.SubjectID = *row.SubjectID
		err = s.materialiseTyped(ctx, row.NoteID, row.FieldID, clinicID, ft,
			func(_ *NoteFieldWithType) (*MaterialisedRef, error) {
				return s.painMat.MaterialisePainForNote(ctx, in)
			})
		return err
	}
	return nil
}

// materialiseTyped is the shared body for one field — short-circuits if
// already materialised, calls the adapter, writes the id-pointer back.
// Idempotent: a re-call on a field that's already an id-pointer is a
// no-op (skips the ledger insert). Submit-time callers don't need the
// returned ref — the resulting id-pointer is read back via GetNote.
func (s *Service) materialiseTyped(
	ctx context.Context,
	noteID, fieldID, clinicID uuid.UUID,
	expected schema.FieldType,
	create func(field *NoteFieldWithType) (*MaterialisedRef, error),
) error {
	field, err := s.repo.GetNoteFieldWithType(ctx, noteID, fieldID, clinicID)
	if err != nil {
		return fmt.Errorf("notes.service.materialiseTyped: get field: %w", err)
	}
	if field.FieldType != string(expected) {
		return fmt.Errorf("notes.service.materialiseTyped: field type is %q, expected %q: %w",
			field.FieldType, expected, domain.ErrValidation)
	}
	if IsMaterialisedValue(expected, field.Value) {
		return nil
	}

	ref, err := create(field)
	if err != nil {
		return fmt.Errorf("notes.service.materialiseTyped: %w", err)
	}

	pointer, err := idPointerJSON(expected, ref.EntityID)
	if err != nil {
		return fmt.Errorf("notes.service.materialiseTyped: %w", err)
	}
	if err := s.repo.WriteMaterialisedPointer(ctx, noteID, fieldID, clinicID, pointer); err != nil {
		return fmt.Errorf("notes.service.materialiseTyped: write pointer: %w", err)
	}
	return nil
}

// ── Payload parsers ───────────────────────────────────────────────────────
//
// The FE writes a structured JSON payload into note_fields.value when
// the user fills a system widget. Shape mirrors the legacy materialise-*
// HTTP body so the FE can keep using the same picker code; the only
// difference is the wire — it goes through PATCH /notes/{id}/fields/{fid}
// like any other field, instead of a dedicated endpoint.

type consentPayload struct {
	ConsentType                 string  `json:"consent_type"`
	Scope                       string  `json:"scope"`
	CapturedVia                 string  `json:"captured_via"`
	RisksDiscussed              *string `json:"risks_discussed,omitempty"`
	AlternativesDiscussed       *string `json:"alternatives_discussed,omitempty"`
	ConsentingPartyName         *string `json:"consenting_party_name,omitempty"`
	ConsentingPartyRelationship *string `json:"consenting_party_relationship,omitempty"`
	WitnessID                   *string `json:"witness_id,omitempty"`
	ExpiresAt                   *string `json:"expires_at,omitempty"`
}

func parseConsentPayload(raw string) (MaterialiseConsentInput, error) {
	var p consentPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return MaterialiseConsentInput{}, fmt.Errorf("invalid consent payload: %w", domain.ErrValidation)
	}
	if p.ConsentType == "" || p.Scope == "" || p.CapturedVia == "" {
		return MaterialiseConsentInput{}, fmt.Errorf("consent payload missing consent_type/scope/captured_via: %w", domain.ErrValidation)
	}
	in := MaterialiseConsentInput{
		ConsentType:                 p.ConsentType,
		Scope:                       p.Scope,
		CapturedVia:                 p.CapturedVia,
		RisksDiscussed:              p.RisksDiscussed,
		AlternativesDiscussed:       p.AlternativesDiscussed,
		ConsentingPartyName:         p.ConsentingPartyName,
		ConsentingPartyRelationship: p.ConsentingPartyRelationship,
	}
	if p.WitnessID != nil && *p.WitnessID != "" {
		id, err := uuid.Parse(*p.WitnessID)
		if err != nil {
			return MaterialiseConsentInput{}, fmt.Errorf("invalid witness_id: %w", domain.ErrValidation)
		}
		in.WitnessID = &id
	}
	if p.ExpiresAt != nil && *p.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *p.ExpiresAt)
		if err != nil {
			return MaterialiseConsentInput{}, fmt.Errorf("invalid expires_at (RFC3339): %w", domain.ErrValidation)
		}
		in.ExpiresAt = &t
	}
	return in, nil
}

type drugOpPayload struct {
	ShelfID             string  `json:"shelf_id"`
	Operation           string  `json:"operation"`
	Quantity            float64 `json:"quantity"`
	Unit                string  `json:"unit"`
	Dose                *string `json:"dose,omitempty"`
	Route               *string `json:"route,omitempty"`
	ReasonIndication    *string `json:"reason_indication,omitempty"`
	WitnessedBy         *string `json:"witnessed_by,omitempty"`
	WitnessKind         *string `json:"witness_kind,omitempty"`
	ExternalWitnessName *string `json:"external_witness_name,omitempty"`
	ExternalWitnessRole *string `json:"external_witness_role,omitempty"`
	WitnessAttestation  *string `json:"witness_attestation,omitempty"`
	WitnessNote         *string `json:"witness_note,omitempty"`
}

func parseDrugOpPayload(raw string) (MaterialiseDrugOpInput, error) {
	var p drugOpPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return MaterialiseDrugOpInput{}, fmt.Errorf("invalid drug_op payload: %w", domain.ErrValidation)
	}
	if p.ShelfID == "" || p.Operation == "" || p.Unit == "" {
		return MaterialiseDrugOpInput{}, fmt.Errorf("drug_op payload missing shelf_id/operation/unit: %w", domain.ErrValidation)
	}
	shelfID, err := uuid.Parse(p.ShelfID)
	if err != nil {
		return MaterialiseDrugOpInput{}, fmt.Errorf("invalid shelf_id: %w", domain.ErrValidation)
	}
	in := MaterialiseDrugOpInput{
		ShelfID:             shelfID,
		Operation:           p.Operation,
		Quantity:            p.Quantity,
		Unit:                p.Unit,
		Dose:                p.Dose,
		Route:               p.Route,
		ReasonIndication:    p.ReasonIndication,
		WitnessKind:         p.WitnessKind,
		ExternalWitnessName: p.ExternalWitnessName,
		ExternalWitnessRole: p.ExternalWitnessRole,
		WitnessAttestation:  p.WitnessAttestation,
		WitnessNote:         p.WitnessNote,
	}
	if p.WitnessedBy != nil && *p.WitnessedBy != "" {
		id, err := uuid.Parse(*p.WitnessedBy)
		if err != nil {
			return MaterialiseDrugOpInput{}, fmt.Errorf("invalid witnessed_by: %w", domain.ErrValidation)
		}
		in.WitnessedBy = &id
	}
	return in, nil
}

type incidentPayload struct {
	IncidentType     string  `json:"incident_type"`
	Severity         string  `json:"severity"`
	OccurredAt       *string `json:"occurred_at,omitempty"`
	Location         *string `json:"location,omitempty"`
	BriefDescription string  `json:"brief_description"`
	ImmediateActions *string `json:"immediate_actions,omitempty"`
	WitnessesText    *string `json:"witnesses_text,omitempty"`
	SubjectOutcome   *string `json:"subject_outcome,omitempty"`
}

func parseIncidentPayload(raw string) (MaterialiseIncidentInput, error) {
	var p incidentPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return MaterialiseIncidentInput{}, fmt.Errorf("invalid incident payload: %w", domain.ErrValidation)
	}
	if p.IncidentType == "" || p.Severity == "" || p.BriefDescription == "" {
		return MaterialiseIncidentInput{}, fmt.Errorf("incident payload missing incident_type/severity/brief_description: %w", domain.ErrValidation)
	}
	in := MaterialiseIncidentInput{
		IncidentType:     p.IncidentType,
		Severity:         p.Severity,
		OccurredAt:       domain.TimeNow(),
		Location:         p.Location,
		BriefDescription: p.BriefDescription,
		ImmediateActions: p.ImmediateActions,
		WitnessesText:    p.WitnessesText,
		SubjectOutcome:   p.SubjectOutcome,
	}
	if p.OccurredAt != nil && *p.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, *p.OccurredAt)
		if err != nil {
			return MaterialiseIncidentInput{}, fmt.Errorf("invalid occurred_at (RFC3339): %w", domain.ErrValidation)
		}
		in.OccurredAt = t
	}
	return in, nil
}

type painPayload struct {
	Score         int     `json:"score"`
	PainScaleUsed string  `json:"pain_scale_used"`
	Method        string  `json:"method"`
	Note          *string `json:"note,omitempty"`
}

func parsePainPayload(raw string) (MaterialisePainInput, error) {
	var p painPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return MaterialisePainInput{}, fmt.Errorf("invalid pain_score payload: %w", domain.ErrValidation)
	}
	if p.PainScaleUsed == "" || p.Method == "" {
		return MaterialisePainInput{}, fmt.Errorf("pain_score payload missing pain_scale_used/method: %w", domain.ErrValidation)
	}
	if p.Score < 0 || p.Score > 10 {
		return MaterialisePainInput{}, fmt.Errorf("pain_score score %d out of range [0,10]: %w", p.Score, domain.ErrValidation)
	}
	return MaterialisePainInput{
		Score:         p.Score,
		PainScaleUsed: p.PainScaleUsed,
		Method:        p.Method,
		Note:          p.Note,
	}, nil
}
