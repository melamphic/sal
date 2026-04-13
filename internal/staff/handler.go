package staff

import (
	"context"
	"errors"

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

type staffResponse struct {
	Body *StaffDTO
}

type staffListResponse struct {
	Body *StaffPage
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// invite handles POST /api/v1/staff/invite.
func (h *Handler) invite(ctx context.Context, input *inviteInput) (*struct{}, error) {
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

	if err := h.svc.Invite(ctx, clinicID, InviteInput{
		Email:       input.Body.Email,
		FullName:    input.Body.FullName,
		Role:        input.Body.Role,
		NoteTier:    input.Body.NoteTier,
		Permissions: perms,
		InviterName: caller.FullName,
		ClinicName:  clinicID.String(), // TODO: pass clinic name through context in Phase 0 follow-up
	}); err != nil {
		return nil, mapStaffError(err)
	}

	return nil, nil
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
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
