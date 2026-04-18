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

// Service handles all staff business logic.
type Service struct {
	repo    repo // interface — see repo.go
	cipher  *crypto.Cipher
	mailer  mailer.Mailer
	appURL  string
	invites InviteCreator      // nil = invite tokens not created (test mode)
	clinics ClinicNameProvider // nil = clinic name omitted from emails (test mode)
}

// NewService creates a new staff Service.
func NewService(repo repo, cipher *crypto.Cipher, m mailer.Mailer, appURL string, invites InviteCreator, clinics ClinicNameProvider) *Service {
	return &Service{repo: repo, cipher: cipher, mailer: m, appURL: appURL, invites: invites, clinics: clinics}
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
func (s *Service) Invite(ctx context.Context, clinicID, callerID uuid.UUID, in InviteInput) (string, error) {
	emailHash := s.cipher.Hash(in.Email)

	exists, err := s.repo.ExistsByEmailHash(ctx, emailHash, clinicID)
	if err != nil {
		return "", fmt.Errorf("staff.service.Invite: check duplicate: %w", err)
	}
	if exists {
		return "", domain.ErrConflict
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

	return inviteURL, nil
}

// Create inserts a new staff member from an accepted invite.
// Called by the auth module when an invite token is verified.
func (s *Service) Create(ctx context.Context, in CreateStaffInput) (*StaffResponse, error) {
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

// Deactivate marks a staff member as deactivated. Cannot deactivate the caller's own account.
func (s *Service) Deactivate(ctx context.Context, staffID, clinicID, callerID uuid.UUID) (*StaffResponse, error) {
	if staffID == callerID {
		return nil, fmt.Errorf("staff.service.Deactivate: %w", domain.ErrForbidden)
	}

	row, err := s.repo.Deactivate(ctx, staffID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("staff.service.Deactivate: %w", err)
	}

	return s.decryptAndBuild(row)
}

// ── Private helpers ───────────────────────────────────────────────────────────

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
		ID:           row.ID.String(),
		ClinicID:     row.ClinicID.String(),
		Email:        email,
		FullName:     fullName,
		Role:         row.Role,
		NoteTier:     row.NoteTier,
		Permissions:  row.Perms,
		Status:       row.Status,
		LastActiveAt: row.LastActiveAt,
		CreatedAt:    row.CreatedAt,
	}
}
