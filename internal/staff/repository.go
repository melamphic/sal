package staff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// StaffRecord is the raw database representation of a staff member.
// PII fields (email, full_name) are stored encrypted and decrypted by the service.
type StaffRecord struct {
	ID           uuid.UUID
	ClinicID     uuid.UUID
	Email        string // encrypted
	EmailHash    string
	FullName     string // encrypted
	Role         domain.StaffRole
	NoteTier     domain.NoteTier
	Perms        domain.Permissions
	Status       domain.StaffStatus
	LastActiveAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   *time.Time
	// Regulatory identity — captured for the "Vet of record" /
	// "Reviewed by" lines on every signed clinical record + report
	// PDF. NZ vet: VCNZ; UK GP: GMC; UK nurse: NMC; AU clinician:
	// AHPRA; US vet: AVMA. Both nullable; populated via the
	// regulator-IDs settings page (P3-P).
	RegulatoryAuthority *string
	RegulatoryRegNo     *string
}

// CreateParams holds the values needed to insert a new staff row.
type CreateParams struct {
	ID        uuid.UUID
	ClinicID  uuid.UUID
	Email     string // pre-encrypted
	EmailHash string
	FullName  string // pre-encrypted
	Role      domain.StaffRole
	NoteTier  domain.NoteTier
	Perms     domain.Permissions
	Status    domain.StaffStatus
	// Optional invite-acceptance fields. nil values insert NULL — the
	// columns are all nullable and the existing onboarding paths
	// (handoff, EnsureOwner) leave them empty.
	Title              *string
	RegulatoryAuthority *string
	RegulatoryRegNo    *string
	MobileE164         *string // pre-encrypted by caller
	TermsAcceptedAt    *time.Time
}

// UpdatePermsParams holds new permission values for a staff member.
type UpdatePermsParams struct {
	Perms domain.Permissions
}

// ListParams holds pagination parameters for staff listing.
type ListParams struct {
	Limit  int
	Offset int
}

// Repository handles all database interactions for the staff module.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new staff Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Create inserts a new staff member and returns the created row.
func (r *Repository) Create(ctx context.Context, p CreateParams) (*StaffRecord, error) {
	row, err := r.scanOne(ctx, `
		INSERT INTO staff (
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, perm_manage_patients,
			perm_marketplace_manage, perm_marketplace_download, status,
			title, regulatory_authority, regulatory_reg_no,
			mobile_e164, terms_accepted_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19,
			$20, $21, $22,
			$23, $24, $25,
			$26, $27
		) RETURNING
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, perm_manage_patients,
			perm_marketplace_manage, perm_marketplace_download,
			status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
	`,
		p.ID, p.ClinicID, p.Email, p.EmailHash, p.FullName, p.Role, p.NoteTier,
		p.Perms.ManageStaff, p.Perms.ManageForms, p.Perms.ManagePolicies,
		p.Perms.ManageBilling, p.Perms.RollbackPolicies, p.Perms.RecordAudio,
		p.Perms.SubmitForms, p.Perms.ViewAllPatients, p.Perms.ViewOwnPatients,
		p.Perms.Dispense, p.Perms.GenerateAuditExport, p.Perms.ManagePatients,
		p.Perms.MarketplaceManage, p.Perms.MarketplaceDownload, p.Status,
		p.Title, p.RegulatoryAuthority, p.RegulatoryRegNo,
		p.MobileE164, p.TermsAcceptedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("staff.repo.Create: %w", err)
	}
	return row, nil
}

// GetByID fetches a staff member by ID within a clinic.
func (r *Repository) GetByID(ctx context.Context, staffID, clinicID uuid.UUID) (*StaffRecord, error) {
	row, err := r.scanOne(ctx, `
		SELECT id, clinic_id, email, email_hash, full_name, role, note_tier,
		       perm_manage_staff, perm_manage_forms, perm_manage_policies,
		       perm_manage_billing, perm_rollback_policies, perm_record_audio,
		       perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
		       perm_dispense, perm_generate_audit_export, perm_manage_patients,
		       perm_marketplace_manage, perm_marketplace_download,
		       status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
		FROM staff
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
	`, staffID, clinicID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff.repo.GetByID: %w", err)
	}
	return row, nil
}

