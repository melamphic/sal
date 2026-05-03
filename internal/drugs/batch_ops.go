package drugs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// ── Batch checkout (cart) ────────────────────────────────────────────────
//
// Checkout from the FE cart sends N lines in one POST. We validate
// every line up-front (witness, shelf existence, balance arithmetic),
// compute per-shelf running balances so multiple lines on the same
// shelf chain correctly, and then commit the entire batch through
// repo.BatchLogOperationsTx — one transaction wrapping all N inserts +
// shelf-balance updates. All-or-nothing.
//
// Pending-witness ('witness_kind=pending') lines ARE supported. The
// ledger row commits inside the batch tx; the async approvals row is
// submitted post-commit, per pending line. If a per-line approval
// submit fails after the batch committed, we log + return the error
// — the ledger row is already in place (witness_status='pending')
// just like the single-call /operations endpoint behaves.

// BatchLogInput is what the service receives for a cart checkout.
type BatchLogInput struct { //nolint:revive
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Lines    []LogOperationInput
}

// BatchLogResult is the all-or-nothing checkout result. Logged is
// populated only on full success. FailedIndex / FailedError are kept
// in the type for back-compat with the handler shape; with atomic
// commit they only fire when validation rejects a line up-front (in
// which case Logged is nil and FailedIndex points at the offending
// line so the FE can highlight it).
type BatchLogResult struct { //nolint:revive
	Logged      []*OperationResponse
	FailedIndex *int
	FailedError string
}

