package staff

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires staff HTTP endpoints to the staff Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new staff Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Request / Response types ──────────────────────────────────────────────────

type inviteInput struct {
	Body struct {
		Email       string             `json:"email" format:"email" doc:"Email address of the person being invited."`
		FullName    string             `json:"full_name" minLength:"2" maxLength:"200" doc:"Full name of the new staff member."`
		Role        domain.StaffRole   `json:"role" enum:"admin,vet,vet_nurse,receptionist" doc:"The role determines default permission set and UI access."`
		NoteTier    domain.NoteTier    `json:"note_tier" enum:"standard,nurse,none" doc:"Billing tier. standard = counts toward clinic tier. nurse = 50% quota, no tier impact."`
		Permissions domain.Permissions `json:"permissions" doc:"Permission flags. Role defaults are pre-applied — override here as needed."`
		SendEmail   *bool              `json:"send_email,omitempty" doc:"When false the invite email is not sent — caller is expected to share the returned invite_url out-of-band. Defaults to true."`
	}
}

type inviteResponse struct {
	Body struct {
		InviteURL string `json:"invite_url" doc:"URL the invited person opens to accept and set up their account."`
	}
}

type listStaffInput struct {
	Limit  int `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0" doc:"Number of results to skip."`
}

type staffIDInput struct {
	StaffID string `path:"staff_id" doc:"The staff member's UUID."`
}

type updatePermsInput struct {
	StaffID string `path:"staff_id" doc:"The staff member's UUID."`
	Body    struct {
		Permissions domain.Permissions `json:"permissions" doc:"Updated permission flags."`
	}
}

// updateRegulatoryInput drives PATCH /staff/{staff_id}/regulatory-id.
// Both fields are nullable — passing nil clears the column.
type updateRegulatoryInput struct {
	StaffID string `path:"staff_id" doc:"The staff member's UUID."`
	Body    struct {
		RegulatoryAuthority *string `json:"regulatory_authority,omitempty" maxLength:"40" doc:"Authority code: VCNZ | NMC | GMC | AHPRA | AVMA | RCVS | …"`
		RegulatoryRegNo     *string `json:"regulatory_reg_no,omitempty"     maxLength:"60" doc:"Registration / membership identifier issued by the authority."`
	}
}

type staffResponse struct {
	Body *StaffResponse
}

type staffListResponse struct {
	Body *StaffListResponse
}

// staffSeatUsageResponse wraps AISeatUsage for the GET /staff/seats
// endpoint. Public so other packages (dashboard) can consume the same
// JSON shape via a typed Go client if needed.
type staffSeatUsageResponse struct {
	Body *AISeatUsage
}

// ── Invite management types ───────────────────────────────────────────────────

// StaffInviteResponse is the JSON shape one invite renders into for
// GET /api/v1/staff/invites and the resend response. Unique name +
// staff prefix per project rule (huma OpenAPI registry).
type StaffInviteResponse struct {
	ID            string             `json:"id"`
	Email         string             `json:"email"`
	Role          domain.StaffRole   `json:"role"`
	NoteTier      domain.NoteTier    `json:"note_tier"`
	Permissions   domain.Permissions `json:"permissions"`
	InvitedByID   string             `json:"invited_by_id"`
	InvitedByName string             `json:"invited_by_name,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	ExpiresAt     time.Time          `json:"expires_at"`
	AcceptedAt    *time.Time         `json:"accepted_at,omitempty"`
	RevokedAt     *time.Time         `json:"revoked_at,omitempty"`
	Status        string             `json:"status" enum:"pending,expired,accepted,revoked"`
}

// StaffInviteListResponse is the list-endpoint envelope.
type StaffInviteListResponse struct {
	Items []StaffInviteResponse `json:"items"`
}

type listInvitesInput struct{}

type listInvitesHTTPResponse struct {
	Body *StaffInviteListResponse
}

type inviteIDInput struct {
	InviteID string `path:"invite_id" doc:"The invite token's UUID."`
}

type resendInviteInput struct {
	InviteID string `path:"invite_id" doc:"The invite token's UUID."`
	Body     struct {
		SendEmail *bool `json:"send_email,omitempty" doc:"When false the invite email is not sent — caller is expected to share the returned invite_url out-of-band. Defaults to true."`
	}
}

type resendInviteHTTPResponse struct {
	Body struct {
		InviteURL string `json:"invite_url" doc:"Fresh URL the invited person opens to accept and set up their account."`
	}
}

type revokeInviteHTTPResponse struct {
	Body struct {
		Revoked bool `json:"revoked"`
	}
}

// StaffActivityEventResponse is the wire shape for one row in the
// merged per-staff activity feed. Source identifies which domain it
// came from; Kind is a dotted slug the FE pattern-matches on.
type StaffActivityEventResponse struct {
	ID         string    `json:"id"`
	Source     string    `json:"source" enum:"notes,drugs,incidents,consent,pain,auth"`
	Kind       string    `json:"kind"`
	OccurredAt time.Time `json:"occurred_at"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle,omitempty"`
	NoteID     *string   `json:"note_id,omitempty"`
	SubjectID  *string   `json:"subject_id,omitempty"`
	EntityID   *string   `json:"entity_id,omitempty"`
}

