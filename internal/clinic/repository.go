package clinic

import (
	"context"
	"encoding/json"
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
	// Note-cap metering (pricing-model-v3 §7). billing_period_start is
	// authoritative for the current period; null on trial clinics that
	// haven't subscribed yet — metering falls back to created_at then.
	BillingPeriodStart  *time.Time
	BillingPeriodEnd    *time.Time
	NoteCapWarnedAt     *time.Time
	NoteCapCSAlertedAt  *time.Time
	NoteCapBlockedAt    *time.Time
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
	// RegulatoryIDs is a JSONB blob keyed by jurisdiction-aware
	// identifier name (nzbn, cqc_location_id, dea_id, ahpra_practice_id,
	// vmd_premises_id, …). Always non-NULL ('{}') at column level;
	// empty map = no IDs configured. Surfaced verbatim to clients —
	// the FE picks which keys to render based on clinic vertical +
	// country.
	RegulatoryIDs json.RawMessage
	// Compliance onboarding (NZ Privacy Act 2020 / HIPC 2020 / AU Privacy
	// Act 1988 APPs / AU Voluntary AI Safety Standard 2024). All fields are
	// nullable — a null timestamp means "not yet attested".
	PrivacyOfficerName              *string
	PrivacyOfficerEmail             *string
	PrivacyOfficerPhone             *string
	POTrainingAttestedAt            *time.Time
	CrossBorderAckAt                *time.Time
	CrossBorderAckVersion           *string
	MHRRegistered                   *bool // AU My Health Record; null outside AU
	AIOversightAckAt                *time.Time
	PatientConsentAckAt             *time.Time
	DPAAcceptedAt                   *time.Time
	DPAVersion                      *string
	ComplianceOnboardingCompletedAt *time.Time
	ComplianceOnboardingVersion     *string // "v1" or "grandfathered_v0"
	ComplianceOnboardingIP          *string // INET-as-text in the DB
	ComplianceOnboardingUserID      *uuid.UUID
	CreatedAt                       time.Time
	UpdatedAt                       time.Time
	ArchivedAt                      *time.Time
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
	// RegulatoryIDs is a partial JSONB blob — when non-nil it
	// replaces the column wholesale (the FE always sends the full
	// post-edit map).
	RegulatoryIDs json.RawMessage
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
	billing_period_start, billing_period_end,
	note_cap_warned_at, note_cap_cs_alerted_at, note_cap_blocked_at,
	data_region, scheduled_for_deletion_at,
	logo_key, accent_color,
	pdf_header_text, pdf_footer_text, pdf_primary_color, pdf_font,
	onboarding_step, onboarding_complete,
	legal_name, country, timezone, business_reg_no, terms_accepted_at,
	privacy_officer_name, privacy_officer_email, privacy_officer_phone,
	po_training_attested_at,
	cross_border_ack_at, cross_border_ack_version,
	mhr_registered,
	ai_oversight_ack_at, patient_consent_ack_at,
	dpa_accepted_at, dpa_version,
	compliance_onboarding_completed_at, compliance_onboarding_version,
	host(compliance_onboarding_ip)::text, compliance_onboarding_user_id,
	regulatory_ids,
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
			billing_period_start   = COALESCE($6, billing_period_start),
			billing_period_end     = COALESCE($7, billing_period_end),
			-- When the period rolls over (incoming start strictly newer than
			-- the stored value) reset the per-period sticky alert flags so
			-- the 80/110/150 cascade can re-fire in the next cycle.
			note_cap_warned_at = CASE
				WHEN $6::timestamptz IS NOT NULL
				 AND (billing_period_start IS NULL OR $6::timestamptz > billing_period_start)
				THEN NULL ELSE note_cap_warned_at END,
			note_cap_cs_alerted_at = CASE
				WHEN $6::timestamptz IS NOT NULL
				 AND (billing_period_start IS NULL OR $6::timestamptz > billing_period_start)
				THEN NULL ELSE note_cap_cs_alerted_at END,
			note_cap_blocked_at = CASE
				WHEN $6::timestamptz IS NOT NULL
				 AND (billing_period_start IS NULL OR $6::timestamptz > billing_period_start)
				THEN NULL ELSE note_cap_blocked_at END,
			updated_at             = NOW()
		WHERE id = $1 AND archived_at IS NULL
		RETURNING `+clinicCols,
		id, p.Status, p.PlanCode, p.StripeCustomerID, p.StripeSubscriptionID,
		p.BillingPeriodStart, p.BillingPeriodEnd,
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
			regulatory_ids      = COALESCE($18::jsonb, regulatory_ids),
			updated_at          = NOW()
		WHERE id = $1 AND archived_at IS NULL
		RETURNING `+clinicCols,
		id,
		p.Name, p.Phone, p.Address, p.LogoKey, p.AccentColor,
		p.PDFHeaderText, p.PDFFooterText, p.PDFPrimaryColor, p.PDFFont,
		p.OnboardingStep, p.OnboardingComplete,
		p.LegalName, p.Country, p.Timezone, p.BusinessRegNo, p.TermsAcceptedAt,
		p.RegulatoryIDs,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.Update: %w", err)
	}
	return c, nil
}