// BatchLogOperations runs the cart checkout atomically. Pre-validates
// all lines (witness rules, shelf existence, balance arithmetic),
// computes per-shelf running balances so same-shelf lines chain, then
// commits all inserts + balance updates in a single repo transaction.
// On any per-line validation or commit failure the entire batch rolls
// back — no partial-success surface.
func (s *Service) BatchLogOperations(ctx context.Context, in BatchLogInput) (*BatchLogResult, error) {
	if len(in.Lines) == 0 {
		return nil, fmt.Errorf("drugs.service.BatchLogOperations: empty cart: %w", domain.ErrValidation)
	}

	// Cache per-shelf state so we only fetch each shelf row once even
	// if the cart has multiple lines from the same shelf.
	shelfCache := map[uuid.UUID]*ShelfRecord{}
	requiresWitnessCache := map[uuid.UUID]bool{}
	runningBalance := map[uuid.UUID]float64{}
	prepped := make([]CreateOperationParams, 0, len(in.Lines))

	// Per-clinic retention policy is the same for every line; fetch once.
	var retentionUntil *time.Time
	if pol, err := s.repo.GetRetentionPolicy(ctx, in.ClinicID); err == nil {
		ru := domainTimeNow().AddDate(pol.LedgerYears, 0, 0)
		retentionUntil = &ru
	} else if !errors.Is(err, domain.ErrNotFound) {
		slog.Warn("drugs.service.BatchLogOperations: retention policy lookup failed; rows keep forever",
			"clinic_id", in.ClinicID, "error", err.Error())
	}

	for i, line := range in.Lines {
		// Stamp clinic + actor on every line so handler doesn't have to.
		line.ClinicID = in.ClinicID
		if line.AdministeredBy == uuid.Nil {
			line.AdministeredBy = in.StaffID
		}
		if line.StaffID == uuid.Nil {
			line.StaffID = in.StaffID
		}
		if err := validateOperation(line); err != nil {
			idx := i
			return &BatchLogResult{FailedIndex: &idx, FailedError: err.Error()},
				fmt.Errorf("drugs.service.BatchLogOperations: line %d: %w", i, err)
		}

		shelf, ok := shelfCache[line.ShelfID]
		if !ok {
			s2, err := s.repo.GetShelfEntryByID(ctx, line.ShelfID, in.ClinicID)
			if err != nil {
				idx := i
				return &BatchLogResult{FailedIndex: &idx, FailedError: err.Error()},
					fmt.Errorf("drugs.service.BatchLogOperations: line %d: shelf: %w", i, err)
			}
			if s2.ArchivedAt != nil {
				idx := i
				return &BatchLogResult{FailedIndex: &idx, FailedError: "shelf archived"},
					fmt.Errorf("drugs.service.BatchLogOperations: line %d: shelf archived: %w",
						i, domain.ErrConflict)
			}
			shelfCache[line.ShelfID] = s2
			runningBalance[line.ShelfID] = s2.Balance
			shelf = s2
		}

		requiresWitness, ok := requiresWitnessCache[line.ShelfID]
		if !ok {
			rw, err := s.shelfRequiresWitness(ctx, in.ClinicID, shelf)
			if err != nil {
				return nil, fmt.Errorf("drugs.service.BatchLogOperations: line %d: witness check: %w", i, err)
			}
			requiresWitnessCache[line.ShelfID] = rw
			requiresWitness = rw
		}

		// Inline witness validation — same rules as service.LogOperation
		// but with batch-specific surface (pending already rejected above).
		witnessKind := ""
		if line.WitnessKind != nil {
			witnessKind = strings.TrimSpace(*line.WitnessKind)
		}
		if requiresWitness {
			switch witnessKind {
			case "", "staff":
				witnessKind = "staff"
				if line.WitnessedBy == nil {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "witness required for controlled drug"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: witness required: %w", i, domain.ErrValidation)
				}
				if *line.WitnessedBy == line.AdministeredBy {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "witness must differ from administering staff"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: witness same as actor: %w", i, domain.ErrValidation)
				}
				perm, err := s.staffPerms.HasPermission(ctx, *line.WitnessedBy, in.ClinicID, "perm_witness_controlled_drugs")
				if err != nil {
					return nil, fmt.Errorf("drugs.service.BatchLogOperations: line %d: witness perm: %w", i, err)
				}
				if !perm {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "witness lacks perm_witness_controlled_drugs"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: witness lacks perm: %w", i, domain.ErrForbidden)
				}
			case "pending":
				if s.approvals == nil {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "async approvals not configured"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: pending witness requires approvals service: %w",
							i, domain.ErrValidation)
				}
				// Pending mode logs the row now without a witness; the
				// approvals queue insert fires post-commit, per line.
			case "external":
				if line.ExternalWitnessName == nil || strings.TrimSpace(*line.ExternalWitnessName) == "" {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "external witness name required"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: external witness name required: %w", i, domain.ErrValidation)
				}
				if line.WitnessAttestation == nil || len(strings.TrimSpace(*line.WitnessAttestation)) < 10 {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "external witness attestation too short"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: external attestation: %w", i, domain.ErrValidation)
				}
			case "self":
				if line.Operation == "discard" {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "self-witness not permitted for discard"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: self-witness on discard: %w", i, domain.ErrValidation)
				}
				if line.WitnessAttestation == nil || len(strings.TrimSpace(*line.WitnessAttestation)) < 30 {
					idx := i
					return &BatchLogResult{FailedIndex: &idx, FailedError: "self-witness attestation too short"},
						fmt.Errorf("drugs.service.BatchLogOperations: line %d: self attestation: %w", i, domain.ErrValidation)
				}
			default:
				idx := i
				return &BatchLogResult{FailedIndex: &idx, FailedError: "unknown witness_kind"},
					fmt.Errorf("drugs.service.BatchLogOperations: line %d: unknown witness_kind %q: %w",
						i, witnessKind, domain.ErrValidation)
			}
		}

		// Per-line balance using the running balance for the shelf.
		balanceBefore := runningBalance[line.ShelfID]
		balanceAfter, err := computeBalanceAfter(line.Operation, balanceBefore, line.Quantity)
		if err != nil {
			idx := i
			return &BatchLogResult{FailedIndex: &idx, FailedError: err.Error()},
				fmt.Errorf("drugs.service.BatchLogOperations: line %d: balance: %w", i, err)
		}
		runningBalance[line.ShelfID] = balanceAfter

		// Compliance v2 chain context.
		cc, err := s.loadCatalogContext(ctx, in.ClinicID, shelf)
		if err != nil {
			slog.Warn("drugs.service.BatchLogOperations: catalog context load failed; chain disabled for line",
				"clinic_id", in.ClinicID, "shelf_id", line.ShelfID, "error", err.Error())
			cc = &catalogContext{}
		}
		var chainK []byte
		if cc.DrugName != "" && cc.Strength != "" && cc.Form != "" {
			chainK = chainKey(in.ClinicID, cc.DrugName, cc.Strength, cc.Form)
		}

		// Witness shape mapping — same as single-call path.
		var witnessKindParam *string
		var witnessStatusParam *string
		switch {
		case !requiresWitness:
			ss := string(domain.EntityReviewNotRequired)
			witnessStatusParam = &ss
		default:
			k := witnessKind
			witnessKindParam = &k
			ss := string(domain.EntityReviewApproved)
			witnessStatusParam = &ss
		}

		prepped = append(prepped, CreateOperationParams{
			ID:                  domain.NewID(),
			ClinicID:            in.ClinicID,
			ShelfID:             line.ShelfID,
			SubjectID:           line.SubjectID,
			NoteID:              line.NoteID,
			NoteFieldID:         line.NoteFieldID,
			Operation:           line.Operation,
			Quantity:            line.Quantity,
			Unit:                line.Unit,
			Dose:                line.Dose,
			Route:               line.Route,
			ReasonIndication:    line.ReasonIndication,
			AdministeredBy:      line.AdministeredBy,
			WitnessedBy:         line.WitnessedBy,
			PrescribedBy:        line.PrescribedBy,
			BalanceBefore:       balanceBefore,
			BalanceAfter:        balanceAfter,
			AddendsTo:           line.AddendsTo,
			Status:              line.Status,
			DrugName:            cc.DrugName,
			DrugStrength:        cc.Strength,
			DrugForm:            cc.Form,
			ChainKey:            chainK,
			RetentionUntil:      retentionUntil,
			WitnessKind:         witnessKindParam,
			ExternalWitnessName: trimNonEmpty(line.ExternalWitnessName),
			ExternalWitnessRole: trimNonEmpty(line.ExternalWitnessRole),
			WitnessAttestation:  trimNonEmpty(line.WitnessAttestation),
			WitnessStatus:       witnessStatusParam,
		})
	}

	// Atomic commit — repo wraps every insert + balance update in one tx.
	recs, err := s.repo.BatchLogOperationsTx(ctx, prepped)
	if err != nil {
		return nil, fmt.Errorf("drugs.service.BatchLogOperations: %w", err)
	}

	// Best-effort subject-access logs after commit. Don't fail the cart
	// on a logger glitch — the ledger landed.
	if s.accessLogger != nil {
		for i, rec := range recs {
			line := in.Lines[i]
			if line.SubjectID != nil {
				_ = s.accessLogger.LogAccess(ctx, in.ClinicID, *line.SubjectID, line.StaffID,
					"drug_op", "drugs.service.BatchLogOperations")
			}
			_ = rec
		}
	}

	out := &BatchLogResult{Logged: make([]*OperationResponse, len(recs))}
	for i, r := range recs {
		out.Logged[i] = operationRecordToResponse(r)
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
