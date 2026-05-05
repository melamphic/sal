package dashboard

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler exposes the dashboard snapshot endpoint. One method, one
// route — every widget on the home page reads from this single
// payload. Cache hits are cheap (sync.Map read + per-staff drafts
// COUNT); cache misses trigger one coordinated build via singleflight.
type Handler struct {
	svc *Service
}

// NewHandler wires the dashboard handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// snapshotResponse returns the JSON payload + a strong ETag derived
// from FetchedAt so the client can short-circuit on a still-fresh
// snapshot. Cache-Control hints at TTLSeconds for browsers /
// intermediaries.
type snapshotResponse struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

// snapshot handles GET /api/v1/clinic/dashboard/snapshot.
func (h *Handler) snapshot(ctx context.Context, _ *struct{}) (*snapshotResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	body, err := h.svc.SnapshotJSON(ctx, clinicID, staffID)
	if err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("dashboard snapshot: %v", err))
	}

	return &snapshotResponse{
		ContentType:  "application/json",
		CacheControl: fmt.Sprintf("private, max-age=%d", int(DefaultTTL.Seconds())),
		Body:         body,
	}, nil
}
