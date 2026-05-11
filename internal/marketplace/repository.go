// Package marketplace implements the Salvia form marketplace.
// Phase 1 scope: Salvia-curated free forms. Browse, preview, acquire (free),
// import. Third-party publishing and Stripe Connect are later phases.
// See docs/marketplace.md for full design.
package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/melamphic/sal/internal/domain"
)

// ── Record types ──────────────────────────────────────────────────────────────

// PublisherRecord mirrors the publisher_accounts row.
type PublisherRecord struct {
	ID                       uuid.UUID
	ClinicID                 uuid.UUID
	DisplayName              string
	Bio                      *string
	WebsiteURL               *string
	VerifiedBadge            bool
	AuthorityType            *string
	AuthorityGrantedBy       *uuid.UUID
	AuthorityGrantedAt       *time.Time
	StripeConnectAccountID   *string
	StripeOnboardingComplete bool
	Status                   string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// ListingRecord mirrors the marketplace_listings row.
type ListingRecord struct {
	ID                 uuid.UUID
	PublisherAccountID uuid.UUID
	Vertical           string
	Name               string
	Slug               string
	ShortDescription   string
	LongDescription    *string
	Tags               []string
	BundleType         string // 'bundled' | 'form_only' | 'pack' | 'policy_only'
	PricingType        string
	PriceCents         *int
	Currency           string
	Status             string
	PreviewFieldCount  int
	DownloadCount      int
	RatingCount        int
	RatingSum          int
	PublishedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ArchivedAt         *time.Time
	// SourcePolicyID is set only for bundle_type='policy_only' listings —
	// the tenant policy whose latest published version becomes the
	// marketplace version snapshot.
	SourcePolicyID *uuid.UUID
	// Joined publisher columns for list/detail responses.
	PublisherDisplayName   string
	PublisherVerifiedBadge bool
	PublisherAuthorityType *string
	PublisherClinicID      uuid.UUID
}

// VersionRecord mirrors marketplace_versions.
type VersionRecord struct {
	ID                  uuid.UUID
	ListingID           uuid.UUID
	VersionMajor        int
	VersionMinor        int
	ChangeType          string
	ChangeSummary       *string
	PackagePayload      json.RawMessage
	PayloadChecksum     string
	FieldCount          int
	SourceFormVersionID *uuid.UUID
	Status              string
	PublishedAt         time.Time
	PublishedBy         uuid.UUID
	CreatedAt           time.Time
}

// VersionFieldRecord mirrors marketplace_version_fields (identical shape to form_fields).
type VersionFieldRecord struct {
	ID                   uuid.UUID
	MarketplaceVersionID uuid.UUID
	Position             int
	Title                string
	Type                 string
	Config               json.RawMessage
	AIPrompt             *string
	Required             bool
	Skippable            bool
	AllowInference       bool
	MinConfidence        *float64
	// SourceFormPosition is the 1-based position of the pack-form this field
	// belonged to. NULL for non-pack versions (single-form / policy_only).
	SourceFormPosition *int
}

// AcquisitionRecord mirrors marketplace_acquisitions.
type AcquisitionRecord struct {
	ID                          uuid.UUID
	ListingID                   uuid.UUID
	MarketplaceVersionID        uuid.UUID
	ClinicID                    uuid.UUID
	AcquiredBy                  uuid.UUID
	AcquisitionType             string
	StripePaymentIntentID       *string
	AmountPaidCents             *int
	PlatformFeeCents            *int
	Currency                    *string
	Status                      string
	ImportedFormID              *uuid.UUID
	PolicyImportChoice          *string
	PolicyAttributionAcceptedAt *time.Time
	FulfilledAt                 *time.Time
	CreatedAt                   time.Time
}

// ── Param types ───────────────────────────────────────────────────────────────

// CreatePublisherParams is used when a Salvia admin provisions a publisher account.
type CreatePublisherParams struct {
	ID            uuid.UUID
	ClinicID      uuid.UUID
	DisplayName   string
	Bio           *string
	WebsiteURL    *string
	AuthorityType *string
	Status        string
}

// CreateListingParams holds values needed to insert a new marketplace listing.
type CreateListingParams struct {
	ID                 uuid.UUID
	PublisherAccountID uuid.UUID
	Vertical           string
	Name               string
	Slug               string
	ShortDescription   string
	LongDescription    *string
	Tags               []string
	BundleType         string
	PricingType        string
	PriceCents         *int
	Currency           string
	PreviewFieldCount  int
	Status             string
	// SourcePolicyID is non-nil only for bundle_type='policy_only'.
	SourcePolicyID *uuid.UUID
}

// CreateVersionParams holds values needed to insert a new marketplace version.
type CreateVersionParams struct {
	ID                  uuid.UUID
	ListingID           uuid.UUID
	VersionMajor        int
	VersionMinor        int
	ChangeType          string
	ChangeSummary       *string
	PackagePayload      json.RawMessage
	PayloadChecksum     string
	FieldCount          int
	SourceFormVersionID *uuid.UUID
	PublishedBy         uuid.UUID
	PublishedAt         time.Time
	// Fields is the relational mirror inserted atomically with the version row.
	Fields []CreateVersionFieldParams
}

// CreateVersionFieldParams mirrors CreateFieldParams on the forms module.
type CreateVersionFieldParams struct {
	ID             uuid.UUID
	Position       int
	Title          string
	Type           string
	Config         json.RawMessage
	AIPrompt       *string
	Required       bool
	Skippable      bool
	AllowInference bool
	MinConfidence  *float64
	// SourceFormPosition is the 1-based pack-form index. Nil for non-pack
	// versions; the column is stored NULL.
	SourceFormPosition *int
}

// CreateAcquisitionParams holds values for a new entitlement row.
type CreateAcquisitionParams struct {
	ID                    uuid.UUID
	ListingID             uuid.UUID
	MarketplaceVersionID  uuid.UUID
	ClinicID              uuid.UUID
	AcquiredBy            uuid.UUID
	AcquisitionType       string
	Status                string
	StripePaymentIntentID *string
	FulfilledAt           *time.Time
}

// ListListingsParams holds browse query filter + pagination.
type ListListingsParams struct {
	Query        *string // free-text search (tsvector)
	Vertical     *string
	PricingType  *string
	VerifiedOnly bool
	PolicyLinked *bool
	Sort         string // relevance | rating | downloads | newest
	Limit        int
	Offset       int
}

// ── Repository ────────────────────────────────────────────────────────────────

// Repository is the PostgreSQL implementation of the marketplace repo.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository constructs a marketplace Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── Publishers ────────────────────────────────────────────────────────────────

const publisherCols = `
	id, clinic_id, display_name, bio, website_url, verified_badge,
	authority_type, authority_granted_by, authority_granted_at,
	stripe_connect_account_id, stripe_onboarding_complete, status,
	created_at, updated_at`

// CreatePublisher inserts a new publisher_accounts row.
func (r *Repository) CreatePublisher(ctx context.Context, p CreatePublisherParams) (*PublisherRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO publisher_accounts (id, clinic_id, display_name, bio, website_url, authority_type, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING %s`, publisherCols)

	row := r.db.QueryRow(ctx, q, p.ID, p.ClinicID, p.DisplayName, p.Bio, p.WebsiteURL, p.AuthorityType, p.Status)
	rec, err := scanPublisher(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("marketplace.repo.CreatePublisher: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("marketplace.repo.CreatePublisher: %w", err)
	}
	return rec, nil
}

// GetPublisherByID fetches a publisher row by ID.
func (r *Repository) GetPublisherByID(ctx context.Context, id uuid.UUID) (*PublisherRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM publisher_accounts WHERE id = $1`, publisherCols)
	row := r.db.QueryRow(ctx, q, id)
	rec, err := scanPublisher(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetPublisherByID: %w", err)
	}
	return rec, nil
}

// GetPublisherByClinicID fetches the publisher associated with a clinic.
func (r *Repository) GetPublisherByClinicID(ctx context.Context, clinicID uuid.UUID) (*PublisherRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM publisher_accounts WHERE clinic_id = $1`, publisherCols)
	row := r.db.QueryRow(ctx, q, clinicID)
	rec, err := scanPublisher(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetPublisherByClinicID: %w", err)
	}
	return rec, nil
}

// ── Listings ──────────────────────────────────────────────────────────────────

const listingCols = `
	l.id, l.publisher_account_id, l.vertical, l.name, l.slug,
	l.short_description, l.long_description, l.tags, l.bundle_type,
	l.pricing_type, l.price_cents, l.currency, l.status,
	l.preview_field_count, l.download_count, l.rating_count, l.rating_sum,
	l.published_at, l.created_at, l.updated_at, l.archived_at,
	l.source_policy_id,
	p.display_name, p.verified_badge, p.authority_type, p.clinic_id`

// CreateListing inserts a new marketplace listing in draft state.
func (r *Repository) CreateListing(ctx context.Context, p CreateListingParams) (*ListingRecord, error) {
	q := fmt.Sprintf(`
		WITH ins AS (
			INSERT INTO marketplace_listings (
				id, publisher_account_id, vertical, name, slug,
				short_description, long_description, tags, bundle_type,
				pricing_type, price_cents, currency,
				preview_field_count, status, source_policy_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING *
		)
		SELECT %s FROM ins l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id`, listingCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.PublisherAccountID, p.Vertical, p.Name, p.Slug,
		p.ShortDescription, p.LongDescription, p.Tags, p.BundleType,
		p.PricingType, p.PriceCents, p.Currency,
		p.PreviewFieldCount, p.Status, p.SourcePolicyID,
	)
	rec, err := scanListing(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("marketplace.repo.CreateListing: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("marketplace.repo.CreateListing: %w", err)
	}
	return rec, nil
}

// GetListingByID fetches a listing by ID with publisher metadata joined.
func (r *Repository) GetListingByID(ctx context.Context, id uuid.UUID) (*ListingRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s
		FROM marketplace_listings l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id
		WHERE l.id = $1`, listingCols)
	row := r.db.QueryRow(ctx, q, id)
	rec, err := scanListing(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetListingByID: %w", err)
	}
	return rec, nil
}

// GetListingBySlug fetches a published listing by slug.
func (r *Repository) GetListingBySlug(ctx context.Context, slug string) (*ListingRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s
		FROM marketplace_listings l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id
		WHERE l.slug = $1`, listingCols)
	row := r.db.QueryRow(ctx, q, slug)
	rec, err := scanListing(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetListingBySlug: %w", err)
	}
	return rec, nil
}

// ListListings runs the public browse query with filters + sort + pagination.
// Only returns listings with status = 'published'.
func (r *Repository) ListListings(ctx context.Context, p ListListingsParams) ([]*ListingRecord, int, error) {
	args := []any{}
	add := func(v any) int {
		args = append(args, v)
		return len(args)
	}

	where := "l.status = 'published'"

	var queryIdx int
	if p.Query != nil && *p.Query != "" {
		queryIdx = add(*p.Query)
		where += fmt.Sprintf(" AND l.search_vector @@ websearch_to_tsquery('english', $%d)", queryIdx)
	}
	if p.Vertical != nil {
		where += fmt.Sprintf(" AND l.vertical = $%d", add(*p.Vertical))
	}
	if p.PricingType != nil {
		where += fmt.Sprintf(" AND l.pricing_type = $%d", add(*p.PricingType))
	}
	if p.VerifiedOnly {
		where += " AND p.verified_badge = true"
	}
	if p.PolicyLinked != nil {
		where += fmt.Sprintf(" AND l.policy_dependency_flag = $%d", add(*p.PolicyLinked))
	}

	countQ := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM marketplace_listings l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id
		WHERE %s`, where)

	var total int
	if err := r.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListListings: count: %w", err)
	}

	// ORDER BY chain with precomputed Bayesian rating expression.
	// Bayesian constants: prior count C=5, prior mean m=3.0.
	orderBy := `
		CASE WHEN $` + fmt.Sprint(add(p.Sort)) + `::text = 'rating' THEN
			(5.0 * 3.0 + l.rating_sum::float) / (5 + l.rating_count)
		END DESC NULLS LAST,
		CASE WHEN $` + fmt.Sprint(len(args)) + `::text = 'downloads' THEN l.download_count END DESC NULLS LAST,
		CASE WHEN $` + fmt.Sprint(len(args)) + `::text = 'newest'    THEN l.published_at END DESC NULLS LAST`

	if queryIdx > 0 {
		orderBy += fmt.Sprintf(`,
		CASE WHEN $%d::text = 'relevance' THEN ts_rank_cd(l.search_vector, websearch_to_tsquery('english', $%d)) END DESC NULLS LAST`, len(args), queryIdx)
	}
	orderBy += `, l.published_at DESC NULLS LAST, l.created_at DESC`

	limitIdx := add(p.Limit)
	offsetIdx := add(p.Offset)

	listQ := fmt.Sprintf(`
		SELECT %s
		FROM marketplace_listings l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`, listingCols, where, orderBy, limitIdx, offsetIdx)

	rows, err := r.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListListings: %w", err)
	}
	defer rows.Close()

	var listings []*ListingRecord
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("marketplace.repo.ListListings: %w", err)
		}
		listings = append(listings, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListListings: rows: %w", err)
	}
	return listings, total, nil
}