// StaffActivityListResponse is the paginated envelope.
type StaffActivityListResponse struct {
	Items []StaffActivityEventResponse `json:"items"`
}

type staffActivityInput struct {
	StaffID string `path:"staff_id" doc:"Staff member's UUID."`
	Limit   int    `query:"limit"  minimum:"1" maximum:"100" default:"50" doc:"Events per page."`
	Offset  int    `query:"offset" minimum:"0" default:"0" doc:"Events to skip."`
}

type staffActivityHTTPResponse struct {
	Body *StaffActivityListResponse
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// invite handles POST /api/v1/staff/invite.
func (h *Handler) invite(ctx context.Context, input *inviteInput) (*inviteResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	// Fetch caller's full name for the invite email.
	caller, err := h.svc.GetByID(ctx, callerID, clinicID)
	if err != nil {
		return nil, huma.Error500InternalServerError("internal server error")
	}

	// Use role defaults and merge with explicitly provided permissions.
	defaults := domain.DefaultPermissions(input.Body.Role)
	perms := mergePerms(defaults, input.Body.Permissions)

	sendEmail := true
	if input.Body.SendEmail != nil {
		sendEmail = *input.Body.SendEmail
	}

	url, err := h.svc.Invite(ctx, clinicID, callerID, InviteInput{
		Email:       input.Body.Email,
		FullName:    input.Body.FullName,
		Role:        input.Body.Role,
		NoteTier:    input.Body.NoteTier,
		Permissions: perms,
		InviterName: caller.FullName,
		SendEmail:   sendEmail,
	})
	if err != nil {
		return nil, mapStaffError(err)
	}

	resp := &inviteResponse{}
	resp.Body.InviteURL = url
	return resp, nil
}

// list handles GET /api/v1/staff.
func (h *Handler) list(ctx context.Context, input *listStaffInput) (*staffListResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	page, err := h.svc.List(ctx, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, huma.Error500InternalServerError("internal server error")
	}

	return &staffListResponse{Body: page}, nil
}

// getMe handles GET /api/v1/staff/me — returns the authenticated staff member's profile.
func (h *Handler) getMe(ctx context.Context, _ *struct{}) (*staffResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	dto, err := h.svc.GetByID(ctx, staffID, clinicID)
	if err != nil {
		return nil, mapStaffError(err)
	}

	return &staffResponse{Body: dto}, nil
}

// get handles GET /api/v1/staff/{staff_id}.
func (h *Handler) get(ctx context.Context, input *staffIDInput) (*staffResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}

	dto, err := h.svc.GetByID(ctx, staffID, clinicID)
	if err != nil {
		return nil, mapStaffError(err)
	}

	return &staffResponse{Body: dto}, nil
}

// updatePermissions handles PATCH /api/v1/staff/{staff_id}/permissions.
func (h *Handler) updatePermissions(ctx context.Context, input *updatePermsInput) (*staffResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerRole := mw.RoleFromContext(ctx)

	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}

	dto, err := h.svc.UpdatePermissions(ctx, staffID, clinicID, callerRole, input.Body.Permissions)
	if err != nil {
		return nil, mapStaffError(err)
	}

	return &staffResponse{Body: dto}, nil
}

// updateRegulatoryIdentity handles PATCH /api/v1/staff/{staff_id}/regulatory-id.
// Sets (or clears) the regulator authority + reg-no on the staff
// member — surfaces on every signed clinical record + report PDF that
// cites this staff as the clinician of record.
func (h *Handler) updateRegulatoryIdentity(ctx context.Context, input *updateRegulatoryInput) (*staffResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}
	dto, err := h.svc.UpdateRegulatoryIdentity(ctx, staffID, clinicID, input.Body.RegulatoryAuthority, input.Body.RegulatoryRegNo)
	if err != nil {
		return nil, mapStaffError(err)
	}
	return &staffResponse{Body: dto}, nil
}

// seatUsage handles GET /api/v1/staff/seats — returns the AI-seat
// counter for the current clinic. Cheap (1 SQL + 1 plan-registry
// lookup); safe to call on every dashboard refresh.
func (h *Handler) seatUsage(ctx context.Context, _ *struct{}) (*staffSeatUsageResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	usage, err := h.svc.GetAISeatUsage(ctx, clinicID)
	if err != nil {
		return nil, huma.Error500InternalServerError("internal server error")
	}
	return &staffSeatUsageResponse{Body: &usage}, nil
}

