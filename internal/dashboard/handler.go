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

// snapshotInput allows the client to request a cache-bypass via
// ?fresh=true. The frontend uses this after local actions (note
// submitted, drug op logged, etc.) where it knows the snapshot
// should reflect a write the user just performed. Idle dashboards
// poll without the flag and ride the 60s TTL.
type snapshotInput struct {
	Fresh bool `query:"fresh" doc:"When true, bypass the in-process cache and force a fresh DB read. Use after local writes to surface them immediately; otherwise the 60s TTL handles refresh."`
}

// snapshotResponse returns the JSON payload + a Cache-Control hint
// pointing at TTLSeconds so the client can schedule the next poll.
type snapshotResponse struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

// snapshot handles GET /api/v1/clinic/dashboard/snapshot.
func (h *Handler) snapshot(ctx context.Context, input *snapshotInput) (*snapshotResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	if input != nil && input.Fresh {
		h.svc.Invalidate(clinicID)
	}

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
