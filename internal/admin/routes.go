package admin

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers the admin dashboard endpoint.
//
// Permission: ManageStaff OR ManageBilling — admin-grade visibility.
// The dashboard exposes counts + alerts across compliance modules; we
// don't want non-admin staff to see at-a-glance regulator-overdue figures.
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	admin := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManageStaff || p.ManageBilling
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "get-admin-dashboard",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/dashboard",
		Summary:     "Live admin dashboard (subjects · drugs · incidents · consent · pain)",
		Description: "Returns aggregated counts + alerts for the last 30 days plus current state. Universal across all (vertical, country) combos — the same payload shape ships everywhere; UI hides cards that don't apply via empty-state rendering.",
		Tags:        []string{"Admin"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, admin},
	}, h.getDashboard)

	huma.Register(api, huma.Operation{
		OperationID: "get-admin-alerts",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/alerts",
		Summary:     "Actionable alerts (regulator deadlines · drug par · expiring stock · consent renewals · reconciliation discrepancies)",
		Description: "Same data sources as the dashboard, filtered to items that need attention right now. Powers the in-app notification rail and the (D2) email digest.",
		Tags:        []string{"Admin"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, admin},
	}, h.getAlerts)
}
