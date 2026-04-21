package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Cross-domain interfaces ───────────────────────────────────────────────────
// CLAUDE.md rule: marketplace never imports another domain's types or queries
// another domain's tables. All cross-domain reads/writes go through interfaces.

// FormSnapshotter provides read access to a form version for building a
// marketplace package payload. Implemented by an adapter in app.go that
// wraps forms.Service + forms.Repository.
type FormSnapshotter interface {
	// SnapshotForm returns the form's metadata and the fields of its latest
	// published version. Returns ErrNotFound if no published version exists.
	SnapshotForm(ctx context.Context, formID, clinicID uuid.UUID) (*FormSnapshot, error)
	// LinkedPolicyIDs returns policy IDs linked to a form (may be empty).
	LinkedPolicyIDs(ctx context.Context, formID, clinicID uuid.UUID) ([]uuid.UUID, error)
}

// FormSnapshot is a DTO carried across the forms/marketplace boundary.
type FormSnapshot struct {
	FormVersionID uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
	Fields        []FormSnapshotField
}

// FormSnapshotField mirrors the shape of a form field for marketplace export.
type FormSnapshotField struct {
	Position       int
	Title          string
	Type           string
	Config         json.RawMessage
	AIPrompt       *string
	Required       bool
	Skippable      bool
	AllowInference bool
	MinConfidence  *float64
}

// PolicySnapshotter reads a policy's latest published version + clauses for
// bundling into a marketplace package. Returns ErrNotFound if no published
// version exists.
type PolicySnapshotter interface {
	SnapshotPolicy(ctx context.Context, policyID, clinicID uuid.UUID) (*PolicySnapshot, error)
}

// PolicySnapshot is a DTO for the policy-carrying payload. The content JSONB
// and clause block_ids must round-trip verbatim so form alignment still
// works post-import.
type PolicySnapshot struct {
	PolicyID    uuid.UUID
	Name        string
	Description *string
	Content     json.RawMessage // AppFlowy block array — opaque
	Clauses     []PolicySnapshotClause
}

// PolicySnapshotClause mirrors a policy_clauses row.
type PolicySnapshotClause struct {
	BlockID string
	Title   string
	Body    string
	Parity  string
}

// FormImporter creates a new form + published version in a clinic's tenant
// space from a marketplace package. Implemented by an adapter in app.go.
type FormImporter interface {
	// ImportForm creates a new tenant form with an immediately-published v1.0.
	// Returns the new tenant form ID.
	ImportForm(ctx context.Context, input FormImportInput) (formID uuid.UUID, err error)
	// LinkFormToPolicy creates a form_policies row after both are imported.
	LinkFormToPolicy(ctx context.Context, formID, clinicID, policyID, staffID uuid.UUID) error
}

// FormImportInput holds the data needed to materialise a marketplace package
// into a tenant form.
type FormImportInput struct {
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	Name          string
	Description   *string
	OverallPrompt *string
	Tags          []string
	ChangeSummary string
	Fields        []FormSnapshotField
}

// PolicyImporter creates a tenant policy + published version + clauses from a
// marketplace policy snapshot. The source_marketplace_version_id is stamped on
// the new policy row so future edits can be governance-warned.
type PolicyImporter interface {
	ImportPolicy(ctx context.Context, input PolicyImportInput) (policyID uuid.UUID, err error)
}

// PolicyImportInput holds the data needed to materialise a policy snapshot.
type PolicyImportInput struct {
	ClinicID                   uuid.UUID
	StaffID                    uuid.UUID
	SourceMarketplaceVersionID uuid.UUID
	Name                       string
	Description                *string
	Content                    json.RawMessage
	Clauses                    []PolicySnapshotClause
	ChangeSummary              string
}

// PolicyNamer resolves policy IDs to display names for informational notes.
type PolicyNamer interface {
	GetPolicyNames(ctx context.Context, clinicID uuid.UUID, policyIDs []uuid.UUID) (map[uuid.UUID]string, error)
}

// ClinicInfoProvider exposes the minimal clinic attributes marketplace needs
// for status/vertical gates.
type ClinicInfoProvider interface {
	GetClinicInfo(ctx context.Context, clinicID uuid.UUID) (*ClinicInfo, error)
}

// ClinicInfo is the minimal projection of a clinic for marketplace gates.
type ClinicInfo struct {
	Status   string
	Vertical string
}

// StripeClient encapsulates the Stripe API calls marketplace needs.
// Implemented by an adapter in app.go that wraps stripe-go.
type StripeClient interface {
	// CreateConnectExpressAccount provisions an Express Connect account for a publisher.
	CreateConnectExpressAccount(ctx context.Context, email, country string) (accountID string, err error)
	// CreateConnectAccountLink returns a hosted onboarding URL for the publisher.
	CreateConnectAccountLink(ctx context.Context, accountID, refreshURL, returnURL string) (url string, err error)
	// CreatePaymentIntent creates a PaymentIntent with destination charge + application fee.
	CreatePaymentIntent(ctx context.Context, input StripePaymentIntentInput) (clientSecret, paymentIntentID string, err error)
	// VerifyAndParseWebhook validates Stripe-Signature and returns the event.
	VerifyAndParseWebhook(payload []byte, signature string) (*StripeEvent, error)
}

// StripePaymentIntentInput is the shape of a purchase-flow PaymentIntent.
type StripePaymentIntentInput struct {
	AmountCents         int
	Currency            string
	ApplicationFeeCents int
	DestinationAccount  string
	Metadata            map[string]string
}

// StripeEvent is the normalised webhook event carried into the service layer.
type StripeEvent struct {
	ID       string
	Type     string
	RawJSON  json.RawMessage
	PayloadV any // type-specific: *StripePaymentIntent, *StripeRefund, *StripeAccount
}

// StripePaymentIntent is the minimal projection for payment_intent.succeeded.
type StripePaymentIntent struct {
	ID                string
	AmountReceived    int
	ApplicationFeeAmt int
	Currency          string
	Metadata          map[string]string
}

// StripeAccount is the minimal projection for account.updated.
type StripeAccount struct {
	ID             string
	ChargesEnabled bool
}

// StripeRefund is the minimal projection for charge.refunded.
type StripeRefund struct {
	PaymentIntentID string
}

// ── Response / API types ──────────────────────────────────────────────────────

