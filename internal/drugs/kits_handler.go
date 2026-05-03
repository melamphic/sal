package drugs

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// KitsHandler wires the drug kits endpoints to KitsService.
type KitsHandler struct {
	svc *KitsService
}

// NewKitsHandler constructs a KitsHandler.
func NewKitsHandler(svc *KitsService) *KitsHandler {
	return &KitsHandler{svc: svc}
}

// ── Wire input / output types ────────────────────────────────────────────

type kitItemBody struct {
	CatalogID        *string  `json:"catalog_id,omitempty"`
	OverrideDrugID   *string  `json:"override_drug_id,omitempty"`
	DefaultQuantity  *float64 `json:"default_quantity,omitempty" minimum:"0"`
	Unit             string   `json:"unit" minLength:"1"`
	DefaultDose      *string  `json:"default_dose,omitempty"`
	DefaultRoute     *string  `json:"default_route,omitempty"`
	DefaultOperation *string  `json:"default_operation,omitempty" enum:"administer,dispense,discard,receive,transfer,adjust"`
	Notes            *string  `json:"notes,omitempty"`
	IsOptional       bool     `json:"is_optional,omitempty"`
}

type createKitBody struct {
	Body struct {
		Name        string        `json:"name" minLength:"1" maxLength:"120"`
		Description *string       `json:"description,omitempty" maxLength:"500"`
		UseContext  *string       `json:"use_context,omitempty" maxLength:"60" doc:"Free-form tag, e.g. 'spay', 'dental_prophy', 'discharge', 'comfort_pack', 'vaccine_panel'"`
		Items       []kitItemBody `json:"items"`
	}
}

type updateKitBody struct {
	KitID string `path:"kit_id"`
	Body  struct {
		Name        string  `json:"name" minLength:"1" maxLength:"120"`
		Description *string `json:"description,omitempty" maxLength:"500"`
		UseContext  *string `json:"use_context,omitempty" maxLength:"60"`
	}
}

type replaceKitItemsBody struct {
	KitID string `path:"kit_id"`
	Body  struct {
		Items []kitItemBody `json:"items"`
	}
}

type kitIDPath struct {
	KitID string `path:"kit_id"`
}

type kitHTTPResponse struct {
	Body *KitResponse
}

type kitListHTTPResponse struct {
	Body *KitListResponse
}

// ── Handlers ─────────────────────────────────────────────────────────────

func (h *KitsHandler) createKit(ctx context.Context, in *createKitBody) (*kitHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	items, err := decodeItems(in.Body.Items)
	if err != nil {
		return nil, err
	}
	resp, err := h.svc.CreateKit(ctx, CreateKitInput{
		ClinicID:    clinicID,
		StaffID:     staffID,
		Name:        in.Body.Name,
		Description: in.Body.Description,
		UseContext:  in.Body.UseContext,
		Items:       items,
	})
	if err != nil {
		return nil, mapKitError(err)
	}
	return &kitHTTPResponse{Body: resp}, nil
}

func (h *KitsHandler) listKits(ctx context.Context, _ *struct{}) (*kitListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListKits(ctx, clinicID)
	if err != nil {
		return nil, mapKitError(err)
	}
	return &kitListHTTPResponse{Body: resp}, nil
}

func (h *KitsHandler) getKit(ctx context.Context, in *kitIDPath) (*kitHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	kitID, err := uuid.Parse(in.KitID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid kit_id")
	}
	resp, err := h.svc.GetKit(ctx, kitID, clinicID)
	if err != nil {
		return nil, mapKitError(err)
	}
	return &kitHTTPResponse{Body: resp}, nil
}

func (h *KitsHandler) updateKit(ctx context.Context, in *updateKitBody) (*kitHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	kitID, err := uuid.Parse(in.KitID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid kit_id")
	}
	resp, err := h.svc.UpdateKit(ctx, UpdateKitInput{
		ID:          kitID,
		ClinicID:    clinicID,
		Name:        in.Body.Name,
		Description: in.Body.Description,
		UseContext:  in.Body.UseContext,
	})
	if err != nil {
		return nil, mapKitError(err)
	}
	return &kitHTTPResponse{Body: resp}, nil
}

func (h *KitsHandler) replaceItems(ctx context.Context, in *replaceKitItemsBody) (*kitHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	kitID, err := uuid.Parse(in.KitID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid kit_id")
	}
	items, err := decodeItems(in.Body.Items)
	if err != nil {
		return nil, err
	}
	resp, err := h.svc.ReplaceItems(ctx, kitID, clinicID, items)
	if err != nil {
		return nil, mapKitError(err)
	}
	return &kitHTTPResponse{Body: resp}, nil
}

type kitArchivedHTTPResponse struct {
	Status int
	Body   struct {
		Archived bool `json:"archived"`
	}
}

func (h *KitsHandler) archiveKit(ctx context.Context, in *kitIDPath) (*kitArchivedHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	kitID, err := uuid.Parse(in.KitID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid kit_id")
	}
	if err := h.svc.ArchiveKit(ctx, kitID, clinicID); err != nil {
		return nil, mapKitError(err)
	}
	resp := &kitArchivedHTTPResponse{Status: http.StatusOK}
	resp.Body.Archived = true
	return resp, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

func decodeItems(rows []kitItemBody) ([]KitItemInput, error) {
	out := make([]KitItemInput, len(rows))
	for i, r := range rows {
		var overrideID *uuid.UUID
		if r.OverrideDrugID != nil && *r.OverrideDrugID != "" {
			id, err := uuid.Parse(*r.OverrideDrugID)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid override_drug_id at item " + ".")
			}
			overrideID = &id
		}
		var catalogID *string
		if r.CatalogID != nil && *r.CatalogID != "" {
			c := *r.CatalogID
			catalogID = &c
		}
		out[i] = KitItemInput{
			CatalogID:        catalogID,
			OverrideDrugID:   overrideID,
			DefaultQuantity:  r.DefaultQuantity,
			Unit:             r.Unit,
			DefaultDose:      r.DefaultDose,
			DefaultRoute:     r.DefaultRoute,
			DefaultOperation: r.DefaultOperation,
			Notes:            r.Notes,
			IsOptional:       r.IsOptional,
		}
	}
	return out, nil
}

func mapKitError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("kit not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