// PublishListing transitions a listing from draft → published and sets published_at.
func (r *Repository) PublishListing(ctx context.Context, id uuid.UUID, now time.Time) (*ListingRecord, error) {
	q := fmt.Sprintf(`
		WITH upd AS (
			UPDATE marketplace_listings
			SET status = 'published',
			    published_at = COALESCE(published_at, $2)
			WHERE id = $1 AND status IN ('draft', 'under_review')
			RETURNING *
		)
		SELECT %s
		FROM upd l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id`, listingCols)

	row := r.db.QueryRow(ctx, q, id, now)
	rec, err := scanListing(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.PublishListing: %w", err)
	}
	return rec, nil
}

// IncrementDownloadCount bumps the denormalised counter on a listing.
func (r *Repository) IncrementDownloadCount(ctx context.Context, id uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `UPDATE marketplace_listings SET download_count = download_count + 1 WHERE id = $1`, id); err != nil {
		return fmt.Errorf("marketplace.repo.IncrementDownloadCount: %w", err)
	}
	return nil
}

// ── Versions ──────────────────────────────────────────────────────────────────

const versionCols = `
	id, listing_id, version_major, version_minor, change_type, change_summary,
	package_payload, payload_checksum, field_count, source_form_version_id,
	status, published_at, published_by, created_at`

