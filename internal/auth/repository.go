package auth

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

// staffRow is a lightweight projection of the staff table used by the auth module.
// We do not import the staff module to avoid circular dependencies — auth only
// needs the fields required for token issuance and permission loading.
type staffRow struct {
	ID        uuid.UUID
	ClinicID  uuid.UUID
	EmailHash string
	FullName  string // encrypted
	Role      domain.StaffRole
	NoteTier  domain.NoteTier
	Status    domain.StaffStatus
	Perms     domain.Permissions
}

// tokenRow is a projection of the auth_tokens table.
type tokenRow struct {
	ID        uuid.UUID
	StaffID   uuid.UUID
	TokenHash string
	TokenType string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// inviteRow is a projection of the invite_tokens table.
type inviteRow struct {
	ID          uuid.UUID
	ClinicID    uuid.UUID
	Email       string // encrypted
	EmailHash   string
	Role        domain.StaffRole
	NoteTier    domain.NoteTier
	Permissions domain.Permissions
	TokenHash   string
	ExpiresAt   time.Time
	InvitedByID uuid.UUID
	AcceptedAt  *time.Time
}

// Repository handles all database interactions for the auth module.
// All queries filter by clinic_id where applicable.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new auth Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// FindStaffByEmailHash looks up a staff member by their hashed email.
// Returns domain.ErrNotFound if no active staff member exists with that hash.
func (r *Repository) FindStaffByEmailHash(ctx context.Context, emailHash string) (*staffRow, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, clinic_id, email_hash, full_name, role, note_tier, status,
		       perm_manage_staff, perm_manage_forms, perm_manage_policies,
		       perm_manage_billing, perm_rollback_policies, perm_record_audio,
		       perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
		       perm_dispense, perm_generate_audit_export
		FROM staff
		WHERE email_hash = $1 AND archived_at IS NULL
	`, emailHash)

	s, err := scanStaff(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("auth.repo.FindStaffByEmailHash: %w", err)
	}
	return s, nil
}

// CreateAuthToken inserts a new hashed token (magic link or refresh) into the database.
func (r *Repository) CreateAuthToken(ctx context.Context, staffID uuid.UUID, tokenHash, tokenType, fromIP string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO auth_tokens (id, staff_id, token_hash, token_type, expires_at, created_from_ip)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, domain.NewID(), staffID, tokenHash, tokenType, expiresAt, fromIP)
	if err != nil {
		return fmt.Errorf("auth.repo.CreateAuthToken: %w", err)
	}
	return nil
}

// GetAndConsumeAuthToken atomically fetches a token and marks it as used.
// Returns domain.ErrNotFound if the token doesn't exist, domain.ErrTokenUsed if
// already consumed, and domain.ErrTokenExpired if past its expiry.
func (r *Repository) GetAndConsumeAuthToken(ctx context.Context, tokenHash string) (*tokenRow, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth.repo.GetAndConsumeAuthToken: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var t tokenRow
	err = tx.QueryRow(ctx, `
		SELECT id, staff_id, token_hash, token_type, expires_at, used_at
		FROM auth_tokens
		WHERE token_hash = $1
		FOR UPDATE
	`, tokenHash).Scan(&t.ID, &t.StaffID, &t.TokenHash, &t.TokenType, &t.ExpiresAt, &t.UsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("auth.repo.GetAndConsumeAuthToken: scan: %w", err)
	}

	if t.UsedAt != nil {
		return nil, domain.ErrTokenUsed
	}
	if time.Now().After(t.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	if _, err := tx.Exec(ctx, `UPDATE auth_tokens SET used_at = NOW() WHERE id = $1`, t.ID); err != nil {
		return nil, fmt.Errorf("auth.repo.GetAndConsumeAuthToken: mark used: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("auth.repo.GetAndConsumeAuthToken: commit: %w", err)
	}

	return &t, nil
}

// GetStaffByID fetches a staff record by ID for token refresh validation.
func (r *Repository) GetStaffByID(ctx context.Context, staffID uuid.UUID) (*staffRow, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, clinic_id, email_hash, full_name, role, note_tier, status,
		       perm_manage_staff, perm_manage_forms, perm_manage_policies,
		       perm_manage_billing, perm_rollback_policies, perm_record_audio,
		       perm_submit_forms, perm_view_all_patients, perm_view_own_patients,
		       perm_dispense, perm_generate_audit_export
		FROM staff
		WHERE id = $1 AND archived_at IS NULL
	`, staffID)

	s, err := scanStaff(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("auth.repo.GetStaffByID: %w", err)
	}
	return s, nil
}

// GetInviteByTokenHash fetches a pending invite token.
func (r *Repository) GetInviteByTokenHash(ctx context.Context, tokenHash string) (*inviteRow, error) {
	var inv inviteRow
	var perms permColumns
	err := r.db.QueryRow(ctx, `
		SELECT id, clinic_id, email, email_hash, role, note_tier, permissions,
		       token_hash, expires_at, invited_by_id, accepted_at
		FROM invite_tokens
		WHERE token_hash = $1 AND accepted_at IS NULL
	`, tokenHash).Scan(
		&inv.ID, &inv.ClinicID, &inv.Email, &inv.EmailHash,
		&inv.Role, &inv.NoteTier, &perms,
		&inv.TokenHash, &inv.ExpiresAt, &inv.InvitedByID, &inv.AcceptedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("auth.repo.GetInviteByTokenHash: %w", err)
	}
	return &inv, nil
}

// MarkInviteAccepted sets accepted_at on an invite token.
func (r *Repository) MarkInviteAccepted(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx, `UPDATE invite_tokens SET accepted_at = NOW() WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("auth.repo.MarkInviteAccepted: %w", err)
	}
	return nil
}

// UpdateLastActive sets last_active_at to now for the given staff member.
// Called asynchronously after successful login — failure is non-fatal.
func (r *Repository) UpdateLastActive(ctx context.Context, staffID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE staff SET last_active_at = NOW(), status = 'active' WHERE id = $1`, staffID)
	if err != nil {
		return fmt.Errorf("auth.repo.UpdateLastActive: %w", err)
	}
	return nil
}

// DeleteRefreshTokensForStaff invalidates all refresh tokens on logout.
func (r *Repository) DeleteRefreshTokensForStaff(ctx context.Context, staffID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM auth_tokens WHERE staff_id = $1 AND token_type = 'refresh'
	`, staffID)
	if err != nil {
		return fmt.Errorf("auth.repo.DeleteRefreshTokensForStaff: %w", err)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// pgxScanner is satisfied by *pgx.Row and pgx.Rows.
type pgxScanner interface {
	Scan(dest ...any) error
}

// permColumns is used to scan JSONB permissions from invite_tokens.
type permColumns = domain.Permissions

func scanStaff(row pgxScanner) (*staffRow, error) {
	var s staffRow
	if err := row.Scan(
		&s.ID, &s.ClinicID, &s.EmailHash, &s.FullName,
		&s.Role, &s.NoteTier, &s.Status,
		&s.Perms.ManageStaff, &s.Perms.ManageForms, &s.Perms.ManagePolicies,
		&s.Perms.ManageBilling, &s.Perms.RollbackPolicies, &s.Perms.RecordAudio,
		&s.Perms.SubmitForms, &s.Perms.ViewAllPatients, &s.Perms.ViewOwnPatients,
		&s.Perms.Dispense, &s.Perms.GenerateAuditExport,
	); err != nil {
		return nil, fmt.Errorf("auth.repo.scanStaff: %w", err)
	}
	return &s, nil
}
