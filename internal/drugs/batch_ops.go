package drugs

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// ── Batch checkout (cart) ────────────────────────────────────────────────
//
// Checkout from the FE cart sends N pre-validated lines in one POST.
// The service iterates and calls LogOperation for each line in submit
// order. Each LogOperation already runs in its own DB transaction (with
// FOR UPDATE on the shelf row to detect concurrent balance changes), so
// this loop is sequential, not nested-transactional.
//
// Failure semantics: if line K fails, lines 1..K-1 have committed and
// line K..N are not attempted. The handler returns 422 with a body
// listing the successful lines + the failure index + reason so the FE
// can recover (offer the user to undo the logged ops via addends_to,
// or just inspect the ledger).
//
// We don't try to wrap the whole batch in one nested tx because each
// LogOperation already grabs its own connection from the pool and
// holds row locks long enough that nesting would cripple concurrency.
// Real atomicity would require a new repo method that does N inserts
// in a single tx — out of scope for v1; the partial-success surface
// is honest about the trade-off.

// BatchLogInput is what the service receives for a cart checkout.
type BatchLogInput struct { //nolint:revive
	ClinicID  uuid.UUID
	StaffID   uuid.UUID
	Lines     []LogOperationInput
}

// BatchLogResult tells the FE what landed and what didn't. When
// FailedIndex is nil, every line succeeded.
type BatchLogResult struct { //nolint:revive
	Logged      []*OperationResponse
	FailedIndex *int
	FailedError string
}

// BatchLogOperations runs the cart checkout sequentially. ClinicID +
// StaffID are stamped on each line before delegating to LogOperation
// so the FE never has to populate them per-line.
func (s *Service) BatchLogOperations(ctx context.Context, in BatchLogInput) (*BatchLogResult, error) {
	if len(in.Lines) == 0 {
		return nil, fmt.Errorf("drugs.service.BatchLogOperations: empty cart: %w", domain.ErrValidation)
	}
	out := &BatchLogResult{Logged: make([]*OperationResponse, 0, len(in.Lines))}
	for i, line := range in.Lines {
		line.ClinicID = in.ClinicID
		// AdministeredBy defaults to caller; keep any per-line override.
		if line.AdministeredBy == uuid.Nil {
			line.AdministeredBy = in.StaffID
		}
		if line.StaffID == uuid.Nil {
			line.StaffID = in.StaffID
		}
		resp, err := s.LogOperation(ctx, line)
		if err != nil {
			idx := i
			out.FailedIndex = &idx
			out.FailedError = err.Error()
			return out, fmt.Errorf("drugs.service.BatchLogOperations: line %d: %w", i, err)
		}
		out.Logged = append(out.Logged, resp)
	}
	return out, nil
}

// ── HTTP wire types ──────────────────────────────────────────────────────

type batchLogLineBody struct {
	ShelfID          string  `json:"shelf_id" minLength:"36"`
	SubjectID        *string `json:"subject_id,omitempty"`
	NoteID           *string `json:"note_id,omitempty"`
	Operation        string  `json:"operation" enum:"administer,dispense,discard,receive,transfer,adjust"`
	Quantity         float64 `json:"quantity"`
	Unit             string  `json:"unit" minLength:"1" maxLength:"20"`
	Dose             *string `json:"dose,omitempty"`
	Route            *string `json:"route,omitempty"`
	ReasonIndication *string `json:"reason_indication,omitempty"`
	WitnessedBy      *string `json:"witnessed_by,omitempty"`
	WitnessKind      *string `json:"witness_kind,omitempty" enum:"staff,pending,external,self"`
	ExternalWitnessName *string `json:"external_witness_name,omitempty" maxLength:"200"`
	ExternalWitnessRole *string `json:"external_witness_role,omitempty" maxLength:"80"`
	WitnessAttestation  *string `json:"witness_attestation,omitempty" maxLength:"2000"`
	WitnessNote         *string `json:"witness_note,omitempty" maxLength:"500"`
}

