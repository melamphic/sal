package admin

import (
	"context"
	"errors"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

type dashboardEmptyInput struct{}

type dashboardHTTPResponse struct {
	Body *DashboardResponse
}

func (h *Handler) getDashboard(ctx context.Context, _ *dashboardEmptyInput) (*dashboardHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	resp, err := h.svc.GetDashboard(ctx, clinicID, staffID)
	if err != nil {
		return nil, mapAdminError(err)
	}
	return &dashboardHTTPResponse{Body: resp}, nil
}

type alertsHTTPResponse struct {
	Body *AlertsResponse
}

func (h *Handler) getAlerts(ctx context.Context, _ *dashboardEmptyInput) (*alertsHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	resp, err := h.svc.GetAlerts(ctx, clinicID, staffID)
	if err != nil {
		return nil, mapAdminError(err)
	}
	return &alertsHTTPResponse{Body: resp}, nil
}

func mapAdminError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		slog.Error("admin: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}
