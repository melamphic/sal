package verticals

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires verticals HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new verticals Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// VerticalSchemaResponse wraps a VerticalSchema payload for huma.
// Named with the package prefix so it is globally unique across the
// OpenAPI schema registry (see CLAUDE.md huma gotchas).
type VerticalSchemaResponse struct {
	Body *domain.VerticalSchema
}

// getSchema handles GET /api/v1/verticals/schema.
// Returns the form schema for the authenticated clinic's vertical so
// the client can render Create/View/Edit forms generically.
func (h *Handler) getSchema(ctx context.Context, _ *struct{}) (*VerticalSchemaResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	schema, err := h.svc.SchemaForClinic(ctx, clinicID)
	if err != nil {
		return nil, mapVerticalsError(err)
	}
	return &VerticalSchemaResponse{Body: schema}, nil
}

func mapVerticalsError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("vertical schema not registered")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
