package dashboard

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers the single dashboard route. Auth required (clinic
// scope comes from JWT); no permission gate — every staff member sees
// the dashboard, individual widgets self-gate via the snapshot's
// shape (e.g. SeatUsage hidden when Cap=0).
func (h *Handler) Mount(api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)

	huma.Register(api, huma.Operation{
		OperationID: "get-dashboard-snapshot",
		Method:      http.MethodGet,
		Path:        "/api/v1/clinic/dashboard/snapshot",
		Summary:     "Vertical-aware dashboard snapshot",
		Description: "Returns a single payload covering KPI strip, vertical action card, watchcards, AI seat usage, drafts count, and the recent-activity feed. Cached in-process for 60s per clinic — write paths invalidate so post-action refreshes are immediate. Use the response's ttl_seconds to schedule the next poll on the client.",
		Tags:        []string{"Dashboard"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.snapshot)
}