// GetByEmailHash fetches a staff member by their hashed email within a clinic.
// Returns domain.ErrNotFound if no active staff member matches.
func (r *Repository) GetByEmailHash(ctx context.Context, emailHash string, clinicID uuid.UUID) (*StaffRecord, error) {
	row, err := r.scanOne(ctx, `
		SELECT id, clinic_id, email, email_hash, full_name, role, note_tier,
		       perm_manage_staff, perm_manage_forms, perm_manage_policies,
		       perm_manage_billing, perm_rollback_policies, perm_record_audio,
		       perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
		       perm_dispense, perm_generate_audit_export, perm_manage_patients,
		       status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
		FROM staff
		WHERE email_hash = $1 AND clinic_id = $2 AND archived_at IS NULL
	`, emailHash, clinicID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff.repo.GetByEmailHash: %w", err)
	}
	return row, nil
}

// ExistsByEmailHash checks if a staff member with the given email hash already exists in the clinic.
func (r *Repository) ExistsByEmailHash(ctx context.Context, emailHash string, clinicID uuid.UUID) (bool, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM staff
		WHERE email_hash = $1 AND clinic_id = $2 AND archived_at IS NULL
	`, emailHash, clinicID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("staff.repo.ExistsByEmailHash: %w", err)
	}
	return count > 0, nil
}

// List returns a page of staff members for a clinic, ordered by creation date.
func (r *Repository) List(ctx context.Context, clinicID uuid.UUID, p ListParams) ([]*StaffRecord, int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, clinic_id, email, email_hash, full_name, role, note_tier,
		       perm_manage_staff, perm_manage_forms, perm_manage_policies,
		       perm_manage_billing, perm_rollback_policies, perm_record_audio,
		       perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
		       perm_dispense, perm_generate_audit_export, perm_manage_patients,
		       perm_marketplace_manage, perm_marketplace_download,
		       status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
		FROM staff
		WHERE clinic_id = $1 AND archived_at IS NULL
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3
	`, clinicID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("staff.repo.List: query: %w", err)
	}
	defer rows.Close()

	var staff []*StaffRecord
	for rows.Next() {
		s := &StaffRecord{}
		if err := scanRow(rows, s); err != nil {
			return nil, 0, fmt.Errorf("staff.repo.List: scan: %w", err)
		}
		staff = append(staff, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("staff.repo.List: rows: %w", err)
	}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM staff WHERE clinic_id = $1 AND archived_at IS NULL`, clinicID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("staff.repo.List: count: %w", err)
	}

	return staff, total, nil
}

// UpdateRegulatoryIdentity sets (or clears) the regulator authority +
// registration number for a staff member. Captured via the regulator-IDs
// settings page; surfaces on every signed clinical record + report PDF.
//
// Pass (nil, nil) to clear both — useful when a clinician moves between
// jurisdictions or steps off the register.
func (r *Repository) UpdateRegulatoryIdentity(ctx context.Context, staffID, clinicID uuid.UUID, authority, regNo *string) (*StaffRecord, error) {
	row, err := r.scanOne(ctx, `
		UPDATE staff SET
			regulatory_authority = $3,
			regulatory_reg_no    = $4,
			updated_at           = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, perm_manage_patients,
			perm_marketplace_manage, perm_marketplace_download,
			status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
	`, staffID, clinicID, authority, regNo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff.repo.UpdateRegulatoryIdentity: %w", err)
	}
	return row, nil
}

// UpdatePermissions updates the permission flags for a staff member.
func (r *Repository) UpdatePermissions(ctx context.Context, staffID, clinicID uuid.UUID, p UpdatePermsParams) (*StaffRecord, error) {
	row, err := r.scanOne(ctx, `
		UPDATE staff SET
			perm_manage_staff          = $3,
			perm_manage_forms          = $4,
			perm_manage_policies       = $5,
			perm_manage_billing        = $6,
			perm_rollback_policies     = $7,
			perm_record_audio          = $8,
			perm_submit_forms          = $9,
			perm_view_all_patients     = $10,
			perm_view_own_patients     = $11,
			perm_dispense              = $12,
			perm_generate_audit_export = $13,
			perm_manage_patients       = $14,
			perm_marketplace_manage    = $15,
			perm_marketplace_download  = $16,
			updated_at                 = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, perm_manage_patients,
			perm_marketplace_manage, perm_marketplace_download,
			status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
	`,
		staffID, clinicID,
		p.Perms.ManageStaff, p.Perms.ManageForms, p.Perms.ManagePolicies,
		p.Perms.ManageBilling, p.Perms.RollbackPolicies, p.Perms.RecordAudio,
		p.Perms.SubmitForms, p.Perms.ViewAllPatients, p.Perms.ViewOwnPatients,
		p.Perms.Dispense, p.Perms.GenerateAuditExport, p.Perms.ManagePatients,
		p.Perms.MarketplaceManage, p.Perms.MarketplaceDownload,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff.repo.UpdatePermissions: %w", err)
	}
	return row, nil
}

// CountStandardActive counts staff members in a clinic whose
// note_tier='standard' and whose status is invited or active. Used by
// tier auto-derivation (pricing-model-v3 §6) — invited staff count
// because clinics pay for seats once an invitation is sent, not at
// first login. Archived rows never count.
func (r *Repository) CountStandardActive(ctx context.Context, clinicID uuid.UUID) (int, error) {
	var count int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM staff
		WHERE clinic_id = $1
		  AND archived_at IS NULL
		  AND note_tier = $2
		  AND status IN ('invited', 'active')
	`, clinicID, domain.NoteTierStandard).Scan(&count); err != nil {
		return 0, fmt.Errorf("staff.repo.CountStandardActive: %w", err)
	}
	return count, nil
}