// PublisherResponse is the API-safe publisher representation.
//
//nolint:revive
type PublisherResponse struct {
	ID            string  `json:"id"`
	DisplayName   string  `json:"display_name"`
	Bio           *string `json:"bio,omitempty"`
	WebsiteURL    *string `json:"website_url,omitempty"`
	VerifiedBadge bool    `json:"verified_badge"`
	AuthorityType *string `json:"authority_type,omitempty"`
}

// ListingResponse is the API-safe listing representation.
//
//nolint:revive
type ListingResponse struct {
	ID                string            `json:"id"`
	Slug              string            `json:"slug"`
	Name              string            `json:"name"`
	ShortDescription  string            `json:"short_description"`
	LongDescription   *string           `json:"long_description,omitempty"`
	Vertical          string            `json:"vertical"`
	Tags              []string          `json:"tags"`
	BundleType        string            `json:"bundle_type"`
	PricingType       string            `json:"pricing_type"`
	PriceCents        *int              `json:"price_cents,omitempty"`
	Currency          string            `json:"currency"`
	Status            string            `json:"status"`
	PreviewFieldCount int               `json:"preview_field_count"`
	DownloadCount     int               `json:"download_count"`
	RatingCount       int               `json:"rating_count"`
	RatingAverage     float64           `json:"rating_average"`
	Publisher         PublisherResponse `json:"publisher"`
	PublishedAt       *string           `json:"published_at,omitempty"`
	CreatedAt         string            `json:"created_at"`
}

// ListingListResponse is a paginated list of listings.
//
//nolint:revive
type ListingListResponse struct {
	Items  []*ListingResponse `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// VersionFieldResponse is the API-safe field representation for preview.
// For paid listings, fields beyond preview_field_count have Locked=true and
// their ai_prompt/min_confidence are cleared.
//
//nolint:revive
type VersionFieldResponse struct {
	Position       int             `json:"position"`
	Title          string          `json:"title"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	AIPrompt       *string         `json:"ai_prompt,omitempty"`
	Required       bool            `json:"required"`
	Skippable      bool            `json:"skippable"`
	AllowInference bool            `json:"allow_inference"`
	MinConfidence  *float64        `json:"min_confidence,omitempty"`
	Locked         bool            `json:"locked,omitempty"`
}

// VersionResponse is the API-safe version representation.
//
//nolint:revive
type VersionResponse struct {
	ID            string                  `json:"id"`
	ListingID     string                  `json:"listing_id"`
	VersionMajor  int                     `json:"version_major"`
	VersionMinor  int                     `json:"version_minor"`
	ChangeType    string                  `json:"change_type"`
	ChangeSummary *string                 `json:"change_summary,omitempty"`
	FieldCount    int                     `json:"field_count"`
	PublishedAt   string                  `json:"published_at"`
	Fields        []*VersionFieldResponse `json:"fields,omitempty"`
}

// AcquisitionResponse is the API-safe acquisition representation.
//
//nolint:revive
type AcquisitionResponse struct {
	ID                   string  `json:"id"`
	ListingID            string  `json:"listing_id"`
	ListingName          string  `json:"listing_name"`
	MarketplaceVersionID string  `json:"marketplace_version_id"`
	AcquisitionType      string  `json:"acquisition_type"`
	Status               string  `json:"status"`
	ImportedFormID       *string `json:"imported_form_id,omitempty"`
	FulfilledAt          *string `json:"fulfilled_at,omitempty"`
	CreatedAt            string  `json:"created_at"`
}