// CreateVersion inserts a marketplace version + all its fields in a single transaction.
func (r *Repository) CreateVersion(ctx context.Context, p CreateVersionParams) (*VersionRecord, []*VersionFieldRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("marketplace.repo.CreateVersion: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	versionQ := fmt.Sprintf(`
		INSERT INTO marketplace_versions (
			id, listing_id, version_major, version_minor, change_type, change_summary,
			package_payload, payload_checksum, field_count, source_form_version_id,
			published_by, published_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING %s`, versionCols)

	row := tx.QueryRow(ctx, versionQ,
		p.ID, p.ListingID, p.VersionMajor, p.VersionMinor, p.ChangeType, p.ChangeSummary,
		p.PackagePayload, p.PayloadChecksum, p.FieldCount, p.SourceFormVersionID,
		p.PublishedBy, p.PublishedAt,
	)
	version, err := scanVersion(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, nil, fmt.Errorf("marketplace.repo.CreateVersion: %w", domain.ErrConflict)
		}
		return nil, nil, fmt.Errorf("marketplace.repo.CreateVersion: %w", err)
	}

	fields := make([]*VersionFieldRecord, 0, len(p.Fields))
	const fieldQ = `
		INSERT INTO marketplace_version_fields (
			id, marketplace_version_id, position, title, type, config,
			ai_prompt, required, skippable, allow_inference, min_confidence,
			source_form_position
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, marketplace_version_id, position, title, type, config,
		          ai_prompt, required, skippable, allow_inference, min_confidence,
		          source_form_position`

	for _, f := range p.Fields {
		row := tx.QueryRow(ctx, fieldQ,
			f.ID, version.ID, f.Position, f.Title, f.Type, f.Config,
			f.AIPrompt, f.Required, f.Skippable, f.AllowInference, f.MinConfidence,
			f.SourceFormPosition,
		)
		rec, err := scanVersionField(row)
		if err != nil {
			return nil, nil, fmt.Errorf("marketplace.repo.CreateVersion: field: %w", err)
		}
		fields = append(fields, rec)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("marketplace.repo.CreateVersion: commit: %w", err)
	}

	return version, fields, nil
}

// GetVersionByID fetches a single version.
func (r *Repository) GetVersionByID(ctx context.Context, id uuid.UUID) (*VersionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM marketplace_versions WHERE id = $1`, versionCols)
	row := r.db.QueryRow(ctx, q, id)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetVersionByID: %w", err)
	}
	return rec, nil
}

// GetLatestVersion returns the newest version for a listing.
func (r *Repository) GetLatestVersion(ctx context.Context, listingID uuid.UUID) (*VersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM marketplace_versions
		WHERE listing_id = $1 AND status = 'active'
		ORDER BY version_major DESC, version_minor DESC
		LIMIT 1`, versionCols)
	row := r.db.QueryRow(ctx, q, listingID)
	rec, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetLatestVersion: %w", err)
	}
	return rec, nil
}

// ListVersionsByListing returns all versions for a listing, newest first.
func (r *Repository) ListVersionsByListing(ctx context.Context, listingID uuid.UUID) ([]*VersionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM marketplace_versions
		WHERE listing_id = $1
		ORDER BY version_major DESC, version_minor DESC`, versionCols)
	rows, err := r.db.Query(ctx, q, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.ListVersionsByListing: %w", err)
	}
	defer rows.Close()

	var list []*VersionRecord
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("marketplace.repo.ListVersionsByListing: %w", err)
		}
		list = append(list, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.repo.ListVersionsByListing: rows: %w", err)
	}
	return list, nil
}

