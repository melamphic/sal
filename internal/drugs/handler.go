package drugs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires drugs HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler constructs a drugs Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared input types ───────────────────────────────────────────────────────

type clinicCtxIDInput struct{}

type idPathInput struct {
	ID string `path:"id" doc:"The resource UUID."`
}

type entryIDPathInput struct {
	EntryID string `path:"entry_id" doc:"The catalog entry id (e.g. 'vet.NZ.ketamine.injectable.100mgml')."`
}

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"200" default:"50" doc:"Page size."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Page offset."`
}

// ── Catalog ──────────────────────────────────────────────────────────────────

type catalogHTTPResponse struct {
	Body *CatalogResponse
}

func (h *Handler) listCatalog(ctx context.Context, _ *clinicCtxIDInput) (*catalogHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListCatalog(ctx, clinicID)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &catalogHTTPResponse{Body: resp}, nil
}

type catalogEntryHTTPResponse struct {
	Body *CatalogEntryResponse
}

func (h *Handler) getCatalogEntry(ctx context.Context, input *entryIDPathInput) (*catalogEntryHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.LookupCatalogEntry(ctx, clinicID, input.EntryID)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &catalogEntryHTTPResponse{Body: resp}, nil
}

// ── Override drugs ───────────────────────────────────────────────────────────

type createOverrideDrugBodyInput struct {
	Body struct {
		Name             string  `json:"name"             minLength:"1" maxLength:"200" doc:"Display name."`
		ActiveIngredient *string `json:"active_ingredient,omitempty"`
		Schedule         *string `json:"schedule,omitempty"`
		Strength         *string `json:"strength,omitempty"`
		Form             *string `json:"form,omitempty"`
		BrandName        *string `json:"brand_name,omitempty"`
		Notes            *string `json:"notes,omitempty"`
	}
}

type overrideDrugHTTPResponse struct {
	Body *OverrideDrugResponse
}