// AcquisitionListResponse is a paginated list of acquisitions.
//
//nolint:revive
type AcquisitionListResponse struct {
	Items  []*AcquisitionResponse `json:"items"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateListingInput is the admin-only input for creating a new marketplace listing.
type CreateListingInput struct {
	CallerClinicID     uuid.UUID // clinic of the caller; must own the publisher
	CallerStaffID      uuid.UUID
	PublisherAccountID uuid.UUID
	Vertical           string
	Name               string
	Slug               string
	ShortDescription   string
	LongDescription    *string
	Tags               []string
	BundleType         string // 'bundled' | 'form_only'
	PricingType        string
	PriceCents         *int
	Currency           string
	PreviewFieldCount  int
}

// PublishVersionInput is the input for publishing a new listing version by
// snapshotting a tenant form's latest published version. Used by both admin
// and publisher-facing routes.
type PublishVersionInput struct {
	ListingID     uuid.UUID
	ClinicID      uuid.UUID // clinic owning the source form (must match publisher's clinic)
	SourceFormID  uuid.UUID
	StaffID       uuid.UUID
	ChangeType    string
	ChangeSummary *string
}

// ListListingsInput is the public browse input.
type ListListingsInput struct {
	Query        *string
	Vertical     *string
	PricingType  *string
	VerifiedOnly bool
	PolicyLinked *bool
	Sort         string // relevance | rating | downloads | newest
	Limit        int
	Offset       int
}

// AcquireInput is a staff-initiated free acquisition.
type AcquireInput struct {
	ListingID uuid.UUID
	ClinicID  uuid.UUID
	StaffID   uuid.UUID
}

// ImportInput imports an active acquisition into the tenant forms table.
// IncludePolicies=true imports the bundled policies and links them.
// RelinkExistingPolicyIDs maps bundled policy index → existing local policy ID
// (used when clinic already has a local copy and wants to skip re-import).
type ImportInput struct {
	AcquisitionID             uuid.UUID
	ClinicID                  uuid.UUID
	StaffID                   uuid.UUID
	IncludePolicies           bool
	AcceptedPolicyAttribution bool
	RelinkExistingPolicyIDs   map[int]uuid.UUID // index → existing policy ID
}

// PurchaseInput kicks off a paid Payment Intent flow.
type PurchaseInput struct {
	ListingID uuid.UUID
	ClinicID  uuid.UUID
	StaffID   uuid.UUID
}

// PurchaseResponse carries the Stripe client_secret to the client.
type PurchaseResponse struct {
	ClientSecret    string `json:"client_secret"`
	PaymentIntentID string `json:"payment_intent_id"`
	AcquisitionID   string `json:"acquisition_id"`
	AmountCents     int    `json:"amount_cents"`
	Currency        string `json:"currency"`
}

// RegisterPublisherInput is the self-serve registration input.
type RegisterPublisherInput struct {
	ClinicID    uuid.UUID
	DisplayName string
	Bio         *string
	WebsiteURL  *string
}

// CreateReviewInput is the reviewer-side input.
type CreateReviewInput struct {
	AcquisitionID uuid.UUID
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	Rating        int
	Body          *string
}

// StripeConnectOnboardingInput starts a Stripe Connect Express onboarding flow.
type StripeConnectOnboardingInput struct {
	PublisherID uuid.UUID
	ClinicID    uuid.UUID
	Email       string
	Country     string
	RefreshURL  string
	ReturnURL   string
}

// GrantBadgeInput grants or revokes a publisher badge.
type GrantBadgeInput struct {
	GranterClinicID   uuid.UUID // caller's clinic — must own a publisher with authority
	TargetPublisherID uuid.UUID
	VerifiedBadge     bool
	AuthorityType     *string // nil clears authority; 'authority' or 'salvia'
}

// UpgradeNotificationResponse is the API-safe notification representation.
type UpgradeNotificationResponse struct {
	ID               string  `json:"id"`
	AcquisitionID    string  `json:"acquisition_id"`
	NewVersionID     string  `json:"new_version_id"`
	NotificationType string  `json:"notification_type"`
	CreatedAt        string  `json:"created_at"`
	SeenAt           *string `json:"seen_at,omitempty"`
}

// ReviewResponse is the API-safe review representation.
type ReviewResponse struct {
	ID        string  `json:"id"`
	ListingID string  `json:"listing_id"`
	Rating    int     `json:"rating"`
	Body      *string `json:"body,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// ReviewListResponse is a paginated list of reviews.
type ReviewListResponse struct {
	Items  []*ReviewResponse `json:"items"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// ── Package envelope ──────────────────────────────────────────────────────────

// Package is the canonical portable form package stored on marketplace_versions.
// Matches the envelope documented in docs/marketplace.md §2.
type Package struct {
	Meta     PackageMeta     `json:"meta"`
	Listing  PackageListing  `json:"listing"`
	Fields   []PackageField  `json:"fields"`
	Policies []PackagePolicy `json:"policies,omitempty"`
}

// PackageMeta is the envelope metadata.
type PackageMeta struct {
	SchemaVersion        string    `json:"schema_version"`
	FormVersion          string    `json:"form_version"`
	SalviaCompatibleFrom string    `json:"salvia_compatible_from"`
	Vertical             string    `json:"vertical"`
	BundleType           string    `json:"bundle_type"` // 'bundled' | 'form_only'
	PolicyAttribution    string    `json:"policy_attribution,omitempty"`
	PublishedAt          time.Time `json:"published_at"`
	Checksum             string    `json:"checksum,omitempty"`
}

// PackageListing is the listing-level metadata inside the envelope.
type PackageListing struct {
	Name                  string   `json:"name"`
	Description           *string  `json:"description,omitempty"`
	Tags                  []string `json:"tags"`
	OverallPrompt         *string  `json:"overall_prompt,omitempty"`
	PolicyDependencyCount int      `json:"policy_dependency_count"`
}

// PackageField mirrors a form_fields row for the marketplace export.
type PackageField struct {
	Position       int             `json:"position"`
	Title          string          `json:"title"`
	Type           string          `json:"type"`
	Config         json.RawMessage `json:"config"`
	AIPrompt       *string         `json:"ai_prompt,omitempty"`
	Required       bool            `json:"required"`
	Skippable      bool            `json:"skippable"`
	AllowInference bool            `json:"allow_inference"`
	MinConfidence  *float64        `json:"min_confidence,omitempty"`
}

// PackagePolicy bundles a policy with the form — opt-in import on consumer side.
// Block IDs are preserved verbatim so form extraction alignment keeps working.
type PackagePolicy struct {
	Name        string                `json:"name"`
	Description *string               `json:"description,omitempty"`
	Content     json.RawMessage       `json:"content"`
	Clauses     []PackagePolicyClause `json:"clauses"`
}

// PackagePolicyClause mirrors a policy_clauses row.
type PackagePolicyClause struct {
	BlockID string `json:"block_id"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Parity  string `json:"parity"`
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service implements marketplace business logic.
type Service struct {
	repo                  repo
	snapshot              FormSnapshotter
	policySnap            PolicySnapshotter
	importer              FormImporter
	policyImporter        PolicyImporter
	policyNamer           PolicyNamer
	clinicInfo            ClinicInfoProvider
	stripe                StripeClient
	platformFeeRegularPct int    // default 30
	policyAttribution     string // default license notice appended to bundled packages
}

// ServiceConfig holds runtime-configurable values for the marketplace service.
type ServiceConfig struct {
	PlatformFeeRegularPct int    // default 30
	PolicyAttribution     string // default platform-level attribution text
}

// repo is the subset of the repository used by the service.
// Defined as an interface so tests can inject an in-memory fake.
type repo interface {
	// Publishers
	CreatePublisher(ctx context.Context, p CreatePublisherParams) (*PublisherRecord, error)
	GetPublisherByID(ctx context.Context, id uuid.UUID) (*PublisherRecord, error)
	GetPublisherByClinicID(ctx context.Context, clinicID uuid.UUID) (*PublisherRecord, error)
	UpdatePublisherStripeConnect(ctx context.Context, publisherID uuid.UUID, accountID string, onboardingComplete bool) error
	GetPublisherByStripeConnectAccountID(ctx context.Context, accountID string) (*PublisherRecord, error)
	SetPublisherBadge(ctx context.Context, publisherID, granterID uuid.UUID, verified bool, authorityType *string, grantedAt *time.Time) error
	// Listings
	CreateListing(ctx context.Context, p CreateListingParams) (*ListingRecord, error)
	GetListingByID(ctx context.Context, id uuid.UUID) (*ListingRecord, error)
	GetListingBySlug(ctx context.Context, slug string) (*ListingRecord, error)
	ListListings(ctx context.Context, p ListListingsParams) ([]*ListingRecord, int, error)
	ListPublisherListings(ctx context.Context, publisherID uuid.UUID, limit, offset int) ([]*ListingRecord, int, error)
	PublishListing(ctx context.Context, id uuid.UUID, now time.Time) (*ListingRecord, error)
	UpdateListingStatus(ctx context.Context, id uuid.UUID, status string) (*ListingRecord, error)
	IncrementDownloadCount(ctx context.Context, id uuid.UUID) error
	// Versions
	CreateVersion(ctx context.Context, p CreateVersionParams) (*VersionRecord, []*VersionFieldRecord, error)
	GetVersionByID(ctx context.Context, id uuid.UUID) (*VersionRecord, error)
	GetLatestVersion(ctx context.Context, listingID uuid.UUID) (*VersionRecord, error)
	ListVersionsByListing(ctx context.Context, listingID uuid.UUID) ([]*VersionRecord, error)
	GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]*VersionFieldRecord, error)
	// Acquisitions
	CreateAcquisition(ctx context.Context, p CreateAcquisitionParams) (*AcquisitionRecord, error)
	GetAcquisitionByID(ctx context.Context, id, clinicID uuid.UUID) (*AcquisitionRecord, error)
	ListAcquisitionsByClinic(ctx context.Context, clinicID uuid.UUID, limit, offset int) ([]*AcquisitionRecord, int, error)
	SetAcquisitionImportedForm(ctx context.Context, acquisitionID, clinicID, importedFormID uuid.UUID) error
	SetAcquisitionPolicyChoice(ctx context.Context, acquisitionID, clinicID uuid.UUID, choice string, acceptedAt *time.Time) error
	FulfillAcquisitionByPaymentIntent(ctx context.Context, paymentIntentID string, amountPaidCents, platformFeeCents int, currency string, fulfilledAt time.Time) (*AcquisitionRecord, bool, error)
	RefundAcquisitionByPaymentIntent(ctx context.Context, paymentIntentID string) (*AcquisitionRecord, error)
	// Reviews
	CreateReview(ctx context.Context, p CreateReviewParams) (*ReviewRecord, error)
	ListReviewsByListing(ctx context.Context, listingID uuid.UUID, limit, offset int) ([]*ReviewRecord, int, error)
	// Notifications
	CreateUpgradeNotificationsForVersion(ctx context.Context, listingID, newVersionID uuid.UUID, notificationType string) (int, error)
	ListUnseenNotifications(ctx context.Context, clinicID uuid.UUID, limit int) ([]*UpdateNotificationRecord, error)
	MarkNotificationSeen(ctx context.Context, notificationID, clinicID uuid.UUID, now time.Time) error
	// Stripe dedupe
	MarkStripeEventProcessed(ctx context.Context, eventID, eventType string) (bool, error)
}