// listInvites handles GET /api/v1/staff/invites.
func (h *Handler) listInvites(ctx context.Context, _ *listInvitesInput) (*listInvitesHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	rows, err := h.svc.ListInvites(ctx, clinicID)
	if err != nil {
		return nil, mapStaffError(err)
	}
	items := make([]StaffInviteResponse, 0, len(rows))
	for _, r := range rows {
		items = append(items, inviteToDTO(r))
	}
	return &listInvitesHTTPResponse{Body: &StaffInviteListResponse{Items: items}}, nil
}

// resendInvite handles POST /api/v1/staff/invites/{invite_id}/resend.
// Mints a new token, revokes the old row, returns the new URL.
func (h *Handler) resendInvite(ctx context.Context, input *resendInviteInput) (*resendInviteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)
	inviteID, err := uuid.Parse(input.InviteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid invite_id")
	}
	sendEmail := true
	if input.Body.SendEmail != nil {
		sendEmail = *input.Body.SendEmail
	}
	url, err := h.svc.ResendInvite(ctx, inviteID, clinicID, callerID, sendEmail)
	if err != nil {
		return nil, mapStaffError(err)
	}
	resp := &resendInviteHTTPResponse{}
	resp.Body.InviteURL = url
	return resp, nil
}

// revokeInvite handles DELETE /api/v1/staff/invites/{invite_id}.
func (h *Handler) revokeInvite(ctx context.Context, input *inviteIDInput) (*revokeInviteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	inviteID, err := uuid.Parse(input.InviteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid invite_id")
	}
	if err := h.svc.RevokeInvite(ctx, inviteID, clinicID); err != nil {
		return nil, mapStaffError(err)
	}
	resp := &revokeInviteHTTPResponse{}
	resp.Body.Revoked = true
	return resp, nil
}

// getActivity handles GET /api/v1/staff/{staff_id}/activity.
// Returns the merged cross-domain activity feed (notes, drugs,
// incidents, consent, pain, logins) for one staff member, newest-
// first. Requires manage_staff.
func (h *Handler) getActivity(ctx context.Context, input *staffActivityInput) (*staffActivityHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}
	events, err := h.svc.GetActivity(ctx, staffID, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, mapStaffError(err)
	}
	items := make([]StaffActivityEventResponse, 0, len(events))
	for _, e := range events {
		items = append(items, StaffActivityEventResponse(e))
	}
	return &staffActivityHTTPResponse{Body: &StaffActivityListResponse{Items: items}}, nil
}

// inviteToDTO projects the service-level entry into the wire shape.
func inviteToDTO(r InviteListEntry) StaffInviteResponse {
	return StaffInviteResponse{
		ID:            r.ID.String(),
		Email:         r.Email,
		Role:          r.Role,
		NoteTier:      r.NoteTier,
		Permissions:   r.Permissions,
		InvitedByID:   r.InvitedByID.String(),
		InvitedByName: r.InvitedByName,
		CreatedAt:     r.CreatedAt,
		ExpiresAt:     r.ExpiresAt,
		AcceptedAt:    r.AcceptedAt,
		RevokedAt:     r.RevokedAt,
		Status:        r.Status,
	}
}

// deactivate handles DELETE /api/v1/staff/{staff_id}.
func (h *Handler) deactivate(ctx context.Context, input *staffIDInput) (*staffResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	staffID, err := uuid.Parse(input.StaffID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid staff_id")
	}

	dto, err := h.svc.Deactivate(ctx, staffID, clinicID, callerID)
	if err != nil {
		return nil, mapStaffError(err)
	}

	return &staffResponse{Body: dto}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// mergePerms applies explicitly-set permissions on top of role defaults.
// A permission is only overridden if the explicit value is true (i.e. granting extra access).
func mergePerms(defaults, explicit domain.Permissions) domain.Permissions {
	return domain.Permissions{
		ManageStaff:         defaults.ManageStaff || explicit.ManageStaff,
		ManageForms:         defaults.ManageForms || explicit.ManageForms,
		ManagePolicies:      defaults.ManagePolicies || explicit.ManagePolicies,
		ManageBilling:       defaults.ManageBilling || explicit.ManageBilling,
		RollbackPolicies:    defaults.RollbackPolicies || explicit.RollbackPolicies,
		RecordAudio:         defaults.RecordAudio || explicit.RecordAudio,
		SubmitForms:         defaults.SubmitForms || explicit.SubmitForms,
		ViewAllPatients:     defaults.ViewAllPatients || explicit.ViewAllPatients,
		ViewOwnPatients:     defaults.ViewOwnPatients || explicit.ViewOwnPatients,
		Dispense:            defaults.Dispense || explicit.Dispense,
		GenerateAuditExport: defaults.GenerateAuditExport || explicit.GenerateAuditExport,
		ManagePatients:      defaults.ManagePatients || explicit.ManagePatients,
	}
}

func mapStaffError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("staff member not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("a staff member with this email already exists in this clinic")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrAISeatCapReached):
		// 402 Payment Required — semantically "your plan is in the way";
		// the UI surfaces an upgrade CTA instead of a generic validation
		// error.
		return huma.NewError(402, "ai seat cap reached — upgrade your plan to add more recording seats")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