func (h *Handler) createOverrideDrug(ctx context.Context, input *createOverrideDrugBodyInput) (*overrideDrugHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	resp, err := h.svc.CreateOverrideDrug(ctx, CreateOverrideDrugInput{
		ClinicID:         clinicID,
		StaffID:          staffID,
		Name:             input.Body.Name,
		ActiveIngredient: input.Body.ActiveIngredient,
		Schedule:         input.Body.Schedule,
		Strength:         input.Body.Strength,
		Form:             input.Body.Form,
		BrandName:        input.Body.BrandName,
		Notes:            input.Body.Notes,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &overrideDrugHTTPResponse{Body: resp}, nil
}

type updateOverrideDrugBodyInput struct {
	ID   string `path:"id"`
	Body struct {
		Name             string  `json:"name" minLength:"1" maxLength:"200"`
		ActiveIngredient *string `json:"active_ingredient,omitempty"`
		Schedule         *string `json:"schedule,omitempty"`
		Strength         *string `json:"strength,omitempty"`
		Form             *string `json:"form,omitempty"`
		BrandName        *string `json:"brand_name,omitempty"`
		Notes            *string `json:"notes,omitempty"`
	}
}

func (h *Handler) updateOverrideDrug(ctx context.Context, input *updateOverrideDrugBodyInput) (*overrideDrugHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.UpdateOverrideDrug(ctx, UpdateOverrideDrugInput{
		ID:               id,
		ClinicID:         clinicID,
		Name:             input.Body.Name,
		ActiveIngredient: input.Body.ActiveIngredient,
		Schedule:         input.Body.Schedule,
		Strength:         input.Body.Strength,
		Form:             input.Body.Form,
		BrandName:        input.Body.BrandName,
		Notes:            input.Body.Notes,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &overrideDrugHTTPResponse{Body: resp}, nil
}

type emptyHTTPResponse struct{}

func (h *Handler) archiveOverrideDrug(ctx context.Context, input *idPathInput) (*emptyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := h.svc.ArchiveOverrideDrug(ctx, id, clinicID); err != nil {
		return nil, mapDrugsError(err)
	}
	return &emptyHTTPResponse{}, nil
}

// ── Shelf ────────────────────────────────────────────────────────────────────

type createShelfBodyInput struct {
	Body struct {
		CatalogID      *string  `json:"catalog_id,omitempty"`
		OverrideDrugID *string  `json:"override_drug_id,omitempty"`
		Strength       *string  `json:"strength,omitempty"`
		Form           *string  `json:"form,omitempty"`
		BatchNumber    *string  `json:"batch_number,omitempty"`
		ExpiryDate     *string  `json:"expiry_date,omitempty"  doc:"YYYY-MM-DD."`
		Location       string   `json:"location"               minLength:"1" maxLength:"80"`
		OpeningBalance float64  `json:"opening_balance"`
		Unit           string   `json:"unit"                   minLength:"1" maxLength:"20"`
		ParLevel       *float64 `json:"par_level,omitempty"`
		Notes          *string  `json:"notes,omitempty"`
	}
}

type shelfHTTPResponse struct {
	Body *ShelfResponse
}

type shelfListHTTPResponse struct {
	Body *ShelfListResponse
}

func (h *Handler) createShelf(ctx context.Context, input *createShelfBodyInput) (*shelfHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	in := CreateShelfInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		CatalogID:      input.Body.CatalogID,
		Strength:       input.Body.Strength,
		Form:           input.Body.Form,
		BatchNumber:    input.Body.BatchNumber,
		Location:       input.Body.Location,
		OpeningBalance: input.Body.OpeningBalance,
		Unit:           input.Body.Unit,
		ParLevel:       input.Body.ParLevel,
		Notes:          input.Body.Notes,
	}
	if input.Body.OverrideDrugID != nil {
		oid, err := uuid.Parse(*input.Body.OverrideDrugID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid override_drug_id")
		}
		in.OverrideDrugID = &oid
	}
	if input.Body.ExpiryDate != nil && *input.Body.ExpiryDate != "" {
		t, err := time.Parse("2006-01-02", *input.Body.ExpiryDate)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid expiry_date (want YYYY-MM-DD)")
		}
		in.ExpiryDate = &t
	}

	resp, err := h.svc.CreateShelfEntry(ctx, in)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &shelfHTTPResponse{Body: resp}, nil
}

type listShelfQueryInput struct {
	paginationInput
	IncludeArchived bool   `query:"include_archived"`
	Location        string `query:"location"`
	Search          string `query:"search"`
}

func (h *Handler) listShelf(ctx context.Context, input *listShelfQueryInput) (*shelfListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	in := ListShelfInput{
		Limit:           input.Limit,
		Offset:          input.Offset,
		IncludeArchived: input.IncludeArchived,
	}
	if input.Location != "" {
		in.Location = &input.Location
	}
	if input.Search != "" {
		in.Search = &input.Search
	}
	resp, err := h.svc.ListShelfEntries(ctx, clinicID, in)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &shelfListHTTPResponse{Body: resp}, nil
}

func (h *Handler) getShelf(ctx context.Context, input *idPathInput) (*shelfHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetShelfEntry(ctx, id, clinicID)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &shelfHTTPResponse{Body: resp}, nil
}

type updateShelfMetaBodyInput struct {
	ID   string `path:"id"`
	Body struct {
		Location string   `json:"location" minLength:"1" maxLength:"80"`
		ParLevel *float64 `json:"par_level,omitempty"`
		Notes    *string  `json:"notes,omitempty"`
	}
}

func (h *Handler) updateShelfMeta(ctx context.Context, input *updateShelfMetaBodyInput) (*shelfHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.UpdateShelfMeta(ctx, UpdateShelfMetaInput{
		ID:       id,
		ClinicID: clinicID,
		Location: input.Body.Location,
		ParLevel: input.Body.ParLevel,
		Notes:    input.Body.Notes,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &shelfHTTPResponse{Body: resp}, nil
}

func (h *Handler) archiveShelf(ctx context.Context, input *idPathInput) (*emptyHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := h.svc.ArchiveShelfEntry(ctx, id, clinicID); err != nil {
		return nil, mapDrugsError(err)
	}
	return &emptyHTTPResponse{}, nil
}

// ── Operations ──────────────────────────────────────────────────────────────

type logOperationBodyInput struct {
	Body struct {
		ShelfID          string   `json:"shelf_id"           minLength:"36"`
		SubjectID        *string  `json:"subject_id,omitempty"`
		NoteID           *string  `json:"note_id,omitempty"`
		Operation        string   `json:"operation"          enum:"administer,dispense,discard,receive,transfer,adjust"`
		Quantity         float64  `json:"quantity"`
		Unit             string   `json:"unit"               minLength:"1" maxLength:"20"`
		Dose             *string  `json:"dose,omitempty"`
		Route            *string  `json:"route,omitempty"`
		ReasonIndication *string  `json:"reason_indication,omitempty"`
		AdministeredBy   *string  `json:"administered_by,omitempty"`
		WitnessedBy      *string  `json:"witnessed_by,omitempty"`
		PrescribedBy     *string  `json:"prescribed_by,omitempty"`
		AddendsTo        *string  `json:"addends_to,omitempty"`
	}
}

type operationHTTPResponse struct {
	Body *OperationResponse
}

type operationListHTTPResponse struct {
	Body *OperationListResponse
}

func (h *Handler) logOperation(ctx context.Context, input *logOperationBodyInput) (*operationHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	shelfID, err := uuid.Parse(input.Body.ShelfID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid shelf_id")
	}

	in := LogOperationInput{
		ClinicID:         clinicID,
		StaffID:          staffID,
		ShelfID:          shelfID,
		Operation:        input.Body.Operation,
		Quantity:         input.Body.Quantity,
		Unit:             input.Body.Unit,
		Dose:             input.Body.Dose,
		Route:            input.Body.Route,
		ReasonIndication: input.Body.ReasonIndication,
		AdministeredBy:   staffID, // default to caller; overridable below
	}

	if input.Body.AdministeredBy != nil && *input.Body.AdministeredBy != "" {
		id, err := uuid.Parse(*input.Body.AdministeredBy)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid administered_by")
		}
		in.AdministeredBy = id
	}
	if input.Body.SubjectID != nil && *input.Body.SubjectID != "" {
		id, err := uuid.Parse(*input.Body.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		in.SubjectID = &id
	}
	if input.Body.NoteID != nil && *input.Body.NoteID != "" {
		id, err := uuid.Parse(*input.Body.NoteID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid note_id")
		}
		in.NoteID = &id
	}
	if input.Body.WitnessedBy != nil && *input.Body.WitnessedBy != "" {
		id, err := uuid.Parse(*input.Body.WitnessedBy)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid witnessed_by")
		}
		in.WitnessedBy = &id
	}
	if input.Body.PrescribedBy != nil && *input.Body.PrescribedBy != "" {
		id, err := uuid.Parse(*input.Body.PrescribedBy)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid prescribed_by")
		}
		in.PrescribedBy = &id
	}
	if input.Body.AddendsTo != nil && *input.Body.AddendsTo != "" {
		id, err := uuid.Parse(*input.Body.AddendsTo)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid addends_to")
		}
		in.AddendsTo = &id
	}

	resp, err := h.svc.LogOperation(ctx, in)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &operationHTTPResponse{Body: resp}, nil
}

type listOperationsQueryInput struct {
	paginationInput
	ShelfID          string `query:"shelf_id"`
	SubjectID        string `query:"subject_id"`
	NoteID           string `query:"note_id"`
	Operation        string `query:"operation"`
	Since            string `query:"since"  doc:"RFC3339"`
	Until            string `query:"until"  doc:"RFC3339"`
	OnlyPendingRecon bool   `query:"only_pending_reconciliation"`
}

func (h *Handler) listOperations(ctx context.Context, input *listOperationsQueryInput) (*operationListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	in := ListOperationsInput{
		Limit:            input.Limit,
		Offset:           input.Offset,
		OnlyPendingRecon: input.OnlyPendingRecon,
	}
	if input.Operation != "" {
		in.Operation = &input.Operation
	}
	if input.ShelfID != "" {
		id, err := uuid.Parse(input.ShelfID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid shelf_id")
		}
		in.ShelfID = &id
	}
	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		in.SubjectID = &id
	}
	if input.NoteID != "" {
		id, err := uuid.Parse(input.NoteID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid note_id")
		}
		in.NoteID = &id
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
	resp, err := h.svc.ListOperations(ctx, clinicID, in)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &operationListHTTPResponse{Body: resp}, nil
}

func (h *Handler) getOperation(ctx context.Context, input *idPathInput) (*operationHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetOperation(ctx, id, clinicID)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &operationHTTPResponse{Body: resp}, nil
}

type listSubjectMedsQueryInput struct {
	paginationInput
	SubjectID string `path:"subject_id"`
}

func (h *Handler) listSubjectMedications(ctx context.Context, input *listSubjectMedsQueryInput) (*operationListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	resp, err := h.svc.ListSubjectMedications(ctx, clinicID, subjectID, staffID, ListOperationsInput{
		Limit:  input.Limit,
		Offset: input.Offset,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &operationListHTTPResponse{Body: resp}, nil
}

// ── Reconciliation ──────────────────────────────────────────────────────────

type startReconciliationBodyInput struct {
	Body struct {
		ShelfID                string   `json:"shelf_id"     minLength:"36"`
		PeriodStart            string   `json:"period_start" doc:"RFC3339"`
		PeriodEnd              string   `json:"period_end"   doc:"RFC3339"`
		PhysicalCount          float64  `json:"physical_count"`
		DiscrepancyExplanation *string  `json:"discrepancy_explanation,omitempty"`
	}
}

type reconciliationHTTPResponse struct {
	Body *ReconciliationResponse
}

type reconciliationListHTTPResponse struct {
	Body *ReconciliationListResponse
}

func (h *Handler) startReconciliation(ctx context.Context, input *startReconciliationBodyInput) (*reconciliationHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	shelfID, err := uuid.Parse(input.Body.ShelfID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid shelf_id")
	}
	periodStart, err := time.Parse(time.RFC3339, input.Body.PeriodStart)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid period_start")
	}
	periodEnd, err := time.Parse(time.RFC3339, input.Body.PeriodEnd)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid period_end")
	}
	resp, err := h.svc.StartReconciliation(ctx, StartReconciliationInput{
		ClinicID:               clinicID,
		StaffID:                staffID,
		ShelfID:                shelfID,
		PeriodStart:            periodStart,
		PeriodEnd:              periodEnd,
		PhysicalCount:          input.Body.PhysicalCount,
		DiscrepancyExplanation: input.Body.DiscrepancyExplanation,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &reconciliationHTTPResponse{Body: resp}, nil
}

func (h *Handler) signSecondaryReconciliation(ctx context.Context, input *idPathInput) (*reconciliationHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.SecondarySignReconciliation(ctx, SecondarySignReconciliationInput{
		ID:       id,
		ClinicID: clinicID,
		StaffID:  staffID,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &reconciliationHTTPResponse{Body: resp}, nil
}

func (h *Handler) reportReconciliation(ctx context.Context, input *idPathInput) (*reconciliationHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.ReportReconciliationToRegulator(ctx, ReportReconciliationInput{
		ID:       id,
		ClinicID: clinicID,
		StaffID:  staffID,
	})
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &reconciliationHTTPResponse{Body: resp}, nil
}

type listReconciliationsQueryInput struct {
	paginationInput
	ShelfID string `query:"shelf_id"`
	Status  string `query:"status"`
	Since   string `query:"since"`
	Until   string `query:"until"`
}

func (h *Handler) listReconciliations(ctx context.Context, input *listReconciliationsQueryInput) (*reconciliationListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	in := ListReconciliationsInput{
		Limit:  input.Limit,
		Offset: input.Offset,
	}
	if input.ShelfID != "" {
		id, err := uuid.Parse(input.ShelfID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid shelf_id")
		}
		in.ShelfID = &id
	}
	if input.Status != "" {
		in.Status = &input.Status
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
	resp, err := h.svc.ListReconciliations(ctx, clinicID, in)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &reconciliationListHTTPResponse{Body: resp}, nil
}

func (h *Handler) getReconciliation(ctx context.Context, input *idPathInput) (*reconciliationHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	id, err := uuid.Parse(input.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	resp, err := h.svc.GetReconciliation(ctx, id, clinicID)
	if err != nil {
		return nil, mapDrugsError(err)
	}
	return &reconciliationHTTPResponse{Body: resp}, nil
}

// ── Error mapping ───────────────────────────────────────────────────────────

func mapDrugsError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		slog.Warn("drugs: not found", "error", err.Error())
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		slog.Warn("drugs: conflict", "error", err.Error())
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		slog.Error("drugs: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

// Compile-time anchor for imports the handler references conditionally.
var (
	_ = http.MethodPost
	_ = json.Marshal
	_ = fmt.Errorf
	_ = uuid.New
)