// ComplianceParams holds the full compliance attestation submitted during
// the onboarding wizard. Service layer is responsible for stamping the
// timestamp fields (ack_at, completed_at) — the repo just writes what it's
// given. Pointer fields with COALESCE semantics: nil leaves the column
// unchanged so a partial re-submission (e.g. PO contact only) doesn't
// clobber prior attestations.
type ComplianceParams struct {
	PrivacyOfficerName    *string
	PrivacyOfficerEmail   *string
	PrivacyOfficerPhone   *string
	POTrainingAttestedAt  *time.Time
	CrossBorderAckAt      *time.Time
	CrossBorderAckVersion *string
	MHRRegistered         *bool
	AIOversightAckAt      *time.Time
	PatientConsentAckAt   *time.Time
	DPAAcceptedAt         *time.Time
	DPAVersion            *string
	// Audit. CompletedAt + Version are stamped on the first complete
	// submission; UserID + IP are recorded against that attestation.
	CompletedAt *time.Time
	Version     *string
	IP          *string // INET-as-text; nil when not derivable
	UserID      *uuid.UUID
	// AdvanceStep, when true, advances onboarding_step to GREATEST(step, 2)
	// — i.e. moves the clinic past compliance during fresh onboarding while
	// never regressing a clinic that's already further along (grandfathered
	// post-hoc submission).
	AdvanceStep bool
}

// SubmitCompliance writes the compliance-onboarding attestation in one
// atomic UPDATE. COALESCE preserves prior values when the caller passes
// nil for a particular field.
func (r *Repository) SubmitCompliance(ctx context.Context, id uuid.UUID, p ComplianceParams) (*Clinic, error) {
	advance := int16(0)
	if p.AdvanceStep {
		advance = 2
	}
	c, err := r.scanOne(ctx, `
		UPDATE clinics SET
			privacy_officer_name               = COALESCE($2,  privacy_officer_name),
			privacy_officer_email              = COALESCE($3,  privacy_officer_email),
			privacy_officer_phone              = COALESCE($4,  privacy_officer_phone),
			po_training_attested_at            = COALESCE($5,  po_training_attested_at),
			cross_border_ack_at                = COALESCE($6,  cross_border_ack_at),
			cross_border_ack_version           = COALESCE($7,  cross_border_ack_version),
			mhr_registered                     = COALESCE($8,  mhr_registered),
			ai_oversight_ack_at                = COALESCE($9,  ai_oversight_ack_at),
			patient_consent_ack_at             = COALESCE($10, patient_consent_ack_at),
			dpa_accepted_at                    = COALESCE($11, dpa_accepted_at),
			dpa_version                        = COALESCE($12, dpa_version),
			compliance_onboarding_completed_at = COALESCE($13, compliance_onboarding_completed_at),
			compliance_onboarding_version      = COALESCE($14, compliance_onboarding_version),
			compliance_onboarding_ip           = COALESCE($15::inet, compliance_onboarding_ip),
			compliance_onboarding_user_id      = COALESCE($16, compliance_onboarding_user_id),
			onboarding_step                    = GREATEST(onboarding_step, $17),
			updated_at                         = NOW()
		WHERE id = $1 AND archived_at IS NULL
		RETURNING `+clinicCols,
		id,
		p.PrivacyOfficerName, p.PrivacyOfficerEmail, p.PrivacyOfficerPhone,
		p.POTrainingAttestedAt,
		p.CrossBorderAckAt, p.CrossBorderAckVersion,
		p.MHRRegistered,
		p.AIOversightAckAt, p.PatientConsentAckAt,
		p.DPAAcceptedAt, p.DPAVersion,
		p.CompletedAt, p.Version, p.IP, p.UserID,
		advance,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("clinic.repo.SubmitCompliance: %w", err)
	}
	return c, nil
}