// NewService constructs a marketplace Service.
//
// Adapters may be nil only when a code path is exercised without the
// corresponding cross-module call (e.g. tests that never publish versions can
// pass nil for snapshot/policySnap; tests that never purchase can pass nil
// for stripe). Methods check for nil and return ErrConflict.
func NewService(
	r repo,
	snapshot FormSnapshotter,
	policySnap PolicySnapshotter,
	importer FormImporter,
	policyImp PolicyImporter,
	policyNamer PolicyNamer,
	clinicInfo ClinicInfoProvider,
	stripe StripeClient,
	cfg ServiceConfig,
) *Service {
	feePct := cfg.PlatformFeeRegularPct
	if feePct <= 0 || feePct > 100 {
		feePct = 30
	}
	attribution := cfg.PolicyAttribution
	if attribution == "" {
		attribution = "Policy content is provided by the publisher under their own license. Acquiring clinic is responsible for local compliance."
	}
	return &Service{
		repo:                  r,
		snapshot:              snapshot,
		policySnap:            policySnap,
		importer:              importer,
		policyImporter:        policyImp,
		policyNamer:           policyNamer,
		clinicInfo:            clinicInfo,
		stripe:                stripe,
		platformFeeRegularPct: feePct,
		policyAttribution:     attribution,
	}
}

// ── Publishers ────────────────────────────────────────────────────────────────