// GetFieldsByVersionID returns all fields for a version ordered by position.
// For pack versions the order is (source_form_position, position) so each
// pack-form's fields stay grouped together when iterated.
func (r *Repository) GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]*VersionFieldRecord, error) {
	const q = `
		SELECT id, marketplace_version_id, position, title, type, config,
		       ai_prompt, required, skippable, allow_inference, min_confidence,
		       source_form_position
		FROM marketplace_version_fields
		WHERE marketplace_version_id = $1
		ORDER BY COALESCE(source_form_position, 0), position`

	rows, err := r.db.Query(ctx, q, versionID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetFieldsByVersionID: %w", err)
	}
	defer rows.Close()

	var list []*VersionFieldRecord
	for rows.Next() {
		f, err := scanVersionField(rows)
		if err != nil {
			return nil, fmt.Errorf("marketplace.repo.GetFieldsByVersionID: %w", err)
		}
		list = append(list, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetFieldsByVersionID: rows: %w", err)
	}
	return list, nil
}

// ── Acquisitions ──────────────────────────────────────────────────────────────

const acquisitionCols = `
	id, listing_id, marketplace_version_id, clinic_id, acquired_by,
	acquisition_type, stripe_payment_intent_id, amount_paid_cents,
	platform_fee_cents, currency, status, imported_form_id,
	policy_import_choice, policy_attribution_accepted_at,
	fulfilled_at, created_at`

// CreateAcquisition inserts an entitlement row.
// Unique constraint on (listing_id, clinic_id) WHERE status = 'active'
// is mapped to domain.ErrConflict when violated.
func (r *Repository) CreateAcquisition(ctx context.Context, p CreateAcquisitionParams) (*AcquisitionRecord, error) {
	q := fmt.Sprintf(`
		INSERT INTO marketplace_acquisitions (
			id, listing_id, marketplace_version_id, clinic_id, acquired_by,
			acquisition_type, status, stripe_payment_intent_id, fulfilled_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING %s`, acquisitionCols)

	row := r.db.QueryRow(ctx, q,
		p.ID, p.ListingID, p.MarketplaceVersionID, p.ClinicID, p.AcquiredBy,
		p.AcquisitionType, p.Status, p.StripePaymentIntentID, p.FulfilledAt,
	)
	rec, err := scanAcquisition(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("marketplace.repo.CreateAcquisition: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("marketplace.repo.CreateAcquisition: %w", err)
	}
	return rec, nil
}

// GetAcquisitionByID fetches an acquisition scoped to a clinic.
func (r *Repository) GetAcquisitionByID(ctx context.Context, id, clinicID uuid.UUID) (*AcquisitionRecord, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM marketplace_acquisitions
		WHERE id = $1 AND clinic_id = $2`, acquisitionCols)
	row := r.db.QueryRow(ctx, q, id, clinicID)
	rec, err := scanAcquisition(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetAcquisitionByID: %w", err)
	}
	return rec, nil
}

// ListAcquisitionsByClinic returns a clinic's entitlements.
func (r *Repository) ListAcquisitionsByClinic(ctx context.Context, clinicID uuid.UUID, limit, offset int) ([]*AcquisitionRecord, int, error) {
	q := fmt.Sprintf(`
		SELECT %s FROM marketplace_acquisitions
		WHERE clinic_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, acquisitionCols)

	rows, err := r.db.Query(ctx, q, clinicID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListAcquisitionsByClinic: %w", err)
	}
	defer rows.Close()

	var list []*AcquisitionRecord
	for rows.Next() {
		a, err := scanAcquisition(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("marketplace.repo.ListAcquisitionsByClinic: %w", err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListAcquisitionsByClinic: rows: %w", err)
	}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM marketplace_acquisitions WHERE clinic_id = $1`, clinicID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListAcquisitionsByClinic: count: %w", err)
	}

	return list, total, nil
}

// SetAcquisitionImportedForm marks an acquisition as imported into the tenant forms table.
func (r *Repository) SetAcquisitionImportedForm(ctx context.Context, acquisitionID, clinicID, importedFormID uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE marketplace_acquisitions
		SET imported_form_id = $3
		WHERE id = $1 AND clinic_id = $2 AND status = 'active'`, acquisitionID, clinicID, importedFormID)
	if err != nil {
		return fmt.Errorf("marketplace.repo.SetAcquisitionImportedForm: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("marketplace.repo.SetAcquisitionImportedForm: %w", domain.ErrNotFound)
	}
	return nil
}

// ── Listing update / moderation ──────────────────────────────────────────────

// UpdateListingStatus sets status on a listing. Used by suspend.
func (r *Repository) UpdateListingStatus(ctx context.Context, id uuid.UUID, status string) (*ListingRecord, error) {
	q := fmt.Sprintf(`
		WITH upd AS (
			UPDATE marketplace_listings
			SET status = $2, updated_at = NOW()
			WHERE id = $1
			RETURNING *
		)
		SELECT %s
		FROM upd l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id`, listingCols)

	row := r.db.QueryRow(ctx, q, id, status)
	rec, err := scanListing(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.UpdateListingStatus: %w", err)
	}
	return rec, nil
}

// UpdateListingMetadataParams holds the editable subset of a listing's
// metadata. The service layer decides which subset is actually allowed for
// a given listing's status (draft = everything, published = the description
// + tags + preview only).
type UpdateListingMetadataParams struct {
	ID                uuid.UUID
	Name              *string
	ShortDescription  *string
	LongDescription   *string // *string + sentinel: caller passes &"" to clear
	Tags              *[]string
	BundleType        *string
	PricingType       *string
	PriceCents        *int
	Currency          *string
	PreviewFieldCount *int
}

// UpdateListingMetadata applies a partial update. Each field is only written
// when the corresponding pointer is non-nil; that way the service can honour
// "only these fields editable when published" without separate SQL paths.
func (r *Repository) UpdateListingMetadata(ctx context.Context, p UpdateListingMetadataParams) (*ListingRecord, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{p.ID}
	idx := 2
	add := func(col string, val any) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, val)
		idx++
	}
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.ShortDescription != nil {
		add("short_description", *p.ShortDescription)
	}
	if p.LongDescription != nil {
		add("long_description", *p.LongDescription)
	}
	if p.Tags != nil {
		add("tags", *p.Tags)
	}
	if p.BundleType != nil {
		add("bundle_type", *p.BundleType)
	}
	if p.PricingType != nil {
		add("pricing_type", *p.PricingType)
	}
	if p.PriceCents != nil {
		add("price_cents", *p.PriceCents)
	}
	if p.Currency != nil {
		add("currency", *p.Currency)
	}
	if p.PreviewFieldCount != nil {
		add("preview_field_count", *p.PreviewFieldCount)
	}

	q := fmt.Sprintf(`
		WITH upd AS (
			UPDATE marketplace_listings
			SET %s
			WHERE id = $1
			RETURNING *
		)
		SELECT %s
		FROM upd l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id`,
		strings.Join(sets, ", "), listingCols)

	row := r.db.QueryRow(ctx, q, args...)
	rec, err := scanListing(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.UpdateListingMetadata: %w", err)
	}
	return rec, nil
}

// ArchiveListing flips status to 'archived' and stamps archived_at. Returns
// the updated record. The service layer enforces ownership + that the
// listing is in a state where archive is allowed (draft / published, not
// suspended).
func (r *Repository) ArchiveListing(ctx context.Context, id uuid.UUID) (*ListingRecord, error) {
	q := fmt.Sprintf(`
		WITH upd AS (
			UPDATE marketplace_listings
			SET status = 'archived', archived_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND status <> 'suspended'
			RETURNING *
		)
		SELECT %s
		FROM upd l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id`, listingCols)

	row := r.db.QueryRow(ctx, q, id)
	rec, err := scanListing(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.ArchiveListing: %w", err)
	}
	return rec, nil
}

// ── Publisher earnings ───────────────────────────────────────────────────────

// EarningsRow is a single fulfilled paid acquisition row used by the
// publisher earnings list. Refunded acquisitions are included with status
// 'refunded' so the publisher sees the full transaction trail.
type EarningsRow struct {
	AcquisitionID    uuid.UUID
	ListingID        uuid.UUID
	ListingName      string
	BuyerClinicID    uuid.UUID
	AmountPaidCents  int
	PlatformFeeCents int
	NetCents         int // amount_paid - platform_fee, what the publisher actually netted
	Currency         string
	Status           string // 'active' or 'refunded'
	FulfilledAt      time.Time
}

// ListPublisherEarnings returns paid acquisitions for the given publisher,
// most-recent first, with platform fees and the net publisher cut computed
// per row. Free acquisitions are excluded — they never carry money.
func (r *Repository) ListPublisherEarnings(ctx context.Context, publisherID uuid.UUID, limit, offset int) ([]*EarningsRow, int, error) {
	const countQ = `
		SELECT COUNT(*)
		FROM marketplace_acquisitions a
		JOIN marketplace_listings l ON l.id = a.listing_id
		WHERE l.publisher_account_id = $1
		  AND a.acquisition_type = 'purchase'
		  AND a.status IN ('active', 'refunded')`
	var total int
	if err := r.db.QueryRow(ctx, countQ, publisherID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherEarnings: count: %w", err)
	}

	const q = `
		SELECT
			a.id, a.listing_id, l.name, a.clinic_id,
			COALESCE(a.amount_paid_cents, 0),
			COALESCE(a.platform_fee_cents, 0),
			COALESCE(a.currency, 'NZD'),
			a.status,
			a.fulfilled_at
		FROM marketplace_acquisitions a
		JOIN marketplace_listings l ON l.id = a.listing_id
		WHERE l.publisher_account_id = $1
		  AND a.acquisition_type = 'purchase'
		  AND a.status IN ('active', 'refunded')
		ORDER BY a.fulfilled_at DESC NULLS LAST
		LIMIT $2 OFFSET $3`
	rows, err := r.db.Query(ctx, q, publisherID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherEarnings: %w", err)
	}
	defer rows.Close()
	var out []*EarningsRow
	for rows.Next() {
		var e EarningsRow
		var fulfilledAt *time.Time
		if err := rows.Scan(
			&e.AcquisitionID, &e.ListingID, &e.ListingName, &e.BuyerClinicID,
			&e.AmountPaidCents, &e.PlatformFeeCents, &e.Currency, &e.Status,
			&fulfilledAt,
		); err != nil {
			return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherEarnings: scan: %w", err)
		}
		if fulfilledAt != nil {
			e.FulfilledAt = *fulfilledAt
		}
		e.NetCents = e.AmountPaidCents - e.PlatformFeeCents
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherEarnings: rows: %w", err)
	}
	return out, total, nil
}

// EarningsMonthly is a single bucketed row for the earnings summary chart.
// Months are reported as YYYY-MM strings; gross/fee/net cents are summed
// across all paid+active acquisitions in the bucket. Refunded acquisitions
// flip the sign in the same bucket so refunds reduce the gross figure.
type EarningsMonthly struct {
	Month        string // 'YYYY-MM'
	GrossCents   int
	FeeCents     int
	NetCents     int
	OrderCount   int
	RefundCount  int
	Currency     string
}

// PublisherEarningsSummary returns up to 24 months of earnings data for a
// publisher, oldest → newest. Currencies are not collapsed (a publisher
// could in theory have NZD + AUD acquisitions); each row carries its own
// currency. Most publishers stick to one currency.
func (r *Repository) PublisherEarningsSummary(ctx context.Context, publisherID uuid.UUID, monthsBack int) ([]*EarningsMonthly, error) {
	if monthsBack <= 0 || monthsBack > 36 {
		monthsBack = 12
	}
	const q = `
		WITH base AS (
			SELECT
				to_char(a.fulfilled_at, 'YYYY-MM') AS month,
				COALESCE(a.currency, 'NZD') AS currency,
				CASE WHEN a.status = 'refunded' THEN -1 ELSE 1 END AS sign,
				COALESCE(a.amount_paid_cents, 0) AS gross,
				COALESCE(a.platform_fee_cents, 0) AS fee,
				CASE WHEN a.status = 'refunded' THEN 1 ELSE 0 END AS is_refund
			FROM marketplace_acquisitions a
			JOIN marketplace_listings l ON l.id = a.listing_id
			WHERE l.publisher_account_id = $1
			  AND a.acquisition_type = 'purchase'
			  AND a.fulfilled_at >= (NOW() - ($2::int || ' months')::interval)
			  AND a.status IN ('active', 'refunded')
		)
		SELECT
			month,
			currency,
			SUM(sign * gross)::int AS gross,
			SUM(sign * fee)::int AS fee,
			COUNT(*) AS order_count,
			SUM(is_refund)::int AS refund_count
		FROM base
		GROUP BY month, currency
		ORDER BY month ASC`
	rows, err := r.db.Query(ctx, q, publisherID, monthsBack)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.PublisherEarningsSummary: %w", err)
	}
	defer rows.Close()
	var out []*EarningsMonthly
	for rows.Next() {
		var m EarningsMonthly
		if err := rows.Scan(&m.Month, &m.Currency, &m.GrossCents, &m.FeeCents, &m.OrderCount, &m.RefundCount); err != nil {
			return nil, fmt.Errorf("marketplace.repo.PublisherEarningsSummary: scan: %w", err)
		}
		m.NetCents = m.GrossCents - m.FeeCents
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.repo.PublisherEarningsSummary: rows: %w", err)
	}
	return out, nil
}

