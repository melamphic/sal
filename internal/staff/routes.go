package staff

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all staff routes onto the provided Chi router.
// All staff routes require authentication.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manageStaff := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageStaff })

	huma.Register(api, huma.Operation{
		OperationID:   "invite-staff",
		Method:        http.MethodPost,
		Path:          "/api/v1/staff/invite",
		Summary:       "Invite a staff member",
		Description:   "Sends an invitation email to the specified address. Requires manage_staff permission.",
		Tags:          []string{"Staff"},
		Security:      []map[string][]string{{"bearerAuth": {}}},
		DefaultStatus: http.StatusAccepted,
		Middlewares:   huma.Middlewares{auth, manageStaff},
	}, h.invite)

	huma.Register(api, huma.Operation{
		OperationID: "list-staff",
		Method:      http.MethodGet,
		Path:        "/api/v1/staff",
		Summary:     "List staff members",
		Description: "Returns a paginated list of all staff members in the clinic.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-current-staff",
		Method:      http.MethodGet,
		Path:        "/api/v1/staff/me",
		Summary:     "Get the authenticated staff member",
		Description: "Returns the profile and permissions for the staff member identified by the bearer token.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.getMe)

	huma.Register(api, huma.Operation{
		OperationID: "get-staff-member",
		Method:      http.MethodGet,
		Path:        "/api/v1/staff/{staff_id}",
		Summary:     "Get a staff member",
		Description: "Returns profile and permissions for a single staff member.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "update-staff-permissions",
		Method:      http.MethodPatch,
		Path:        "/api/v1/staff/{staff_id}/permissions",
		Summary:     "Update staff permissions",
		Description: "Updates the permission flags for a staff member. Only super_admin can grant manage_billing or rollback_policies.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.updatePermissions)

	huma.Register(api, huma.Operation{
		OperationID: "update-staff-regulatory-id",
		Method:      http.MethodPatch,
		Path:        "/api/v1/staff/{staff_id}/regulatory-id",
		Summary:     "Update staff regulatory identity",
		Description: "Sets or clears the regulator authority + registration number for a staff member (VCNZ / NMC / GMC / AHPRA / AVMA / RCVS / etc.). Surfaces on every signed clinical record + report PDF that cites this staff as the clinician of record.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.updateRegulatoryIdentity)

	huma.Register(api, huma.Operation{
		OperationID: "deactivate-staff",
		Method:      http.MethodDelete,
		Path:        "/api/v1/staff/{staff_id}",
		Summary:     "Deactivate a staff member",
		Description: "Marks a staff member as deactivated. Records preserved for audit trail integrity. Cannot deactivate your own account.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.deactivate)
}