// EnsurePublisher upserts the Salvia-platform publisher for an admin clinic.
// Phase 1 uses this to bootstrap a single publisher_accounts row.
func (s *Service) EnsurePublisher(ctx context.Context, clinicID uuid.UUID, displayName string) (*PublisherResponse, error) {
	existing, err := s.repo.GetPublisherByClinicID(ctx, clinicID)
	if err == nil {
		return toPublisherResponse(existing), nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("marketplace.service.EnsurePublisher: %w", err)
	}

	authorityType := "salvia"
	created, err := s.repo.CreatePublisher(ctx, CreatePublisherParams{
		ID:            domain.NewID(),
		ClinicID:      clinicID,
		DisplayName:   displayName,
		AuthorityType: &authorityType,
		Status:        "active",
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.EnsurePublisher: %w", err)
	}
	return toPublisherResponse(created), nil
}

// ── Listings (admin) ──────────────────────────────────────────────────────────

// CreateListing creates a draft marketplace listing.
// Any publisher may call this for their own publisher account; ownership is
// enforced by matching CallerClinicID against the publisher's clinic_id.
func (s *Service) CreateListing(ctx context.Context, input CreateListingInput) (*ListingResponse, error) {
	if err := validateListingInput(input); err != nil {
		return nil, fmt.Errorf("marketplace.service.CreateListing: %w", err)
	}

	// Ownership + status check on publisher.
	publisher, err := s.repo.GetPublisherByID(ctx, input.PublisherAccountID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.CreateListing: publisher: %w", err)
	}
	if !s.callerOwnsPublisher(ctx, input.CallerClinicID, publisher) {
		return nil, fmt.Errorf("marketplace.service.CreateListing: caller does not own publisher: %w", domain.ErrForbidden)
	}
	if publisher.Status != "active" {
		return nil, fmt.Errorf("marketplace.service.CreateListing: publisher not active: %w", domain.ErrConflict)
	}

	// Trial/suspended clinics cannot publish.
	if err := s.checkCanPublish(ctx, input.CallerClinicID); err != nil {
		return nil, fmt.Errorf("marketplace.service.CreateListing: %w", err)
	}

	bundleType := input.BundleType
	if bundleType == "" {
		bundleType = "bundled"
	}

	rec, err := s.repo.CreateListing(ctx, CreateListingParams{
		ID:                 domain.NewID(),
		PublisherAccountID: input.PublisherAccountID,
		Vertical:           input.Vertical,
		Name:               input.Name,
		Slug:               input.Slug,
		ShortDescription:   input.ShortDescription,
		LongDescription:    input.LongDescription,
		Tags:               normaliseTags(input.Tags),
		BundleType:         bundleType,
		PricingType:        input.PricingType,
		PriceCents:         input.PriceCents,
		Currency:           firstNonEmpty(input.Currency, "NZD"),
		PreviewFieldCount:  defaultIntIfZero(input.PreviewFieldCount, 3),
		Status:             "draft",
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.CreateListing: %w", err)
	}
	return toListingResponse(rec), nil
}

// PublishListing transitions a listing from draft → published.
// A listing with zero versions cannot be published.
func (s *Service) PublishListing(ctx context.Context, listingID uuid.UUID) (*ListingResponse, error) {
	versions, err := s.repo.ListVersionsByListing(ctx, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListing: %w", err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("marketplace.service.PublishListing: listing has no versions: %w", domain.ErrConflict)
	}
	rec, err := s.repo.PublishListing(ctx, listingID, domain.TimeNow())
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListing: %w", err)
	}
	return toListingResponse(rec), nil
}

// ── Versions (admin) ──────────────────────────────────────────────────────────

// PublishVersion snapshots a tenant form's latest published version (and
// optionally its linked policies) into the marketplace. Builds the package
// envelope with SHA-256 checksum, writes the version row + field mirror
// atomically, and enqueues upgrade notifications for active acquisitions.
func (s *Service) PublishVersion(ctx context.Context, input PublishVersionInput) (*VersionResponse, error) {
	if s.snapshot == nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: form snapshotter not configured: %w", domain.ErrConflict)
	}

	listing, err := s.repo.GetListingByID(ctx, input.ListingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: %w", err)
	}

	// Ownership check: caller's clinic must own the listing's publisher.
	publisher, err := s.repo.GetPublisherByID(ctx, listing.PublisherAccountID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: publisher: %w", err)
	}
	if publisher.ClinicID != input.ClinicID {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: caller not owner: %w", domain.ErrForbidden)
	}

	// Trial/suspended gate.
	if err := s.checkCanPublish(ctx, input.ClinicID); err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: %w", err)
	}

	snapshot, err := s.snapshot.SnapshotForm(ctx, input.SourceFormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: snapshot: %w", err)
	}

	policyIDs, err := s.snapshot.LinkedPolicyIDs(ctx, input.SourceFormID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: policies: %w", err)
	}

	// Bundle policies when listing bundle_type='bundled' AND policies are linked.
	var bundledPolicies []PackagePolicy
	if listing.BundleType == "bundled" && len(policyIDs) > 0 {
		if s.policySnap == nil {
			return nil, fmt.Errorf("marketplace.service.PublishVersion: policy snapshotter not configured but listing is bundled: %w", domain.ErrConflict)
		}
		bundledPolicies = make([]PackagePolicy, 0, len(policyIDs))
		for _, pid := range policyIDs {
			ps, err := s.policySnap.SnapshotPolicy(ctx, pid, input.ClinicID)
			if err != nil {
				return nil, fmt.Errorf("marketplace.service.PublishVersion: policy snapshot %s: %w", pid, err)
			}
			clauses := make([]PackagePolicyClause, len(ps.Clauses))
			for i, c := range ps.Clauses {
				clauses[i] = PackagePolicyClause(c)
			}
			bundledPolicies = append(bundledPolicies, PackagePolicy{
				Name:        ps.Name,
				Description: ps.Description,
				Content:     ps.Content,
				Clauses:     clauses,
			})
		}
	}

	major, minor := nextMarketplaceVersion(ctx, s.repo, input.ListingID, input.ChangeType)
	publishedAt := domain.TimeNow()

	pkg := Package{
		Meta: PackageMeta{
			SchemaVersion:        "1",
			FormVersion:          fmt.Sprintf("%d.%d", major, minor),
			SalviaCompatibleFrom: "1.0",
			Vertical:             listing.Vertical,
			BundleType:           listing.BundleType,
			PolicyAttribution:    s.policyAttribution,
			PublishedAt:          publishedAt,
		},
		Listing: PackageListing{
			Name:                  snapshot.Name,
			Description:           snapshot.Description,
			Tags:                  snapshot.Tags,
			OverallPrompt:         snapshot.OverallPrompt,
			PolicyDependencyCount: len(policyIDs),
		},
		Fields:   toPackageFields(snapshot.Fields),
		Policies: bundledPolicies,
	}

	checksum, err := checksumPackage(pkg)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: checksum: %w", err)
	}
	pkg.Meta.Checksum = checksum

	payload, err := json.Marshal(pkg)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: marshal: %w", err)
	}

	fields := make([]CreateVersionFieldParams, len(snapshot.Fields))
	for i, f := range snapshot.Fields {
		cfg := f.Config
		if cfg == nil {
			cfg = json.RawMessage(`{}`)
		}
		fields[i] = CreateVersionFieldParams{
			ID:             domain.NewID(),
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         cfg,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  f.MinConfidence,
		}
	}

	version, _, err := s.repo.CreateVersion(ctx, CreateVersionParams{
		ID:                  domain.NewID(),
		ListingID:           input.ListingID,
		VersionMajor:        major,
		VersionMinor:        minor,
		ChangeType:          input.ChangeType,
		ChangeSummary:       input.ChangeSummary,
		PackagePayload:      payload,
		PayloadChecksum:     checksum,
		FieldCount:          len(snapshot.Fields),
		SourceFormVersionID: &snapshot.FormVersionID,
		PublishedBy:         input.StaffID,
		PublishedAt:         publishedAt,
		Fields:              fields,
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishVersion: %w", err)
	}

	// Upgrade notifications for existing acquirers.
	notificationType := "minor_update"
	if input.ChangeType == "major" {
		notificationType = "major_upgrade"
	}
	if _, err := s.repo.CreateUpgradeNotificationsForVersion(ctx, input.ListingID, version.ID, notificationType); err != nil {
		// Notification failure is best-effort; log via wrapped return, don't block publish.
		return nil, fmt.Errorf("marketplace.service.PublishVersion: notify: %w", err)
	}

	return toVersionResponse(version, nil, 0, false), nil
}

// ── Public read ───────────────────────────────────────────────────────────────

// ListListings runs the public browse query.
func (s *Service) ListListings(ctx context.Context, input ListListingsInput) (*ListingListResponse, error) {
	input.Limit = clampLimit(input.Limit)
	if input.Sort == "" {
		input.Sort = "newest"
	}

	listings, total, err := s.repo.ListListings(ctx, ListListingsParams(input))
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListListings: %w", err)
	}

	items := make([]*ListingResponse, len(listings))
	for i, l := range listings {
		items[i] = toListingResponse(l)
	}
	return &ListingListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// GetListingBySlug fetches a listing by slug. Includes the latest version preview.