// ── Pack source forms ────────────────────────────────────────────────────────

// PackForm is a row from marketplace_listing_forms — one component form of
// a `pack` listing. Position is 1-based; the import flow materialises forms
// in this order so the buyer's tenant gets a curated import.
type PackForm struct {
	ListingID    uuid.UUID
	Position     int
	SourceFormID uuid.UUID
	CreatedAt    time.Time
}

// ListPackForms returns all source-form rows for a pack listing, ordered.
func (r *Repository) ListPackForms(ctx context.Context, listingID uuid.UUID) ([]*PackForm, error) {
	const q = `
		SELECT listing_id, position, source_form_id, created_at
		FROM marketplace_listing_forms
		WHERE listing_id = $1
		ORDER BY position ASC`
	rows, err := r.db.Query(ctx, q, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.ListPackForms: %w", err)
	}
	defer rows.Close()
	var out []*PackForm
	for rows.Next() {
		var pf PackForm
		if err := rows.Scan(&pf.ListingID, &pf.Position, &pf.SourceFormID, &pf.CreatedAt); err != nil {
			return nil, fmt.Errorf("marketplace.repo.ListPackForms: scan: %w", err)
		}
		out = append(out, &pf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.repo.ListPackForms: rows: %w", err)
	}
	return out, nil
}

// SetPackForms replaces the entire pack-forms list for a listing in one
// transaction. Used by the publisher when editing the pack composition —
// drag/drop reorder + add/remove all flow through this single write so we
// don't have to deal with per-row diffing.
func (r *Repository) SetPackForms(ctx context.Context, listingID uuid.UUID, formIDs []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("marketplace.repo.SetPackForms: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM marketplace_listing_forms WHERE listing_id = $1`, listingID); err != nil {
		return fmt.Errorf("marketplace.repo.SetPackForms: clear: %w", err)
	}
	for i, fid := range formIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO marketplace_listing_forms (listing_id, position, source_form_id)
			VALUES ($1, $2, $3)`, listingID, i+1, fid); err != nil {
			if domain.IsUniqueViolation(err) {
				return fmt.Errorf("marketplace.repo.SetPackForms: duplicate form: %w", domain.ErrConflict)
			}
			return fmt.Errorf("marketplace.repo.SetPackForms: insert %d: %w", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("marketplace.repo.SetPackForms: commit: %w", err)
	}
	return nil
}

// DeleteListingDraft removes a draft listing along with any unreleased
// version rows. Only valid when status='draft' — once published, listings
// must be archived instead so historical acquisitions still resolve. The
// constraint is enforced at the service layer (status check) and inside
// the SQL (`WHERE status = 'draft'`); both belt-and-braces.
func (r *Repository) DeleteListingDraft(ctx context.Context, id uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("marketplace.repo.DeleteListingDraft: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Children of a draft listing: version rows + their fields, listing tags.
	// FK constraints cascade via ON DELETE CASCADE on most child tables; we
	// do the explicit deletes for any without cascade to keep this safe.
	if _, err := tx.Exec(ctx, `DELETE FROM marketplace_listing_tags WHERE listing_id = $1`, id); err != nil {
		return fmt.Errorf("marketplace.repo.DeleteListingDraft: tags: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM marketplace_versions WHERE listing_id = $1`, id); err != nil {
		return fmt.Errorf("marketplace.repo.DeleteListingDraft: versions: %w", err)
	}
	ct, err := tx.Exec(ctx,
		`DELETE FROM marketplace_listings WHERE id = $1 AND status = 'draft'`,
		id)
	if err != nil {
		return fmt.Errorf("marketplace.repo.DeleteListingDraft: listing: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("marketplace.repo.DeleteListingDraft: %w", domain.ErrNotFound)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("marketplace.repo.DeleteListingDraft: commit: %w", err)
	}
	return nil
}

// ── Acquisition webhook-driven updates ───────────────────────────────────────

// FulfillAcquisitionByPaymentIntent transitions a pending acquisition to active
// using Stripe webhook data. Returns (acquisition, true) on state change or
// (acquisition, false) if it was already fulfilled (idempotent).
func (r *Repository) FulfillAcquisitionByPaymentIntent(
	ctx context.Context,
	paymentIntentID string,
	amountPaidCents int,
	platformFeeCents int,
	currency string,
	fulfilledAt time.Time,
) (*AcquisitionRecord, bool, error) {
	q := fmt.Sprintf(`
		UPDATE marketplace_acquisitions
		SET status            = 'active',
		    amount_paid_cents = $2,
		    platform_fee_cents = $3,
		    currency          = $4,
		    fulfilled_at      = $5
		WHERE stripe_payment_intent_id = $1 AND status = 'pending'
		RETURNING %s`, acquisitionCols)

	row := r.db.QueryRow(ctx, q, paymentIntentID, amountPaidCents, platformFeeCents, currency, fulfilledAt)
	rec, err := scanAcquisition(row)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Already fulfilled or unknown intent — fetch existing for caller idempotency.
			existing, lookupErr := r.findAcquisitionByStripePI(ctx, paymentIntentID)
			if lookupErr != nil {
				return nil, false, fmt.Errorf("marketplace.repo.FulfillAcquisitionByPaymentIntent: %w", lookupErr)
			}
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("marketplace.repo.FulfillAcquisitionByPaymentIntent: %w", err)
	}
	return rec, true, nil
}

