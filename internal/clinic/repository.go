package clinic

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

// Clinic is the repository-layer representation of a clinic row.
// PII fields (email, phone, address) are stored encrypted in the DB and
// are decrypted by the service layer before returning to callers.
type Clinic struct {
	ID                     uuid.UUID
	Name                   string
	Slug                   string
	Email                  string // encrypted in DB
	EmailHash              string
	Phone                  *string // encrypted in DB
	Address                *string // encrypted in DB
	Vertical               domain.Vertical
	Status                 domain.ClinicStatus
	TrialEndsAt            time.Time
	StripeCustomerID       *string
	StripeSubscriptionID   *string
	NoteCap                *int
	NoteCount              int
	NoteCountResetAt       *time.Time
	DataRegion             string
	ScheduledForDeletionAt *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ArchivedAt             *time.Time
}

// UpdateParams holds the fields that can be updated on a clinic.
// A nil pointer means "leave unchanged".
type UpdateParams struct {
	Name    *string
	Phone   *string // pre-encrypted by service
	Address *string // pre-encrypted by service
}

// Repository handles all database interactions for the clinic module.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new clinic Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Create inserts a new clinic row and returns the created record.
func (r *Repository) Create(ctx context.Context, p CreateParams) (*Clinic, error) {
	var c Clinic
	err := r.db.QueryRow(ctx, `
		INSERT INTO clinics (
			id, name, slug, email, email_hash, phone, address,
			vertical, status, trial_ends_at, data_region
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING
			id, name, slug, email, email_hash, phone, address,
			vertical, status, trial_ends_at,
			stripe_customer_id, stripe_subscription_id,
			note_cap, note_count, note_count_reset_at,
			data_region, scheduled_for_deletion_at,
			created_at, updated_at, archived_at
	`,
		p.ID, p.Name, p.Slug, p.Email, p.EmailHash, p.Phone, p.Address,
		p.Vertical, domain.ClinicStatusTrial, p.TrialEndsAt, p.DataRegion,
	).Scan(
		&c.ID, &c.Name, &c.Slug, &c.Email, &c.EmailHash, &c.Phone, &c.Address,
		&c.Vertical, &c.Status, &c.TrialEndsAt,
		&c.StripeCustomerID, &c.StripeSubscriptionID,
		&c.NoteCap, &c.NoteCount, &c.NoteCountResetAt,
		&c.DataRegion, &c.ScheduledForDeletionAt,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, domain.ErrConflict
		}
		return nil, fmt.Errorf("clinic.repo.Create: %w", err)
	}
	return &c, nil
}

// GetByID fetches a clinic by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		SELECT id, name, slug, email, email_hash, phone, address,
		       vertical, status, trial_ends_at,
		       stripe_customer_id, stripe_subscription_id,
		       note_cap, note_count, note_count_reset_at,
		       data_region, scheduled_for_deletion_at,
		       created_at, updated_at, archived_at
		FROM clinics WHERE id = $1 AND archived_at IS NULL
	`, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.GetByID: %w", err)
	}
	return c, nil
}

// GetByEmailHash fetches a clinic by its hashed email (used during registration deduplication).
func (r *Repository) GetByEmailHash(ctx context.Context, emailHash string) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		SELECT id, name, slug, email, email_hash, phone, address,
		       vertical, status, trial_ends_at,
		       stripe_customer_id, stripe_subscription_id,
		       note_cap, note_count, note_count_reset_at,
		       data_region, scheduled_for_deletion_at,
		       created_at, updated_at, archived_at
		FROM clinics WHERE email_hash = $1 AND archived_at IS NULL
	`, emailHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.GetByEmailHash: %w", err)
	}
	return c, nil
}

// Update applies a partial update to a clinic row and returns the updated record.
func (r *Repository) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		UPDATE clinics SET
			name    = COALESCE($2, name),
			phone   = COALESCE($3, phone),
			address = COALESCE($4, address),
			updated_at = NOW()
		WHERE id = $1 AND archived_at IS NULL
		RETURNING
			id, name, slug, email, email_hash, phone, address,
			vertical, status, trial_ends_at,
			stripe_customer_id, stripe_subscription_id,
			note_cap, note_count, note_count_reset_at,
			data_region, scheduled_for_deletion_at,
			created_at, updated_at, archived_at
	`, id, p.Name, p.Phone, p.Address)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.Update: %w", err)
	}
	return c, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// CreateParams holds all values required to create a new clinic.
type CreateParams struct {
	ID          uuid.UUID
	Name        string
	Slug        string
	Email       string // pre-encrypted
	EmailHash   string
	Phone       *string // pre-encrypted
	Address     *string // pre-encrypted
	Vertical    domain.Vertical
	TrialEndsAt time.Time
	DataRegion  string
}

func (r *Repository) scanOne(ctx context.Context, query string, args ...any) (*Clinic, error) {
	var c Clinic
	if err := r.db.QueryRow(ctx, query, args...).Scan(
		&c.ID, &c.Name, &c.Slug, &c.Email, &c.EmailHash, &c.Phone, &c.Address,
		&c.Vertical, &c.Status, &c.TrialEndsAt,
		&c.StripeCustomerID, &c.StripeSubscriptionID,
		&c.NoteCap, &c.NoteCount, &c.NoteCountResetAt,
		&c.DataRegion, &c.ScheduledForDeletionAt,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	); err != nil {
		return nil, fmt.Errorf("clinic.repo.scanOne: %w", err)
	}
	return &c, nil
}
