package staff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/crypto"
	"github.com/melamphic/sal/internal/platform/mailer"
)

// ClinicNameProvider resolves a clinic's display name from its ID.
// Implemented by an adapter in app.go that bridges to clinic.Service.
type ClinicNameProvider interface {
	GetClinicName(ctx context.Context, clinicID uuid.UUID) (string, error)
}

// InviteCreator creates invite tokens for staff invitations.
// Implemented by an adapter in app.go that bridges to auth.Repository.
type InviteCreator interface {
	CreateInvite(ctx context.Context, params CreateInviteTokenParams) (rawToken string, err error)
}

// CreateInviteTokenParams holds the data for creating an invite token.
type CreateInviteTokenParams struct {
	ClinicID    uuid.UUID
	Email       string // plaintext — will be encrypted by the adapter
	Role        domain.StaffRole
	NoteTier    domain.NoteTier
	Permissions domain.Permissions
	InvitedByID uuid.UUID
}

// TierReconciler is the cross-domain hook tier-auto-derivation wires
// in. After every staff invite/create/deactivate that touches a
// note_tier=standard seat, staff.Service calls Reconcile so the
// clinic's Stripe subscription can be flipped between Practice and Pro
// when the headcount crosses the boundary.
//
// Implementations must swallow non-fatal errors (Stripe API hiccups,
// network blips). Only DB-read errors that should 500 the request
// bubble up. Set to nil to disable auto-derivation (tests, local dev).
type TierReconciler interface {
	Reconcile(ctx context.Context, clinicID uuid.UUID) error
}

// AISeatCapResolver looks up the AI-seat ceiling for the supplied
// clinic — i.e. how many `note_tier=standard` staff that clinic's
// current plan permits. Cross-domain port implemented by an adapter
// in app.go that reads clinic.plan_code and consults the
// domain.Plans registry. nil disables enforcement (tests / local dev).
type AISeatCapResolver interface {
	AISeatCap(ctx context.Context, clinicID uuid.UUID) (int, error)
}

// Service handles all staff business logic.
type Service struct {
	repo     repo // interface — see repo.go
	cipher   *crypto.Cipher
	mailer   mailer.Mailer
	appURL   string
	invites  InviteCreator      // nil = invite tokens not created (test mode)
	clinics  ClinicNameProvider // nil = clinic name omitted from emails (test mode)
	tier     TierReconciler     // nil = tier auto-derivation off
	seatCaps AISeatCapResolver  // nil = AI-seat cap enforcement off
}

// NewService creates a new staff Service.
func NewService(repo repo, cipher *crypto.Cipher, m mailer.Mailer, appURL string, invites InviteCreator, clinics ClinicNameProvider) *Service {
	return &Service{repo: repo, cipher: cipher, mailer: m, appURL: appURL, invites: invites, clinics: clinics}
}

// SetTierReconciler wires the cross-domain tier-derivation hook. Called
// from app.go after staff.Service is constructed — keeps NewService
// signature stable.
func (s *Service) SetTierReconciler(t TierReconciler) {
	s.tier = t
}

// SetAISeatCapResolver wires the AI-seat-cap port. nil disables the
// pricing-model-B check, which matches test/local behaviour where no
// plan registry is hooked up.
func (s *Service) SetAISeatCapResolver(r AISeatCapResolver) {
	s.seatCaps = r
}

// checkAISeatCap returns domain.ErrAISeatCapReached if the clinic
// already holds as many `note_tier=standard` seats as its plan allows.
// Called by Invite / Create before any DB write that would push a new
// staff member into the standard tier. No-ops when no resolver is
// wired so unit tests don't need to stub the cross-domain port.
func (s *Service) checkAISeatCap(ctx context.Context, clinicID uuid.UUID, tier domain.NoteTier) error {
	if s.seatCaps == nil || tier != domain.NoteTierStandard {
		return nil
	}
	cap, err := s.seatCaps.AISeatCap(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("staff.service.checkAISeatCap: resolve cap: %w", err)
	}
	if cap <= 0 {
		return nil
	}
	current, err := s.repo.CountStandardActive(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("staff.service.checkAISeatCap: count: %w", err)
	}
	if current >= cap {
		return domain.ErrAISeatCapReached
	}
	return nil
}