// RefundAcquisitionByPaymentIntent marks an acquisition refunded via webhook.
func (r *Repository) RefundAcquisitionByPaymentIntent(ctx context.Context, paymentIntentID string) (*AcquisitionRecord, error) {
	q := fmt.Sprintf(`
		UPDATE marketplace_acquisitions
		SET status = 'refunded'
		WHERE stripe_payment_intent_id = $1 AND status = 'active'
		RETURNING %s`, acquisitionCols)

	row := r.db.QueryRow(ctx, q, paymentIntentID)
	rec, err := scanAcquisition(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.RefundAcquisitionByPaymentIntent: %w", err)
	}
	return rec, nil
}

// SetAcquisitionPolicyChoice records the policy-import decision during import.
func (r *Repository) SetAcquisitionPolicyChoice(ctx context.Context, acquisitionID, clinicID uuid.UUID, choice string, acceptedAt *time.Time) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE marketplace_acquisitions
		SET policy_import_choice = $3,
		    policy_attribution_accepted_at = $4
		WHERE id = $1 AND clinic_id = $2`, acquisitionID, clinicID, choice, acceptedAt)
	if err != nil {
		return fmt.Errorf("marketplace.repo.SetAcquisitionPolicyChoice: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("marketplace.repo.SetAcquisitionPolicyChoice: %w", domain.ErrNotFound)
	}
	return nil
}

func (r *Repository) findAcquisitionByStripePI(ctx context.Context, paymentIntentID string) (*AcquisitionRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM marketplace_acquisitions WHERE stripe_payment_intent_id = $1`, acquisitionCols)
	row := r.db.QueryRow(ctx, q, paymentIntentID)
	rec, err := scanAcquisition(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.findAcquisitionByStripePI: %w", err)
	}
	return rec, nil
}

// ── Publisher updates ────────────────────────────────────────────────────────