func (s *Service) GetListingBySlug(ctx context.Context, slug string) (*ListingResponse, *VersionResponse, error) {
	listing, err := s.repo.GetListingBySlug(ctx, slug)
	if err != nil {
		return nil, nil, fmt.Errorf("marketplace.service.GetListingBySlug: %w", err)
	}
	if listing.Status != "published" {
		return nil, nil, fmt.Errorf("marketplace.service.GetListingBySlug: %w", domain.ErrNotFound)
	}

	version, err := s.repo.GetLatestVersion(ctx, listing.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, nil, fmt.Errorf("marketplace.service.GetListingBySlug: version: %w", err)
	}

	var versionResp *VersionResponse
	if version != nil {
		fields, err := s.repo.GetFieldsByVersionID(ctx, version.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("marketplace.service.GetListingBySlug: fields: %w", err)
		}
		versionResp = toVersionResponse(version, fields, listing.PreviewFieldCount, listing.PricingType == "paid")
	}

	return toListingResponse(listing), versionResp, nil
}

// GetVersion returns a specific version with its fields rendered for preview.
func (s *Service) GetVersion(ctx context.Context, listingID, versionID uuid.UUID) (*VersionResponse, error) {
	listing, err := s.repo.GetListingByID(ctx, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.GetVersion: %w", err)
	}
	if listing.Status != "published" {
		return nil, fmt.Errorf("marketplace.service.GetVersion: %w", domain.ErrNotFound)
	}

	version, err := s.repo.GetVersionByID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.GetVersion: %w", err)
	}
	if version.ListingID != listingID {
		return nil, fmt.Errorf("marketplace.service.GetVersion: %w", domain.ErrNotFound)
	}

	fields, err := s.repo.GetFieldsByVersionID(ctx, version.ID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.GetVersion: fields: %w", err)
	}
	return toVersionResponse(version, fields, listing.PreviewFieldCount, listing.PricingType == "paid"), nil
}

// ── Acquisitions ──────────────────────────────────────────────────────────────

// Acquire creates a free acquisition for the caller's clinic.
// Paid listings are rejected — callers must use Purchase for those.
func (s *Service) Acquire(ctx context.Context, input AcquireInput) (*AcquisitionResponse, error) {
	listing, err := s.repo.GetListingByID(ctx, input.ListingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Acquire: %w", err)
	}
	if listing.Status != "published" {
		return nil, fmt.Errorf("marketplace.service.Acquire: listing not published: %w", domain.ErrNotFound)
	}
	if listing.PricingType != "free" {
		return nil, fmt.Errorf("marketplace.service.Acquire: paid listing — use Purchase: %w", domain.ErrConflict)
	}
	if err := s.checkCanAcquireFree(ctx, input.ClinicID); err != nil {
		return nil, fmt.Errorf("marketplace.service.Acquire: %w", err)
	}

	version, err := s.repo.GetLatestVersion(ctx, listing.ID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Acquire: no version available: %w", err)
	}

	now := domain.TimeNow()
	acq, err := s.repo.CreateAcquisition(ctx, CreateAcquisitionParams{
		ID:                   domain.NewID(),
		ListingID:            listing.ID,
		MarketplaceVersionID: version.ID,
		ClinicID:             input.ClinicID,
		AcquiredBy:           input.StaffID,
		AcquisitionType:      "free",
		Status:               "active",
		FulfilledAt:          &now,
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Acquire: %w", err)
	}

	if err := s.repo.IncrementDownloadCount(ctx, listing.ID); err != nil {
		_ = err // counter best-effort
	}

	return toAcquisitionResponse(acq, listing.Name), nil
}

// Import materialises an active acquisition into the tenant forms table.
// Supports the opt-in policy flow:
//   - IncludePolicies=true + AcceptedPolicyAttribution=true → import bundled policies
//     and link form ↔ policies
//   - IncludePolicies=false + RelinkExistingPolicyIDs populated → link form to
//     existing local policies at the given indices
//   - neither → form-only import (no links)
//
// Respects the forms invariant: create form with draft, then publish draft → v1.0.
func (s *Service) Import(ctx context.Context, input ImportInput) (*AcquisitionResponse, error) {
	if s.importer == nil {
		return nil, fmt.Errorf("marketplace.service.Import: form importer not configured: %w", domain.ErrConflict)
	}

	acq, err := s.repo.GetAcquisitionByID(ctx, input.AcquisitionID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: %w", err)
	}
	if acq.Status != "active" {
		return nil, fmt.Errorf("marketplace.service.Import: acquisition not active: %w", domain.ErrConflict)
	}
	if acq.ImportedFormID != nil {
		return nil, fmt.Errorf("marketplace.service.Import: already imported: %w", domain.ErrConflict)
	}

	version, err := s.repo.GetVersionByID(ctx, acq.MarketplaceVersionID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: version: %w", err)
	}

	listing, err := s.repo.GetListingByID(ctx, acq.ListingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: listing: %w", err)
	}

	var pkg Package
	if err := json.Unmarshal(version.PackagePayload, &pkg); err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: decode payload: %w", err)
	}

	// Policy attribution gate when bundled and consumer opts in.
	if input.IncludePolicies {
		if len(pkg.Policies) == 0 {
			return nil, fmt.Errorf("marketplace.service.Import: no bundled policies to import: %w", domain.ErrConflict)
		}
		if !input.AcceptedPolicyAttribution {
			return nil, fmt.Errorf("marketplace.service.Import: must accept policy attribution: %w", domain.ErrForbidden)
		}
		if s.policyImporter == nil {
			return nil, fmt.Errorf("marketplace.service.Import: policy importer not configured: %w", domain.ErrConflict)
		}
	}

	snapshotFields := make([]FormSnapshotField, len(pkg.Fields))
	for i, f := range pkg.Fields {
		cfg := f.Config
		if cfg == nil {
			cfg = json.RawMessage(`{}`)
		}
		snapshotFields[i] = FormSnapshotField{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         cfg,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  f.MinConfidence,
		}
	}

	importTags := append([]string{}, pkg.Listing.Tags...)
	if !containsString(importTags, "marketplace") {
		importTags = append(importTags, "marketplace")
	}

	changeSummary := fmt.Sprintf("Imported from marketplace listing %s v%d.%d", listing.Slug, version.VersionMajor, version.VersionMinor)

	formID, err := s.importer.ImportForm(ctx, FormImportInput{
		ClinicID:      input.ClinicID,
		StaffID:       input.StaffID,
		Name:          pkg.Listing.Name,
		Description:   pkg.Listing.Description,
		OverallPrompt: pkg.Listing.OverallPrompt,
		Tags:          importTags,
		ChangeSummary: changeSummary,
		Fields:        snapshotFields,
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: importer: %w", err)
	}

	// Policy import / relink flow.
	policyChoice := "skipped"
	var acceptedAt *time.Time
	if input.IncludePolicies {
		for i, pp := range pkg.Policies {
			// Relink overrides import: consumer chose to reuse an existing local policy.
			if existing, ok := input.RelinkExistingPolicyIDs[i]; ok {
				if err := s.importer.LinkFormToPolicy(ctx, formID, input.ClinicID, existing, input.StaffID); err != nil {
					return nil, fmt.Errorf("marketplace.service.Import: relink %d: %w", i, err)
				}
				continue
			}
			clauses := make([]PolicySnapshotClause, len(pp.Clauses))
			for j, c := range pp.Clauses {
				clauses[j] = PolicySnapshotClause(c)
			}
			content := pp.Content
			if content == nil {
				content = json.RawMessage(`[]`)
			}
			newPolicyID, err := s.policyImporter.ImportPolicy(ctx, PolicyImportInput{
				ClinicID:                   input.ClinicID,
				StaffID:                    input.StaffID,
				SourceMarketplaceVersionID: version.ID,
				Name:                       pp.Name,
				Description:                pp.Description,
				Content:                    content,
				Clauses:                    clauses,
				ChangeSummary:              changeSummary,
			})
			if err != nil {
				return nil, fmt.Errorf("marketplace.service.Import: policy import %d: %w", i, err)
			}
			if err := s.importer.LinkFormToPolicy(ctx, formID, input.ClinicID, newPolicyID, input.StaffID); err != nil {
				return nil, fmt.Errorf("marketplace.service.Import: link %d: %w", i, err)
			}
		}
		policyChoice = "imported"
		t := domain.TimeNow()
		acceptedAt = &t
	} else if len(input.RelinkExistingPolicyIDs) > 0 {
		for i, existing := range input.RelinkExistingPolicyIDs {
			if i < 0 || i >= len(pkg.Policies) {
				continue
			}
			if err := s.importer.LinkFormToPolicy(ctx, formID, input.ClinicID, existing, input.StaffID); err != nil {
				return nil, fmt.Errorf("marketplace.service.Import: relink %d: %w", i, err)
			}
		}
		policyChoice = "relinked"
	}

	if err := s.repo.SetAcquisitionImportedForm(ctx, acq.ID, input.ClinicID, formID); err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: mark imported: %w", err)
	}
	if err := s.repo.SetAcquisitionPolicyChoice(ctx, acq.ID, input.ClinicID, policyChoice, acceptedAt); err != nil {
		return nil, fmt.Errorf("marketplace.service.Import: set choice: %w", err)
	}

	acq.ImportedFormID = &formID
	acq.PolicyImportChoice = &policyChoice
	acq.PolicyAttributionAcceptedAt = acceptedAt
	return toAcquisitionResponse(acq, listing.Name), nil
}