// DTO is the decrypted service-layer representation of a staff member.
type StaffResponse struct {
	ID           string             `json:"id"`
	ClinicID     string             `json:"clinic_id"`
	Email        string             `json:"email"`
	FullName     string             `json:"full_name"`
	Role         domain.StaffRole   `json:"role"`
	NoteTier     domain.NoteTier    `json:"note_tier"`
	Permissions  domain.Permissions `json:"permissions"`
	Status       domain.StaffStatus `json:"status"`
	LastActiveAt *time.Time         `json:"last_active_at,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	// Regulatory identity surfaced on every signed clinical record +
	// report PDF that cites this staff member as the clinician of
	// record. Both nullable until the user fills them in via the
	// settings page.
	RegulatoryAuthority *string `json:"regulatory_authority,omitempty"`
	RegulatoryRegNo     *string `json:"regulatory_reg_no,omitempty"`
}

// Page is a paginated list of staff DTOs.
type StaffListResponse struct {
	Items  []*StaffResponse `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// InviteInput holds the data for a staff invitation.
type InviteInput struct {
	Email       string
	FullName    string
	Role        domain.StaffRole
	NoteTier    domain.NoteTier
	Permissions domain.Permissions
	InviterName string
	// SendEmail controls whether the invite email is dispatched.
	// When false the invite is created but no email is sent — the caller is
	// expected to share the returned invite URL out-of-band.
	SendEmail bool
}

// CreateStaffInput is used when a staff member accepts an invite and sets up their account.
type CreateStaffInput struct {
	ClinicID    uuid.UUID
	Email       string
	FullName    string
	Role        domain.StaffRole
	NoteTier    domain.NoteTier
	Permissions domain.Permissions
}

// Invite creates a pending invite token and (optionally) sends the invitation email.
// Always returns the invite URL so callers can display or share it directly.
// Returns domain.ErrConflict if an active staff member with that email already exists in this clinic.
// Returns domain.ErrAISeatCapReached if the new seat would exceed the plan's AI-seat ceiling.
func (s *Service) Invite(ctx context.Context, clinicID, callerID uuid.UUID, in InviteInput) (string, error) {
	emailHash := s.cipher.Hash(in.Email)

	exists, err := s.repo.ExistsByEmailHash(ctx, emailHash, clinicID)
	if err != nil {
		return "", fmt.Errorf("staff.service.Invite: check duplicate: %w", err)
	}
	if exists {
		return "", domain.ErrConflict
	}

	if err := s.checkAISeatCap(ctx, clinicID, in.NoteTier); err != nil {
		return "", err
	}

	// Resolve clinic name for the invitation email.
	var clinicName string
	if s.clinics != nil {
		clinicName, err = s.clinics.GetClinicName(ctx, clinicID)
		if err != nil {
			return "", fmt.Errorf("staff.service.Invite: get clinic name: %w", err)
		}
	}

	// Create invite token so the invited person can verify and accept.
	rawToken, err := s.invites.CreateInvite(ctx, CreateInviteTokenParams{
		ClinicID:    clinicID,
		Email:       in.Email,
		Role:        in.Role,
		NoteTier:    in.NoteTier,
		Permissions: in.Permissions,
		InvitedByID: callerID,
	})
	if err != nil {
		return "", fmt.Errorf("staff.service.Invite: create invite token: %w", err)
	}

	inviteURL := fmt.Sprintf("%s/invite/accept?token=%s", s.appURL, rawToken)

	if in.SendEmail {
		if err := s.mailer.SendInvite(ctx, in.Email, in.InviterName, clinicName, inviteURL); err != nil {
			return "", fmt.Errorf("staff.service.Invite: send invite: %w", err)
		}
	}

	// Tier auto-derivation kicks in only when the new seat is `standard` —
	// nurse/none seats don't count toward the Practice/Pro boundary.
	if in.NoteTier == domain.NoteTierStandard {
		s.reconcileTier(ctx, clinicID)
	}

	return inviteURL, nil
}

// Create inserts a new staff member from an accepted invite.
// Called by the auth module when an invite token is verified.
// Returns domain.ErrAISeatCapReached if the new seat would exceed the
// plan's AI-seat ceiling (e.g. plan was downgraded between invite and
// acceptance).
func (s *Service) Create(ctx context.Context, in CreateStaffInput) (*StaffResponse, error) {
	if err := s.checkAISeatCap(ctx, in.ClinicID, in.NoteTier); err != nil {
		return nil, err
	}

	encEmail, err := s.cipher.Encrypt(in.Email)
	if err != nil {
		return nil, fmt.Errorf("staff.service.Create: encrypt email: %w", err)
	}

	encName, err := s.cipher.Encrypt(in.FullName)
	if err != nil {
		return nil, fmt.Errorf("staff.service.Create: encrypt name: %w", err)
	}

	p := CreateParams{
		ID:        domain.NewID(),
		ClinicID:  in.ClinicID,
		Email:     encEmail,
		EmailHash: s.cipher.Hash(in.Email),
		FullName:  encName,
		Role:      in.Role,
		NoteTier:  in.NoteTier,
		Perms:     in.Permissions,
		Status:    domain.StaffStatusActive,
	}

	row, err := s.repo.Create(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("staff.service.Create: %w", err)
	}

	if in.NoteTier == domain.NoteTierStandard {
		s.reconcileTier(ctx, in.ClinicID)
	}

	return s.toDTO(row, in.Email, in.FullName), nil
}

// EnsureOwner finds-or-creates the super-admin staff for a clinic during /mel
// handoff provisioning. Idempotent on (email_hash, clinic_id) — replaying the
// same email returns the existing row. Bypasses the invite flow: no token,
// no email, staff is active immediately so the handoff can mint a session.
func (s *Service) EnsureOwner(ctx context.Context, clinicID uuid.UUID, email, fullName string) (*StaffResponse, error) {
	emailHash := s.cipher.Hash(email)

	existing, err := s.repo.GetByEmailHash(ctx, emailHash, clinicID)
	if err == nil {
		return s.decryptAndBuild(existing)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("staff.service.EnsureOwner: lookup: %w", err)
	}

	return s.Create(ctx, CreateStaffInput{
		ClinicID:    clinicID,
		Email:       email,
		FullName:    fullName,
		Role:        domain.StaffRoleSuperAdmin,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(domain.StaffRoleSuperAdmin),
	})
}

// GetByID returns decrypted staff details.
func (s *Service) GetByID(ctx context.Context, staffID, clinicID uuid.UUID) (*StaffResponse, error) {
	row, err := s.repo.GetByID(ctx, staffID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("staff.service.GetByID: %w", err)
	}
	return s.decryptAndBuild(row)
}

// List returns a paginated, decrypted list of staff members.
func (s *Service) List(ctx context.Context, clinicID uuid.UUID, limit, offset int) (*StaffListResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	rows, total, err := s.repo.List(ctx, clinicID, ListParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("staff.service.List: %w", err)
	}

	dtos := make([]*StaffResponse, 0, len(rows))
	for _, row := range rows {
		dto, err := s.decryptAndBuild(row)
		if err != nil {
			return nil, fmt.Errorf("staff.service.List: decrypt: %w", err)
		}
		dtos = append(dtos, dto)
	}

	return &StaffListResponse{
		Items:  dtos,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// UpdatePermissions updates a staff member's capability flags.
// Only super_admin may grant manage_billing or rollback_policies.
func (s *Service) UpdatePermissions(ctx context.Context, staffID, clinicID uuid.UUID, callerRole domain.StaffRole, perms domain.Permissions) (*StaffResponse, error) {
	// Guard: only super_admin can grant billing or policy rollback.
	if callerRole != domain.StaffRoleSuperAdmin {
		perms.ManageBilling = false
		perms.RollbackPolicies = false
	}

	row, err := s.repo.UpdatePermissions(ctx, staffID, clinicID, UpdatePermsParams{Perms: perms})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff.service.UpdatePermissions: %w", err)
	}

	return s.decryptAndBuild(row)
}

// CountStandardActive returns the count of active+invited staff in a
// clinic whose note_tier is `standard`. Cross-domain entrypoint used by
// the tiering module to derive Practice/Pro from headcount. Never
// called from HTTP handlers.
func (s *Service) CountStandardActive(ctx context.Context, clinicID uuid.UUID) (int, error) {
	n, err := s.repo.CountStandardActive(ctx, clinicID)
	if err != nil {
		return 0, fmt.Errorf("staff.service.CountStandardActive: %w", err)
	}
	return n, nil
}

// AISeatUsage is the {used, cap} pair returned by GetAISeatUsage.
// Renders directly into the dashboard's seat-usage widget and the
// settings team page's seat-bar. Cap=0 means no resolver is wired
// (test mode) and the UI should hide the meter.
type AISeatUsage struct {
	Used int `json:"used"`
	Cap  int `json:"cap"`
}

// GetAISeatUsage reports how many AI seats (note_tier=standard) the
// clinic currently uses + the cap their plan permits. Cheap: 1 SQL +
// 1 plan-registry lookup; safe to call on every dashboard refresh.
func (s *Service) GetAISeatUsage(ctx context.Context, clinicID uuid.UUID) (AISeatUsage, error) {
	used, err := s.repo.CountStandardActive(ctx, clinicID)
	if err != nil {
		return AISeatUsage{}, fmt.Errorf("staff.service.GetAISeatUsage: count: %w", err)
	}
	cap := 0
	if s.seatCaps != nil {
		c, err := s.seatCaps.AISeatCap(ctx, clinicID)
		if err != nil {
			return AISeatUsage{}, fmt.Errorf("staff.service.GetAISeatUsage: cap: %w", err)
		}
		cap = c
	}
	return AISeatUsage{Used: used, Cap: cap}, nil
}

// Deactivate marks a staff member as deactivated. Cannot deactivate the caller's own account.
func (s *Service) Deactivate(ctx context.Context, staffID, clinicID, callerID uuid.UUID) (*StaffResponse, error) {
	if staffID == callerID {
		return nil, fmt.Errorf("staff.service.Deactivate: %w", domain.ErrForbidden)
	}

	row, err := s.repo.Deactivate(ctx, staffID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("staff.service.Deactivate: %w", err)
	}

	if row.NoteTier == domain.NoteTierStandard {
		s.reconcileTier(ctx, clinicID)
	}

	return s.decryptAndBuild(row)
}

// ── Private helpers ───────────────────────────────────────────────────────────

// reconcileTier invokes the cross-domain tier reconciler if one is wired.
// Best-effort: errors are swallowed so a Stripe blip can't fail a staff
// mutation. The caller has already committed the underlying staff row.
func (s *Service) reconcileTier(ctx context.Context, clinicID uuid.UUID) {
	if s.tier == nil {
		return
	}
	_ = s.tier.Reconcile(ctx, clinicID)
}

func (s *Service) decryptAndBuild(row *StaffRecord) (*StaffResponse, error) {
	email, err := s.cipher.Decrypt(row.Email)
	if err != nil {
		return nil, fmt.Errorf("staff.service: decrypt email: %w", err)
	}

	name, err := s.cipher.Decrypt(row.FullName)
	if err != nil {
		return nil, fmt.Errorf("staff.service: decrypt name: %w", err)
	}

	return s.toDTO(row, email, name), nil
}

func (s *Service) toDTO(row *StaffRecord, email, fullName string) *StaffResponse {
	return &StaffResponse{
		ID:                  row.ID.String(),
		ClinicID:            row.ClinicID.String(),
		Email:               email,
		FullName:            fullName,
		Role:                row.Role,
		NoteTier:            row.NoteTier,
		Permissions:         row.Perms,
		Status:              row.Status,
		LastActiveAt:        row.LastActiveAt,
		CreatedAt:           row.CreatedAt,
		RegulatoryAuthority: row.RegulatoryAuthority,
		RegulatoryRegNo:     row.RegulatoryRegNo,
	}
}

// UpdateRegulatoryIdentity sets (or clears) the regulator authority +
// reg-no for a staff member. Pass nil for either to clear it.
func (s *Service) UpdateRegulatoryIdentity(ctx context.Context, staffID, clinicID uuid.UUID, authority, regNo *string) (*StaffResponse, error) {
	row, err := s.repo.UpdateRegulatoryIdentity(ctx, staffID, clinicID, authority, regNo)
	if err != nil {
		return nil, fmt.Errorf("staff.service.UpdateRegulatoryIdentity: %w", err)
	}
	return s.decryptAndBuild(row)
}
