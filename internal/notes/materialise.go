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
	ClinicID         uuid.UUID
	StaffID          uuid.UUID
	SubjectID        *uuid.UUID
	NoteID           uuid.UUID
	NoteFieldID      uuid.UUID
	ShelfID          uuid.UUID
	Operation        string
	Quantity         float64
	Unit             string
	Dose             *string
	Route            *string
	ReasonIndication *string
	WitnessedBy      *uuid.UUID
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
type SystemSummary struct {
	EntityID uuid.UUID
	Items    []SystemSummaryItem
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

// ── Service methods — typed entrypoints called by HTTP handlers ────────

// MaterialiseConsent creates the consent record for a system.consent
// field and writes the id-pointer back into note_fields.value.
// Idempotent: if the field already points at a materialised consent,
// returns the existing entity reference without re-creating.
func (s *Service) MaterialiseConsent(ctx context.Context, noteID, fieldID, clinicID, staffID uuid.UUID, in MaterialiseConsentInput) (*MaterialisedRef, error) {
	in.ClinicID = clinicID
	in.StaffID = staffID
	in.NoteID = noteID
	in.NoteFieldID = fieldID
	return s.materialiseTyped(ctx, noteID, fieldID, clinicID,
		schema.FieldTypeSystemConsent,
		func(field *NoteFieldWithType) (*MaterialisedRef, error) {
			if s.consentMat == nil {
				return nil, fmt.Errorf("notes.service.MaterialiseConsent: consent materialiser not wired: %w", domain.ErrConflict)
			}
			if field.SubjectID == nil {
				return nil, fmt.Errorf("notes.service.MaterialiseConsent: note has no subject — consent must attach to a patient: %w", domain.ErrValidation)
			}
			in.SubjectID = *field.SubjectID
			return s.consentMat.MaterialiseConsentForNote(ctx, in)
		})
}

// MaterialiseDrugOp creates the controlled-drug ledger row for a
// system.drug_op field. Status is forced to 'confirmed' here — the
// clinician's explicit tap on Confirm IS the materialisation event.
// Pending_confirm is reserved for future auto-materialise paths.
func (s *Service) MaterialiseDrugOp(ctx context.Context, noteID, fieldID, clinicID, staffID uuid.UUID, in MaterialiseDrugOpInput) (*MaterialisedRef, error) {
	in.ClinicID = clinicID
	in.StaffID = staffID
	in.NoteID = noteID
	in.NoteFieldID = fieldID
	return s.materialiseTyped(ctx, noteID, fieldID, clinicID,
		schema.FieldTypeSystemDrugOp,
		func(field *NoteFieldWithType) (*MaterialisedRef, error) {
			if s.drugOpMat == nil {
				return nil, fmt.Errorf("notes.service.MaterialiseDrugOp: drug op materialiser not wired: %w", domain.ErrConflict)
			}
			// Drug ops can be standalone (stocktake / receive) — subject
			// is optional. Pass through whatever the note has.
			in.SubjectID = field.SubjectID
			return s.drugOpMat.MaterialiseDrugOpForNote(ctx, in)
		})
}

// MaterialiseIncident creates the incident_events row for a
// system.incident field. The classifier runs server-side as today; the
// returned id-pointer + the classifier deadline drive the Compliance
// Inbox countdown.
func (s *Service) MaterialiseIncident(ctx context.Context, noteID, fieldID, clinicID, staffID uuid.UUID, in MaterialiseIncidentInput) (*MaterialisedRef, error) {
	in.ClinicID = clinicID
	in.StaffID = staffID
	in.NoteID = noteID
	in.NoteFieldID = fieldID
	return s.materialiseTyped(ctx, noteID, fieldID, clinicID,
		schema.FieldTypeSystemIncident,
		func(field *NoteFieldWithType) (*MaterialisedRef, error) {
			if s.incidentMat == nil {
				return nil, fmt.Errorf("notes.service.MaterialiseIncident: incident materialiser not wired: %w", domain.ErrConflict)
			}
			if field.SubjectID == nil {
				return nil, fmt.Errorf("notes.service.MaterialiseIncident: note has no subject — incident must attach to a patient: %w", domain.ErrValidation)
			}
			in.SubjectID = *field.SubjectID
			return s.incidentMat.MaterialiseIncidentForNote(ctx, in)
		})
}

// MaterialisePain creates the pain_scores row for a system.pain_score field.
func (s *Service) MaterialisePain(ctx context.Context, noteID, fieldID, clinicID, staffID uuid.UUID, in MaterialisePainInput) (*MaterialisedRef, error) {
	in.ClinicID = clinicID
	in.StaffID = staffID
	in.NoteID = noteID
	in.NoteFieldID = fieldID
	return s.materialiseTyped(ctx, noteID, fieldID, clinicID,
		schema.FieldTypeSystemPainScore,
		func(field *NoteFieldWithType) (*MaterialisedRef, error) {
			if s.painMat == nil {
				return nil, fmt.Errorf("notes.service.MaterialisePain: pain materialiser not wired: %w", domain.ErrConflict)
			}
			if field.SubjectID == nil {
				return nil, fmt.Errorf("notes.service.MaterialisePain: note has no subject — pain score must attach to a patient: %w", domain.ErrValidation)
			}
			in.SubjectID = *field.SubjectID
			return s.painMat.MaterialisePainForNote(ctx, in)
		})
}

// materialiseTyped is the shared body — validates the field type
// matches expected, short-circuits if already materialised, calls the
// adapter, writes the id-pointer back. Returns the ref the caller
// should hand to the HTTP layer.
//
// The create callback receives the full NoteFieldWithType so each
// type-specific Materialise method can pull subject_id off the parent
// note before calling its adapter (consent/incident/pain need a non-zero
// subject; drug op accepts an optional one).
func (s *Service) materialiseTyped(
	ctx context.Context,
	noteID, fieldID, clinicID uuid.UUID,
	expected schema.FieldType,
	create func(field *NoteFieldWithType) (*MaterialisedRef, error),
) (*MaterialisedRef, error) {
	field, err := s.repo.GetNoteFieldWithType(ctx, noteID, fieldID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.materialiseTyped: get field: %w", err)
	}
	if field.FieldType != string(expected) {
		return nil, fmt.Errorf("notes.service.materialiseTyped: field type is %q, expected %q: %w",
			field.FieldType, expected, domain.ErrValidation)
	}
	if IsMaterialisedValue(expected, field.Value) {
		// Idempotent — return the existing pointer.
		key := idPointerKeys[expected]
		var m map[string]string
		_ = json.Unmarshal([]byte(*field.Value), &m)
		id, _ := uuid.Parse(m[key])
		return &MaterialisedRef{EntityID: id}, nil
	}

	ref, err := create(field)
	if err != nil {
		return nil, fmt.Errorf("notes.service.materialiseTyped: %w", err)
	}

	pointer, err := idPointerJSON(expected, ref.EntityID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.materialiseTyped: %w", err)
	}
	if err := s.repo.WriteMaterialisedPointer(ctx, noteID, fieldID, clinicID, pointer); err != nil {
		return nil, fmt.Errorf("notes.service.materialiseTyped: write pointer: %w", err)
	}
	return ref, nil
}

// ── Submit gate ────────────────────────────────────────────────────────

// ListUnmaterialisedSystemFields returns every system.* field on the
// note whose value is non-null but not yet an id-pointer. Submit is
// blocked while this list is non-empty.
func (s *Service) ListUnmaterialisedSystemFields(ctx context.Context, noteID, clinicID uuid.UUID) ([]UnmaterialisedField, error) {
	rows, err := s.repo.ListSystemFieldStates(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.ListUnmaterialisedSystemFields: %w", err)
	}
	var out []UnmaterialisedField
	for _, r := range rows {
		ft := schema.FieldType(r.FieldType)
		if !schema.IsSystemFieldType(ft) {
			continue
		}
		if r.Value == nil {
			continue
		}
		if IsMaterialisedValue(ft, r.Value) {
			continue
		}
		out = append(out, UnmaterialisedField{
			FieldID:   r.FieldID,
			FieldType: r.FieldType,
			Title:     r.Title,
		})
	}
	return out, nil
}

// UnmaterialisedField — name + id of a system widget that's been
// AI-filled but not yet committed to the ledger. Surfaces in the submit
// error so the user knows exactly what to confirm.
type UnmaterialisedField struct {
	FieldID   uuid.UUID
	FieldType string
	Title     string
}

// pluralS — small grammar helper for the submit-error message.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
