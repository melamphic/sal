package clinic

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires clinic HTTP endpoints to the clinic Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new clinic Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Request / Response types ──────────────────────────────────────────────────

type registerInput struct {
	Body struct {
		Name       string          `json:"name" minLength:"2" maxLength:"200" doc:"The clinic's display name."`
		Email      string          `json:"email" format:"email" doc:"The clinic's primary contact email. Used for billing and admin notifications."`
		Phone      *string         `json:"phone,omitempty" doc:"Clinic phone number."`
		Address    *string         `json:"address,omitempty" doc:"Clinic physical address."`
		Vertical   domain.Vertical `json:"vertical" enum:"veterinary,dental,aged_care" doc:"The clinical domain this clinic operates in."`
		DataRegion string          `json:"data_region" doc:"Where clinic data is stored (e.g. ap-southeast-2, eu-west-2)." default:"ap-southeast-2"`
	}
}

type updateInput struct {
	Body struct {
		Name    *string `json:"name,omitempty" minLength:"2" maxLength:"200" doc:"Updated clinic name."`
		Phone   *string `json:"phone,omitempty" doc:"Updated phone number."`
		Address *string `json:"address,omitempty" doc:"Updated physical address."`
	}
}

type clinicResponse struct {
	Body *ClinicResponse
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// register handles POST /api/v1/clinic/register.
// This is a public endpoint — it creates the clinic and the first super admin.
func (h *Handler) register(ctx context.Context, input *registerInput) (*clinicResponse, error) {
	dto, err := h.svc.Register(ctx, RegisterInput{
		Name:       input.Body.Name,
		Email:      input.Body.Email,
		Phone:      input.Body.Phone,
		Address:    input.Body.Address,
		Vertical:   input.Body.Vertical,
		DataRegion: input.Body.DataRegion,
	})
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// get handles GET /api/v1/clinic.
// Returns the authenticated clinic's details.
func (h *Handler) get(ctx context.Context, _ *struct{}) (*clinicResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	dto, err := h.svc.GetByID(ctx, clinicID)
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// update handles PATCH /api/v1/clinic.
// Updates mutable clinic settings.
func (h *Handler) update(ctx context.Context, input *updateInput) (*clinicResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	dto, err := h.svc.Update(ctx, clinicID, UpdateInput{
		Name:    input.Body.Name,
		Phone:   input.Body.Phone,
		Address: input.Body.Address,
	})
	if err != nil {
		return nil, mapClinicError(err)
	}
	return &clinicResponse{Body: dto}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapClinicError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("clinic not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("a clinic with this email already exists")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
