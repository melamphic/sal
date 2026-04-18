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
	PlanCode               *domain.PlanCode
	StripeCustomerID       *string
	StripeSubscriptionID   *string
	NoteCap                *int
	NoteCount              int
	NoteCountResetAt       *time.Time
	DataRegion             string
	ScheduledForDeletionAt *time.Time
	LogoKey                *string
	AccentColor            *string
	PDFHeaderText          *string
	PDFFooterText          *string
	PDFPrimaryColor        *string
	PDFFont                *string
	OnboardingStep         int16
	OnboardingComplete     bool
	LegalName              *string
	Country                string // ISO 3166-1 alpha-2 (e.g. NZ, AU, GB, IN)
	Timezone               string // IANA tz (e.g. Pacific/Auckland)
	BusinessRegNo          *string
	TermsAcceptedAt        *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ArchivedAt             *time.Time
}

// UpdateParams holds the fields that can be updated on a clinic.
// A nil pointer means "leave unchanged".
type UpdateParams struct {
	Name               *string
	Phone              *string // pre-encrypted by service
	Address            *string // pre-encrypted by service
	LogoKey            *string
	AccentColor        *string
	PDFHeaderText      *string
	PDFFooterText      *string
	PDFPrimaryColor    *string
	PDFFont            *string
	OnboardingStep     *int16
	OnboardingComplete *bool
	LegalName          *string
	Country            *string
	Timezone           *string
	BusinessRegNo      *string
	TermsAcceptedAt    *time.Time
}

// Repository handles all database interactions for the clinic module.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new clinic Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

const clinicCols = `
	id, name, slug, email, email_hash, phone, address,
	vertical, status, trial_ends_at,
	plan_code, stripe_customer_id, stripe_subscription_id,
	note_cap, note_count, note_count_reset_at,
	data_region, scheduled_for_deletion_at,
	logo_key, accent_color,
	pdf_header_text, pdf_footer_text, pdf_primary_color, pdf_font,
	onboarding_step, onboarding_complete,
	legal_name, country, timezone, business_reg_no, terms_accepted_at,
	created_at, updated_at, archived_at
`

// Create inserts a new clinic row and returns the created record.
func (r *Repository) Create(ctx context.Context, p CreateParams) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		INSERT INTO clinics (
			id, name, slug, email, email_hash, phone, address,
			vertical, status, trial_ends_at, data_region, plan_code, stripe_customer_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING `+clinicCols,
		p.ID, p.Name, p.Slug, p.Email, p.EmailHash, p.Phone, p.Address,
		p.Vertical, domain.ClinicStatusTrial, p.TrialEndsAt, p.DataRegion,
		p.PlanCode, p.StripeCustomerID,
	)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, domain.ErrConflict
		}
		return nil, fmt.Errorf("clinic.repo.Create: %w", err)
	}
	return c, nil
}

// GetByID fetches a clinic by primary key.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		SELECT `+clinicCols+`
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
		SELECT `+clinicCols+`
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

// GetByStripeCustomerID fetches a clinic by its Stripe customer id.
// Used by the billing webhook to map `subscription.*` events → clinic.
func (r *Repository) GetByStripeCustomerID(ctx context.Context, stripeCustomerID string) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		SELECT `+clinicCols+`
		FROM clinics WHERE stripe_customer_id = $1 AND archived_at IS NULL
	`, stripeCustomerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.GetByStripeCustomerID: %w", err)
	}
	return c, nil
}