// UpdatePublisherStripeConnect stores the Stripe Connect account ID post-onboarding.
func (r *Repository) UpdatePublisherStripeConnect(ctx context.Context, publisherID uuid.UUID, accountID string, onboardingComplete bool) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE publisher_accounts
		SET stripe_connect_account_id = $2,
		    stripe_onboarding_complete = $3
		WHERE id = $1`, publisherID, accountID, onboardingComplete)
	if err != nil {
		return fmt.Errorf("marketplace.repo.UpdatePublisherStripeConnect: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("marketplace.repo.UpdatePublisherStripeConnect: %w", domain.ErrNotFound)
	}
	return nil
}

// GetPublisherByStripeConnectAccountID lookup for webhook routing.
func (r *Repository) GetPublisherByStripeConnectAccountID(ctx context.Context, accountID string) (*PublisherRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM publisher_accounts WHERE stripe_connect_account_id = $1`, publisherCols)
	row := r.db.QueryRow(ctx, q, accountID)
	rec, err := scanPublisher(row)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.GetPublisherByStripeConnectAccountID: %w", err)
	}
	return rec, nil
}

// SetPublisherBadge grants or revokes verified_badge. granterID is stored for audit.
func (r *Repository) SetPublisherBadge(ctx context.Context, publisherID, granterID uuid.UUID, verified bool, authorityType *string, grantedAt *time.Time) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE publisher_accounts
		SET verified_badge = $2,
		    authority_type = $3,
		    authority_granted_by = $4,
		    authority_granted_at = $5
		WHERE id = $1`, publisherID, verified, authorityType, granterID, grantedAt)
	if err != nil {
		return fmt.Errorf("marketplace.repo.SetPublisherBadge: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("marketplace.repo.SetPublisherBadge: %w", domain.ErrNotFound)
	}
	return nil
}

// ListPublisherListings returns listings owned by a publisher (for self-management).
func (r *Repository) ListPublisherListings(ctx context.Context, publisherID uuid.UUID, limit, offset int) ([]*ListingRecord, int, error) {
	countQ := `SELECT COUNT(*) FROM marketplace_listings WHERE publisher_account_id = $1`
	var total int
	if err := r.db.QueryRow(ctx, countQ, publisherID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherListings: count: %w", err)
	}

	q := fmt.Sprintf(`
		SELECT %s
		FROM marketplace_listings l
		JOIN publisher_accounts p ON p.id = l.publisher_account_id
		WHERE l.publisher_account_id = $1
		ORDER BY l.created_at DESC
		LIMIT $2 OFFSET $3`, listingCols)

	rows, err := r.db.Query(ctx, q, publisherID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherListings: %w", err)
	}
	defer rows.Close()

	var out []*ListingRecord
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherListings: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListPublisherListings: rows: %w", err)
	}
	return out, total, nil
}

// ── Reviews ──────────────────────────────────────────────────────────────────

// ReviewRecord mirrors marketplace_reviews.
type ReviewRecord struct {
	ID            uuid.UUID
	ListingID     uuid.UUID
	AcquisitionID uuid.UUID
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	Rating        int
	Body          *string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateReviewParams is input for inserting a review.
type CreateReviewParams struct {
	ID            uuid.UUID
	ListingID     uuid.UUID
	AcquisitionID uuid.UUID
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	Rating        int
	Body          *string
}

const reviewCols = `id, listing_id, acquisition_id, clinic_id, staff_id, rating, body, status, created_at, updated_at`

// CreateReview inserts a new review and updates the listing's denormalised counters
// atomically. Returns ErrConflict if the clinic already reviewed this listing.
func (r *Repository) CreateReview(ctx context.Context, p CreateReviewParams) (*ReviewRecord, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.CreateReview: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	reviewQ := fmt.Sprintf(`
		INSERT INTO marketplace_reviews (id, listing_id, acquisition_id, clinic_id, staff_id, rating, body)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING %s`, reviewCols)

	row := tx.QueryRow(ctx, reviewQ, p.ID, p.ListingID, p.AcquisitionID, p.ClinicID, p.StaffID, p.Rating, p.Body)
	rec, err := scanReview(row)
	if err != nil {
		if domain.IsUniqueViolation(err) {
			return nil, fmt.Errorf("marketplace.repo.CreateReview: %w", domain.ErrConflict)
		}
		return nil, fmt.Errorf("marketplace.repo.CreateReview: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE marketplace_listings
		SET rating_count = rating_count + 1,
		    rating_sum   = rating_sum + $2
		WHERE id = $1`, p.ListingID, p.Rating); err != nil {
		return nil, fmt.Errorf("marketplace.repo.CreateReview: listing update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("marketplace.repo.CreateReview: commit: %w", err)
	}
	return rec, nil
}