// MarkNoteCapWarned stamps note_cap_warned_at to the given time iff the
// flag is currently NULL. Returns (true, nil) when the row was claimed
// (i.e. the email should be sent), (false, nil) when another caller had
// already marked it. Idempotent — safe to call repeatedly.
func (r *Repository) MarkNoteCapWarned(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE clinics SET note_cap_warned_at = $2, updated_at = NOW()
		WHERE id = $1 AND archived_at IS NULL AND note_cap_warned_at IS NULL
	`, id, at)
	if err != nil {
		return false, fmt.Errorf("clinic.repo.MarkNoteCapWarned: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkNoteCapCSAlerted stamps note_cap_cs_alerted_at iff currently NULL.
// Returns true when the caller claimed the alert (should send the CS
// notification), false when it had already been marked.
func (r *Repository) MarkNoteCapCSAlerted(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE clinics SET note_cap_cs_alerted_at = $2, updated_at = NOW()
		WHERE id = $1 AND archived_at IS NULL AND note_cap_cs_alerted_at IS NULL
	`, id, at)
	if err != nil {
		return false, fmt.Errorf("clinic.repo.MarkNoteCapCSAlerted: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkNoteCapBlocked stamps note_cap_blocked_at iff currently NULL. The
// flag is informational — the actual block decision is computed at note
// create time from the live count, never read from this column. We
// persist it so analytics/CS can see when each tenant first hit 150%.
func (r *Repository) MarkNoteCapBlocked(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE clinics SET note_cap_blocked_at = $2, updated_at = NOW()
		WHERE id = $1 AND archived_at IS NULL AND note_cap_blocked_at IS NULL
	`, id, at)
	if err != nil {
		return false, fmt.Errorf("clinic.repo.MarkNoteCapBlocked: %w", err)
	}
	return tag.RowsAffected() == 1, nil
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
//
// BillingPeriodStart/End mirror sub.current_period_{start,end}. When
// BillingPeriodStart advances past the prior value the per-period
// note-cap alert flags (warned/cs_alerted/blocked) reset to NULL so the
// 80/110/150% cascade can re-fire in the new period.
type ApplySubscriptionStateParams struct {
	Status               domain.ClinicStatus
	PlanCode             *domain.PlanCode
	StripeCustomerID     *string
	StripeSubscriptionID *string
	BillingPeriodStart   *time.Time
	BillingPeriodEnd     *time.Time
}

func (r *Repository) scanOne(ctx context.Context, query string, args ...any) (*Clinic, error) {
	var c Clinic
	if err := r.db.QueryRow(ctx, query, args...).Scan(
		&c.ID, &c.Name, &c.Slug, &c.Email, &c.EmailHash, &c.Phone, &c.Address,
		&c.Vertical, &c.Status, &c.TrialEndsAt,
		&c.PlanCode, &c.StripeCustomerID, &c.StripeSubscriptionID,
		&c.NoteCap, &c.NoteCount, &c.NoteCountResetAt,
		&c.BillingPeriodStart, &c.BillingPeriodEnd,
		&c.NoteCapWarnedAt, &c.NoteCapCSAlertedAt, &c.NoteCapBlockedAt,
		&c.DataRegion, &c.ScheduledForDeletionAt,
		&c.LogoKey, &c.AccentColor,
		&c.PDFHeaderText, &c.PDFFooterText, &c.PDFPrimaryColor, &c.PDFFont,
		&c.OnboardingStep, &c.OnboardingComplete,
		&c.LegalName, &c.Country, &c.Timezone, &c.BusinessRegNo, &c.TermsAcceptedAt,
		&c.PrivacyOfficerName, &c.PrivacyOfficerEmail, &c.PrivacyOfficerPhone,
		&c.POTrainingAttestedAt,
		&c.CrossBorderAckAt, &c.CrossBorderAckVersion,
		&c.MHRRegistered,
		&c.AIOversightAckAt, &c.PatientConsentAckAt,
		&c.DPAAcceptedAt, &c.DPAVersion,
		&c.ComplianceOnboardingCompletedAt, &c.ComplianceOnboardingVersion,
		&c.ComplianceOnboardingIP, &c.ComplianceOnboardingUserID,
		&c.RegulatoryIDs,
		&c.CreatedAt, &c.UpdatedAt, &c.ArchivedAt,
	); err != nil {
		return nil, fmt.Errorf("clinic.repo.scanOne: %w", err)
	}
	return &c, nil
}
