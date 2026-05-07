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
		OperationID: "get-ai-seat-usage",
		Method:      http.MethodGet,
		Path:        "/api/v1/staff/seats",
		Summary:     "AI seat usage for the current clinic",
		Description: "Returns {used, cap} where `used` is the number of staff with note_tier=standard and `cap` is the plan's AI-seat ceiling (Practice=3, Pro=7). Cap=0 means enforcement is disabled (test/local).",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth},
	}, h.seatUsage)

	huma.Register(api, huma.Operation{
		OperationID: "list-staff-invites",
		Method:      http.MethodGet,
		Path:        "/api/v1/staff/invites",
		Summary:     "List pending + expired staff invitations",
		Description: "Returns invites for the clinic that haven't been accepted, with derived status (pending|expired). Revoked rows are filtered out at the repo layer; accepted invites already appear in /staff. Requires manage_staff.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.listInvites)

	huma.Register(api, huma.Operation{
		OperationID: "resend-staff-invite",
		Method:      http.MethodPost,
		Path:        "/api/v1/staff/invites/{invite_id}/resend",
		Summary:     "Resend (re-mint) a staff invitation",
		Description: "Mints a fresh invite token with the same role/perms/email and revokes the previous row, so any link still in the invitee's inbox stops working. Returns the new invite_url. Optionally re-emails the invitee.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.resendInvite)

	huma.Register(api, huma.Operation{
		OperationID: "get-staff-activity",
		Method:      http.MethodGet,
		Path:        "/api/v1/staff/{staff_id}/activity",
		Summary:     "Per-staff activity feed",
		Description: "Merged cross-domain activity (notes, drugs, incidents, consent, pain, logins) for one staff member, newest-first. Sources fan out in parallel server-side. Requires manage_staff.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.getActivity)

	huma.Register(api, huma.Operation{
		OperationID: "revoke-staff-invite",
		Method:      http.MethodDelete,
		Path:        "/api/v1/staff/invites/{invite_id}",
		Summary:     "Revoke a pending staff invitation",
		Description: "Stamps revoked_at on the invite row so the link in the invitee's inbox stops working. Idempotent: returns 404 if the invite is already accepted, already revoked, or in a different clinic.",
		Tags:        []string{"Staff"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		Middlewares: huma.Middlewares{auth, manageStaff},
	}, h.revokeInvite)

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