// ListReviewsByListing returns published reviews for a listing, newest first.
func (r *Repository) ListReviewsByListing(ctx context.Context, listingID uuid.UUID, limit, offset int) ([]*ReviewRecord, int, error) {
	countQ := `SELECT COUNT(*) FROM marketplace_reviews WHERE listing_id = $1 AND status = 'published'`
	var total int
	if err := r.db.QueryRow(ctx, countQ, listingID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListReviewsByListing: count: %w", err)
	}

	q := fmt.Sprintf(`
		SELECT %s FROM marketplace_reviews
		WHERE listing_id = $1 AND status = 'published'
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, reviewCols)

	rows, err := r.db.Query(ctx, q, listingID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListReviewsByListing: %w", err)
	}
	defer rows.Close()

	var out []*ReviewRecord
	for rows.Next() {
		rec, err := scanReview(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("marketplace.repo.ListReviewsByListing: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("marketplace.repo.ListReviewsByListing: rows: %w", err)
	}
	return out, total, nil
}

// ── Upgrade notifications ────────────────────────────────────────────────────

// UpdateNotificationRecord mirrors marketplace_update_notifications.
type UpdateNotificationRecord struct {
	ID               uuid.UUID
	AcquisitionID    uuid.UUID
	ClinicID         uuid.UUID
	NewVersionID     uuid.UUID
	NotificationType string
	SeenAt           *time.Time
	CreatedAt        time.Time
}

// CreateUpgradeNotificationsForVersion inserts one row per active acquisition
// of the given listing. Returns the number of inserted rows.
func (r *Repository) CreateUpgradeNotificationsForVersion(ctx context.Context, listingID, newVersionID uuid.UUID, notificationType string) (int, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO marketplace_update_notifications (
			acquisition_id, clinic_id, new_version_id, notification_type
		)
		SELECT id, clinic_id, $2, $3
		FROM marketplace_acquisitions
		WHERE listing_id = $1 AND status = 'active'`, listingID, newVersionID, notificationType)
	if err != nil {
		return 0, fmt.Errorf("marketplace.repo.CreateUpgradeNotificationsForVersion: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// ListUnseenNotifications returns this clinic's unread upgrade notifications.
func (r *Repository) ListUnseenNotifications(ctx context.Context, clinicID uuid.UUID, limit int) ([]*UpdateNotificationRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, acquisition_id, clinic_id, new_version_id, notification_type, seen_at, created_at
		FROM marketplace_update_notifications
		WHERE clinic_id = $1 AND seen_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2`, clinicID, limit)
	if err != nil {
		return nil, fmt.Errorf("marketplace.repo.ListUnseenNotifications: %w", err)
	}
	defer rows.Close()

	var out []*UpdateNotificationRecord
	for rows.Next() {
		var n UpdateNotificationRecord
		if err := rows.Scan(&n.ID, &n.AcquisitionID, &n.ClinicID, &n.NewVersionID, &n.NotificationType, &n.SeenAt, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("marketplace.repo.ListUnseenNotifications: scan: %w", err)
		}
		out = append(out, &n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.repo.ListUnseenNotifications: rows: %w", err)
	}
	return out, nil
}

// MarkNotificationSeen flags one notification as seen.
func (r *Repository) MarkNotificationSeen(ctx context.Context, notificationID, clinicID uuid.UUID, now time.Time) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE marketplace_update_notifications
		SET seen_at = $3
		WHERE id = $1 AND clinic_id = $2 AND seen_at IS NULL`, notificationID, clinicID, now)
	if err != nil {
		return fmt.Errorf("marketplace.repo.MarkNotificationSeen: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("marketplace.repo.MarkNotificationSeen: %w", domain.ErrNotFound)
	}
	return nil
}

// MarkNotificationsSeenForAcquisitionVersion flags every unread notification
// for (acquisition_id, new_version_id) as seen. Called by the import flow when
// the buyer accepts a new version: dismissing the banner is implicit in the
// act of importing. Idempotent — no error when zero rows match (the buyer may
// be importing without any pending notification, e.g., a manual re-import).
func (r *Repository) MarkNotificationsSeenForAcquisitionVersion(ctx context.Context, acquisitionID, clinicID, versionID uuid.UUID, now time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE marketplace_update_notifications
		SET seen_at = $4
		WHERE acquisition_id = $1 AND clinic_id = $2 AND new_version_id = $3 AND seen_at IS NULL`,
		acquisitionID, clinicID, versionID, now)
	if err != nil {
		return fmt.Errorf("marketplace.repo.MarkNotificationsSeenForAcquisitionVersion: %w", err)
	}
	return nil
}

// ── Stripe event dedupe ──────────────────────────────────────────────────────

// MarkStripeEventProcessed inserts the event_id if it wasn't already there.
// Returns true if inserted (first time seen), false if already processed.
func (r *Repository) MarkStripeEventProcessed(ctx context.Context, eventID, eventType string) (bool, error) {
	ct, err := r.db.Exec(ctx, `
		INSERT INTO stripe_events_processed (event_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING`, eventID, eventType)
	if err != nil {
		return false, fmt.Errorf("marketplace.repo.MarkStripeEventProcessed: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

type scannable interface {
	Scan(dest ...any) error
}

func scanPublisher(row scannable) (*PublisherRecord, error) {
	var p PublisherRecord
	err := row.Scan(
		&p.ID, &p.ClinicID, &p.DisplayName, &p.Bio, &p.WebsiteURL, &p.VerifiedBadge,
		&p.AuthorityType, &p.AuthorityGrantedBy, &p.AuthorityGrantedAt,
		&p.StripeConnectAccountID, &p.StripeOnboardingComplete, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("marketplace.repo.scanPublisher: %w", err)
	}
	return &p, nil
}

func scanListing(row scannable) (*ListingRecord, error) {
	var l ListingRecord
	err := row.Scan(
		&l.ID, &l.PublisherAccountID, &l.Vertical, &l.Name, &l.Slug,
		&l.ShortDescription, &l.LongDescription, &l.Tags, &l.BundleType,
		&l.PricingType, &l.PriceCents, &l.Currency, &l.Status,
		&l.PreviewFieldCount, &l.DownloadCount, &l.RatingCount, &l.RatingSum,
		&l.PublishedAt, &l.CreatedAt, &l.UpdatedAt, &l.ArchivedAt,
		&l.SourcePolicyID,
		&l.PublisherDisplayName, &l.PublisherVerifiedBadge, &l.PublisherAuthorityType,
		&l.PublisherClinicID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("marketplace.repo.scanListing: %w", err)
	}
	return &l, nil
}

func scanVersion(row scannable) (*VersionRecord, error) {
	var v VersionRecord
	err := row.Scan(
		&v.ID, &v.ListingID, &v.VersionMajor, &v.VersionMinor,
		&v.ChangeType, &v.ChangeSummary, &v.PackagePayload, &v.PayloadChecksum,
		&v.FieldCount, &v.SourceFormVersionID, &v.Status,
		&v.PublishedAt, &v.PublishedBy, &v.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("marketplace.repo.scanVersion: %w", err)
	}
	return &v, nil
}

func scanVersionField(row scannable) (*VersionFieldRecord, error) {
	var f VersionFieldRecord
	err := row.Scan(
		&f.ID, &f.MarketplaceVersionID, &f.Position, &f.Title, &f.Type, &f.Config,
		&f.AIPrompt, &f.Required, &f.Skippable, &f.AllowInference, &f.MinConfidence,
		&f.SourceFormPosition,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("marketplace.repo.scanVersionField: %w", err)
	}
	return &f, nil
}

func scanReview(row scannable) (*ReviewRecord, error) {
	var r ReviewRecord
	err := row.Scan(
		&r.ID, &r.ListingID, &r.AcquisitionID, &r.ClinicID, &r.StaffID,
		&r.Rating, &r.Body, &r.Status, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("marketplace.repo.scanReview: %w", err)
	}
	return &r, nil
}

func scanAcquisition(row scannable) (*AcquisitionRecord, error) {
	var a AcquisitionRecord
	err := row.Scan(
		&a.ID, &a.ListingID, &a.MarketplaceVersionID, &a.ClinicID, &a.AcquiredBy,
		&a.AcquisitionType, &a.StripePaymentIntentID, &a.AmountPaidCents,
		&a.PlatformFeeCents, &a.Currency, &a.Status, &a.ImportedFormID,
		&a.PolicyImportChoice, &a.PolicyAttributionAcceptedAt,
		&a.FulfilledAt, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("marketplace.repo.scanAcquisition: %w", err)
	}
	return &a, nil
}