type batchLogBody struct {
	Body struct {
		// Common patient applied to every line that doesn't override.
		// Most cart checkouts dispense N drugs against ONE patient, so
		// the FE sets this once at the cart header and we fan it out.
		// Per-line subject_id still wins (e.g. mixed cart that has a
		// receive-stock line with no patient).
		SubjectID *string `json:"subject_id,omitempty" doc:"Default subject for every line that omits one"`

		Lines []batchLogLineBody `json:"lines" minItems:"1" maxItems:"50"`
	}
}

type batchLogHTTPResponse struct {
	Body struct {
		Logged      []*OperationResponse `json:"logged"`
		FailedIndex *int                 `json:"failed_index,omitempty" doc:"Zero-based index of the line that errored out, when present. Earlier lines committed successfully."`
		FailedError *string              `json:"failed_error,omitempty"`
	}
}

func (h *Handler) batchLogOperations(ctx context.Context, input *batchLogBody) (*batchLogHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	defaultSubject, err := optUUIDPtr(input.Body.SubjectID, "subject_id")
	if err != nil {
		return nil, err
	}

	lines := make([]LogOperationInput, len(input.Body.Lines))
	for i, b := range input.Body.Lines {
		shelfID, err := uuid.Parse(b.ShelfID)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("line %d: invalid shelf_id", i))
		}
		line := LogOperationInput{
			ShelfID:             shelfID,
			Operation:           b.Operation,
			Quantity:            b.Quantity,
			Unit:                b.Unit,
			Dose:                b.Dose,
			Route:               b.Route,
			ReasonIndication:    b.ReasonIndication,
			WitnessKind:         b.WitnessKind,
			ExternalWitnessName: b.ExternalWitnessName,
			ExternalWitnessRole: b.ExternalWitnessRole,
			WitnessAttestation:  b.WitnessAttestation,
			WitnessNote:         b.WitnessNote,
		}
		if b.SubjectID != nil && *b.SubjectID != "" {
			id, err := uuid.Parse(*b.SubjectID)
			if err != nil {
				return nil, huma.Error400BadRequest(fmt.Sprintf("line %d: invalid subject_id", i))
			}
			line.SubjectID = &id
		} else if defaultSubject != nil {
			line.SubjectID = defaultSubject
		}
		if b.NoteID != nil && *b.NoteID != "" {
			id, err := uuid.Parse(*b.NoteID)
			if err != nil {
				return nil, huma.Error400BadRequest(fmt.Sprintf("line %d: invalid note_id", i))
			}
			line.NoteID = &id
		}
		if b.WitnessedBy != nil && *b.WitnessedBy != "" {
			id, err := uuid.Parse(*b.WitnessedBy)
			if err != nil {
				return nil, huma.Error400BadRequest(fmt.Sprintf("line %d: invalid witnessed_by", i))
			}
			line.WitnessedBy = &id
		}
		lines[i] = line
	}

	res, err := h.svc.BatchLogOperations(ctx, BatchLogInput{
		ClinicID: clinicID,
		StaffID:  staffID,
		Lines:    lines,
	})
	if err != nil {
		// Partial-success path — the result still has Logged populated.
		// We surface a 422 so the FE can branch on the partial body.
		resp := &batchLogHTTPResponse{}
		if res != nil {
			resp.Body.Logged = res.Logged
			resp.Body.FailedIndex = res.FailedIndex
			if res.FailedError != "" {
				m := res.FailedError
				resp.Body.FailedError = &m
			}
		}
		return resp, mapDrugsError(err)
	}
	resp := &batchLogHTTPResponse{}
	resp.Body.Logged = res.Logged
	return resp, nil
}

// optUUIDPtr — small helper to parse an optional UUID query/body param.
func optUUIDPtr(s *string, name string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil //nolint:nilnil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid " + name)
	}
	return &id, nil
}