// Deactivate soft-deletes a staff member by setting status to 'deactivated'.
func (r *Repository) Deactivate(ctx context.Context, staffID, clinicID uuid.UUID) (*StaffRecord, error) {
	row, err := r.scanOne(ctx, `
		UPDATE staff SET status = 'deactivated', updated_at = NOW()
		WHERE id = $1 AND clinic_id = $2 AND archived_at IS NULL
		RETURNING
			id, clinic_id, email, email_hash, full_name, role, note_tier,
			perm_manage_staff, perm_manage_forms, perm_manage_policies,
			perm_manage_billing, perm_rollback_policies, perm_record_audio,
			perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
			perm_dispense, perm_generate_audit_export, perm_manage_patients,
			perm_marketplace_manage, perm_marketplace_download,
			status, last_active_at, created_at, updated_at, archived_at, regulatory_authority, regulatory_reg_no
	`, staffID, clinicID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("staff.repo.Deactivate: %w", err)
	}
	return row, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

type pgxRows interface {
	Scan(dest ...any) error
}

func (r *Repository) scanOne(ctx context.Context, query string, args ...any) (*StaffRecord, error) {
	s := &StaffRecord{}
	return s, scanRow(r.db.QueryRow(ctx, query, args...), s)
}

func scanRow(row pgxRows, s *StaffRecord) error {
	if err := row.Scan(
		&s.ID, &s.ClinicID, &s.Email, &s.EmailHash, &s.FullName,
		&s.Role, &s.NoteTier,
		&s.Perms.ManageStaff, &s.Perms.ManageForms, &s.Perms.ManagePolicies,
		&s.Perms.ManageBilling, &s.Perms.RollbackPolicies, &s.Perms.RecordAudio,
		&s.Perms.SubmitForms, &s.Perms.ViewAllPatients, &s.Perms.ViewOwnPatients,
		&s.Perms.Dispense, &s.Perms.GenerateAuditExport, &s.Perms.ManagePatients,
		&s.Perms.MarketplaceManage, &s.Perms.MarketplaceDownload,
		&s.Status, &s.LastActiveAt, &s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt,
		&s.RegulatoryAuthority, &s.RegulatoryRegNo,
	); err != nil {
		return fmt.Errorf("staff.repo.scanRow: %w", err)
	}
	return nil
}