// ListMyAcquisitions returns acquisitions for the caller's clinic.
func (s *Service) ListMyAcquisitions(ctx context.Context, clinicID uuid.UUID, limit, offset int) (*AcquisitionListResponse, error) {
	limit = clampLimit(limit)
	list, total, err := s.repo.ListAcquisitionsByClinic(ctx, clinicID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListMyAcquisitions: %w", err)
	}

	items := make([]*AcquisitionResponse, len(list))
	for i, a := range list {
		listing, err := s.repo.GetListingByID(ctx, a.ListingID)
		var name string
		if err == nil {
			name = listing.Name
		}
		items[i] = toAcquisitionResponse(a, name)
	}
	return &AcquisitionListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// ── Ownership + status helpers ────────────────────────────────────────────────

// callerOwnsPublisher reports whether the given clinic owns the publisher.
func (s *Service) callerOwnsPublisher(_ context.Context, callerClinicID uuid.UUID, publisher *PublisherRecord) bool {
	return publisher != nil && publisher.ClinicID == callerClinicID
}

// checkCanPublish ensures the clinic's status permits publishing / new versions.
// Trial and suspended clinics cannot publish.
func (s *Service) checkCanPublish(ctx context.Context, clinicID uuid.UUID) error {
	if s.clinicInfo == nil {
		return nil // test/local mode
	}
	info, err := s.clinicInfo.GetClinicInfo(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("get clinic info: %w", err)
	}
	switch info.Status {
	case "active", "grace_period":
		return nil
	case "trial":
		return fmt.Errorf("trial clinics cannot publish: %w", domain.ErrForbidden)
	case "suspended", "cancelled":
		return fmt.Errorf("clinic %s: %w", info.Status, domain.ErrForbidden)
	default:
		return fmt.Errorf("clinic status %q not permitted: %w", info.Status, domain.ErrForbidden)
	}
}

// checkCanAcquireFree ensures clinic status permits acquiring a free listing.
// Trial clinics can acquire free, suspended cannot.
func (s *Service) checkCanAcquireFree(ctx context.Context, clinicID uuid.UUID) error {
	if s.clinicInfo == nil {
		return nil
	}
	info, err := s.clinicInfo.GetClinicInfo(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("get clinic info: %w", err)
	}
	switch info.Status {
	case "trial", "active", "grace_period":
		return nil
	default:
		return fmt.Errorf("clinic status %q cannot acquire: %w", info.Status, domain.ErrForbidden)
	}
}

// checkCanAcquirePaid ensures clinic status permits acquiring a paid listing.
// Trial clinics cannot buy paid forms.
func (s *Service) checkCanAcquirePaid(ctx context.Context, clinicID uuid.UUID) error {
	if s.clinicInfo == nil {
		return nil
	}
	info, err := s.clinicInfo.GetClinicInfo(ctx, clinicID)
	if err != nil {
		return fmt.Errorf("get clinic info: %w", err)
	}
	switch info.Status {
	case "active", "grace_period":
		return nil
	case "trial":
		return fmt.Errorf("trial clinics cannot purchase paid listings: %w", domain.ErrForbidden)
	default:
		return fmt.Errorf("clinic status %q cannot purchase: %w", info.Status, domain.ErrForbidden)
	}
}

// getCallerVertical returns the caller's clinic vertical for browse scoping.
func (s *Service) getCallerVertical(ctx context.Context, clinicID uuid.UUID) (string, error) {
	if s.clinicInfo == nil {
		return "", nil
	}
	info, err := s.clinicInfo.GetClinicInfo(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("get vertical: %w", err)
	}
	return info.Vertical, nil
}

// platformFeePctForPublisher returns the platform fee % for a publisher.
// Authority bodies (authority_type IN ('salvia','authority')) pay 0%.
func (s *Service) platformFeePctForPublisher(p *PublisherRecord) int {
	if p != nil && p.AuthorityType != nil {
		switch *p.AuthorityType {
		case "salvia", "authority":
			return 0
		}
	}
	return s.platformFeeRegularPct
}

// nextMarketplaceVersion computes the next version number for a listing.
// Best-effort: on repo error we fall back to 1.0 — this is only called inside
// PublishVersion which has already fetched the listing, so repo errors here
// indicate transient DB issues that will surface in the CreateVersion call.
func nextMarketplaceVersion(ctx context.Context, r repo, listingID uuid.UUID, changeType string) (major, minor int) {
	latest, err := r.GetLatestVersion(ctx, listingID)
	if err != nil || latest == nil {
		return 1, 0
	}
	if changeType == "major" {
		return latest.VersionMajor + 1, 0
	}
	return latest.VersionMajor, latest.VersionMinor + 1
}

func validateListingInput(input CreateListingInput) error {
	if input.Name == "" {
		return fmt.Errorf("name required: %w", domain.ErrValidation)
	}
	if input.Slug == "" {
		return fmt.Errorf("slug required: %w", domain.ErrValidation)
	}
	if input.ShortDescription == "" {
		return fmt.Errorf("short_description required: %w", domain.ErrValidation)
	}
	switch input.Vertical {
	case "veterinary", "dental", "aged_care":
	default:
		return fmt.Errorf("invalid vertical: %w", domain.ErrValidation)
	}
	switch input.PricingType {
	case "free":
		if input.PriceCents != nil {
			return fmt.Errorf("free listings must not set price_cents: %w", domain.ErrValidation)
		}
	case "paid":
		if input.PriceCents == nil || *input.PriceCents <= 0 {
			return fmt.Errorf("paid listings require positive price_cents: %w", domain.ErrValidation)
		}
	default:
		return fmt.Errorf("invalid pricing_type: %w", domain.ErrValidation)
	}
	return nil
}

func normaliseTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func firstNonEmpty(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func defaultIntIfZero(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// checksumPackage returns a deterministic SHA-256 of the package payload.
// Meta.Checksum is zeroed before marshalling to avoid recursion.
func checksumPackage(pkg Package) (string, error) {
	pkg.Meta.Checksum = ""
	payload, err := json.Marshal(pkg)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func toPackageFields(fields []FormSnapshotField) []PackageField {
	out := make([]PackageField, len(fields))
	for i, f := range fields {
		cfg := f.Config
		if cfg == nil {
			cfg = json.RawMessage(`{}`)
		}
		out[i] = PackageField{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         cfg,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  f.MinConfidence,
		}
	}
	return out
}

func bayesianAverage(ratingSum, ratingCount int) float64 {
	const (
		priorCount = 5.0
		priorMean  = 3.0
	)
	return (priorCount*priorMean + float64(ratingSum)) / (priorCount + float64(ratingCount))
}

func toPublisherResponse(p *PublisherRecord) *PublisherResponse {
	return &PublisherResponse{
		ID:            p.ID.String(),
		DisplayName:   p.DisplayName,
		Bio:           p.Bio,
		WebsiteURL:    p.WebsiteURL,
		VerifiedBadge: p.VerifiedBadge,
		AuthorityType: p.AuthorityType,
	}
}

func toListingResponse(l *ListingRecord) *ListingResponse {
	r := &ListingResponse{
		ID:                l.ID.String(),
		Slug:              l.Slug,
		Name:              l.Name,
		ShortDescription:  l.ShortDescription,
		LongDescription:   l.LongDescription,
		Vertical:          l.Vertical,
		Tags:              l.Tags,
		BundleType:        l.BundleType,
		PricingType:       l.PricingType,
		PriceCents:        l.PriceCents,
		Currency:          l.Currency,
		Status:            l.Status,
		PreviewFieldCount: l.PreviewFieldCount,
		DownloadCount:     l.DownloadCount,
		RatingCount:       l.RatingCount,
		RatingAverage:     bayesianAverage(l.RatingSum, l.RatingCount),
		Publisher: PublisherResponse{
			ID:            l.PublisherAccountID.String(),
			DisplayName:   l.PublisherDisplayName,
			VerifiedBadge: l.PublisherVerifiedBadge,
			AuthorityType: l.PublisherAuthorityType,
		},
		CreatedAt: l.CreatedAt.Format(time.RFC3339),
	}
	if l.Tags == nil {
		r.Tags = []string{}
	}
	if l.PublishedAt != nil {
		s := l.PublishedAt.Format(time.RFC3339)
		r.PublishedAt = &s
	}
	return r
}

// toVersionResponse converts a version record to the API type.
// If previewLimit > 0 and isPaid, fields beyond previewLimit are locked —
// their ai_prompt and min_confidence are hidden and Locked=true is set.
func toVersionResponse(v *VersionRecord, fields []*VersionFieldRecord, previewLimit int, isPaid bool) *VersionResponse {
	r := &VersionResponse{
		ID:            v.ID.String(),
		ListingID:     v.ListingID.String(),
		VersionMajor:  v.VersionMajor,
		VersionMinor:  v.VersionMinor,
		ChangeType:    v.ChangeType,
		ChangeSummary: v.ChangeSummary,
		FieldCount:    v.FieldCount,
		PublishedAt:   v.PublishedAt.Format(time.RFC3339),
	}
	if fields == nil {
		return r
	}
	r.Fields = make([]*VersionFieldResponse, len(fields))
	for i, f := range fields {
		locked := isPaid && previewLimit > 0 && (i+1) > previewLimit
		fr := &VersionFieldResponse{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         f.Config,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			Locked:         locked,
		}
		if !locked {
			fr.AIPrompt = f.AIPrompt
			fr.MinConfidence = f.MinConfidence
		}
		r.Fields[i] = fr
	}
	return r
}

func toAcquisitionResponse(a *AcquisitionRecord, listingName string) *AcquisitionResponse {
	r := &AcquisitionResponse{
		ID:                   a.ID.String(),
		ListingID:            a.ListingID.String(),
		ListingName:          listingName,
		MarketplaceVersionID: a.MarketplaceVersionID.String(),
		AcquisitionType:      a.AcquisitionType,
		Status:               a.Status,
		CreatedAt:            a.CreatedAt.Format(time.RFC3339),
	}
	if a.ImportedFormID != nil {
		s := a.ImportedFormID.String()
		r.ImportedFormID = &s
	}
	if a.FulfilledAt != nil {
		s := a.FulfilledAt.Format(time.RFC3339)
		r.FulfilledAt = &s
	}
	return r
}