// ApplySubscriptionState writes the authoritative billing state received
// from a Stripe webhook. Status is always written; plan_code and the
// stripe ids are COALESCEd so passing nil leaves them unchanged.
func (r *Repository) ApplySubscriptionState(ctx context.Context, id uuid.UUID, p ApplySubscriptionStateParams) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		UPDATE clinics SET
			status                 = $2,
			plan_code              = COALESCE($3, plan_code),
			stripe_customer_id     = COALESCE($4, stripe_customer_id),
			stripe_subscription_id = COALESCE($5, stripe_subscription_id),
			updated_at             = NOW()
		WHERE id = $1 AND archived_at IS NULL
		RETURNING `+clinicCols,
		id, p.Status, p.PlanCode, p.StripeCustomerID, p.StripeSubscriptionID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.ApplySubscriptionState: %w", err)
	}
	return c, nil
}

// Update applies a partial update to a clinic row and returns the updated record.
func (r *Repository) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (*Clinic, error) {
	c, err := r.scanOne(ctx, `
		UPDATE clinics SET
			name                = COALESCE($2,  name),
			phone               = COALESCE($3,  phone),
			address             = COALESCE($4,  address),
			logo_key            = COALESCE($5,  logo_key),
			accent_color        = COALESCE($6,  accent_color),
			pdf_header_text     = COALESCE($7,  pdf_header_text),
			pdf_footer_text     = COALESCE($8,  pdf_footer_text),
			pdf_primary_color   = COALESCE($9,  pdf_primary_color),
			pdf_font            = COALESCE($10, pdf_font),
			onboarding_step     = COALESCE($11, onboarding_step),
			onboarding_complete = COALESCE($12, onboarding_complete),
			legal_name          = COALESCE($13, legal_name),
			country             = COALESCE($14, country),
			timezone            = COALESCE($15, timezone),
			business_reg_no     = COALESCE($16, business_reg_no),
			terms_accepted_at   = COALESCE($17, terms_accepted_at),
			updated_at          = NOW()
		WHERE id = $1 AND archived_at IS NULL
		RETURNING `+clinicCols,
		id,
		p.Name, p.Phone, p.Address, p.LogoKey, p.AccentColor,
		p.PDFHeaderText, p.PDFFooterText, p.PDFPrimaryColor, p.PDFFont,
		p.OnboardingStep, p.OnboardingComplete,
		p.LegalName, p.Country, p.Timezone, p.BusinessRegNo, p.TermsAcceptedAt,
	)
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
	// Optional. Set during /mel handoff when the visitor purchased a plan
	// before reaching Salvia. Trial signups leave this nil — webhook fills
	// it on subscription.created.
	PlanCode *domain.PlanCode
	// StripeCustomerID is the `cus_…` id set during /mel paid-at-signup
	// checkout. Lets the webhook map `subscription.created` → clinic.
	// Nil on trial signups.
	StripeCustomerID *string
}

// ApplySubscriptionStateParams is the authoritative billing-state write
// triggered by Stripe webhooks. COALESCE semantics: nil pointer = leave
// unchanged. Status is always written.
type ApplySubscriptionStateParams struct {
	Status               domain.ClinicStatus
	PlanCode             *domain.PlanCode
	StripeCustomerID     *string
	StripeSubscriptionID *string
}

func (r *Repository) scanOne(ctx context.Context, query string, args ...any) (*Clinic, error) {
	var c Clinic
	if err := r.db.QueryRow(ctx, query, args...).Scan(
		&c.ID, &c.Name, &c.Slug, &c.Email, &c.EmailHash, &c.Phone, &c.Address,
		&c.Vertical, &c.Status, &c.TrialEndsAt,
		&c.PlanCode, &c.StripeCustomerID, &c.StripeSubscriptionID,
		&c.NoteCap, &c.NoteCount, &c.NoteCountResetAt,
		&c.DataRegion, &c.ScheduledForDeletionAt,
		&c.LogoKey, &c.AccentColor,
		&c.PDFHeaderText, &c.PDFFooterText, &c.PDFPrimaryColor, &c.PDFFont,
		&c.OnboardingStep, &c.OnboardingComplete,
		&c.LegalName, &c.Country, &c.Timezone, &c.BusinessRegNo, &c.TermsAcceptedAt,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	); err != nil {
		return nil, fmt.Errorf("clinic.repo.scanOne: %w", err)
	}
	return &c, nil
}
