package marketplace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Self-serve publisher registration ────────────────────────────────────────

// RegisterPublisher creates a publisher_accounts row for the caller's clinic.
// Idempotent: returns the existing row if present. Rejects trial/suspended
// clinics. The row is created with status='active' so no Salvia approval
// queue is needed.
func (s *Service) RegisterPublisher(ctx context.Context, input RegisterPublisherInput) (*PublisherResponse, error) {
	if err := s.checkCanPublish(ctx, input.ClinicID); err != nil {
		return nil, fmt.Errorf("marketplace.service.RegisterPublisher: %w", err)
	}

	existing, err := s.repo.GetPublisherByClinicID(ctx, input.ClinicID)
	if err == nil {
		return toPublisherResponse(existing), nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("marketplace.service.RegisterPublisher: %w", err)
	}

	rec, err := s.repo.CreatePublisher(ctx, CreatePublisherParams{
		ID:          domain.NewID(),
		ClinicID:    input.ClinicID,
		DisplayName: input.DisplayName,
		Bio:         input.Bio,
		WebsiteURL:  input.WebsiteURL,
		Status:      "active",
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.RegisterPublisher: %w", err)
	}
	return toPublisherResponse(rec), nil
}

// ListMyPublisherListings returns listings owned by the caller's publisher.
// When the clinic has no publisher_accounts row yet (brand-new state), the
// response is an empty page rather than 404 — from the caller's perspective
// "no publisher" and "publisher with zero listings" should look identical
// to the UI's empty-state handling.
func (s *Service) ListMyPublisherListings(ctx context.Context, clinicID uuid.UUID, limit, offset int) (*ListingListResponse, error) {
	limit = clampLimit(limit)
	publisher, err := s.repo.GetPublisherByClinicID(ctx, clinicID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &ListingListResponse{Items: []*ListingResponse{}, Total: 0, Limit: limit, Offset: offset}, nil
		}
		return nil, fmt.Errorf("marketplace.service.ListMyPublisherListings: %w", err)
	}
	rows, total, err := s.repo.ListPublisherListings(ctx, publisher.ID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListMyPublisherListings: %w", err)
	}
	items := make([]*ListingResponse, len(rows))
	for i, r := range rows {
		items[i] = toListingResponse(r)
	}
	return &ListingListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// UpdateListingInput holds the editable subset for PATCH. Each pointer is
// optional — only non-nil fields are written. Service layer enforces the
// per-status edit policy:
//   - draft     → all fields editable
//   - published → only short_description, long_description, tags,
//                 preview_field_count are editable; price/name/slug/vertical
//                 changes require archival + re-listing (or moderation).
//   - archived  → no edits allowed
//   - suspended → no edits allowed
type UpdateListingInput struct {
	CallerClinicID    uuid.UUID
	ListingID         uuid.UUID
	Name              *string
	ShortDescription  *string
	LongDescription   *string
	Tags              *[]string
	BundleType        *string
	PricingType       *string
	PriceCents        *int
	Currency          *string
	PreviewFieldCount *int
}

// UpdateListingByOwner applies a partial metadata update with ownership +
// status-policy enforcement. Returns the updated listing.
func (s *Service) UpdateListingByOwner(ctx context.Context, input UpdateListingInput) (*ListingResponse, error) {
	listing, err := s.repo.GetListingByID(ctx, input.ListingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: %w", err)
	}
	publisher, err := s.repo.GetPublisherByID(ctx, listing.PublisherAccountID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: publisher: %w", err)
	}
	if !s.callerOwnsPublisher(ctx, input.CallerClinicID, publisher) {
		return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: not owner: %w", domain.ErrForbidden)
	}

	switch listing.Status {
	case "archived", "suspended":
		return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: not editable in %s: %w", listing.Status, domain.ErrConflict)
	case "draft":
		// Anything goes.
	default:
		// Published / under_review: restrict to safe fields. Reject any
		// caller-supplied edits to identity / pricing fields.
		if input.Name != nil || input.BundleType != nil ||
			input.PricingType != nil || input.PriceCents != nil ||
			input.Currency != nil {
			return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: name/pricing change not allowed post-publish: %w", domain.ErrForbidden)
		}
	}

	// Validate paid → free / free → paid invariant when both flip together.
	// Same rule as CreateListing: free disallows price_cents.
	if input.PricingType != nil && input.PriceCents != nil {
		if *input.PricingType == "free" && *input.PriceCents != 0 {
			return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: free listings cannot have price_cents: %w", domain.ErrValidation)
		}
		if *input.PricingType == "paid" && *input.PriceCents <= 0 {
			return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: paid listings need price_cents > 0: %w", domain.ErrValidation)
		}
	}

	rec, err := s.repo.UpdateListingMetadata(ctx, UpdateListingMetadataParams{
		ID:                input.ListingID,
		Name:              input.Name,
		ShortDescription:  input.ShortDescription,
		LongDescription:   input.LongDescription,
		Tags:              input.Tags,
		BundleType:        input.BundleType,
		PricingType:       input.PricingType,
		PriceCents:        input.PriceCents,
		Currency:          input.Currency,
		PreviewFieldCount: input.PreviewFieldCount,
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.UpdateListingByOwner: %w", err)
	}
	return toListingResponse(rec), nil
}

// ArchiveListingByOwner moves a listing to status='archived'. Existing
// acquisitions stay valid; new browse / acquire / purchase calls reject.
// Suspended listings cannot be archived (Salvia's hammer wins).
func (s *Service) ArchiveListingByOwner(ctx context.Context, callerClinicID, listingID uuid.UUID) (*ListingResponse, error) {
	listing, err := s.repo.GetListingByID(ctx, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ArchiveListingByOwner: %w", err)
	}
	publisher, err := s.repo.GetPublisherByID(ctx, listing.PublisherAccountID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ArchiveListingByOwner: publisher: %w", err)
	}
	if !s.callerOwnsPublisher(ctx, callerClinicID, publisher) {
		return nil, fmt.Errorf("marketplace.service.ArchiveListingByOwner: not owner: %w", domain.ErrForbidden)
	}
	if listing.Status == "suspended" {
		return nil, fmt.Errorf("marketplace.service.ArchiveListingByOwner: cannot archive suspended listing: %w", domain.ErrConflict)
	}
	if listing.Status == "archived" {
		return toListingResponse(listing), nil // idempotent
	}
	rec, err := s.repo.ArchiveListing(ctx, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ArchiveListingByOwner: %w", err)
	}
	return toListingResponse(rec), nil
}

// DeleteListingDraftByOwner permanently removes a draft listing along with
// any unreleased version rows. Once a listing has been published it cannot
// be deleted — archive instead so historical acquisitions still resolve.
func (s *Service) DeleteListingDraftByOwner(ctx context.Context, callerClinicID, listingID uuid.UUID) error {
	listing, err := s.repo.GetListingByID(ctx, listingID)
	if err != nil {
		return fmt.Errorf("marketplace.service.DeleteListingDraftByOwner: %w", err)
	}
	publisher, err := s.repo.GetPublisherByID(ctx, listing.PublisherAccountID)
	if err != nil {
		return fmt.Errorf("marketplace.service.DeleteListingDraftByOwner: publisher: %w", err)
	}
	if !s.callerOwnsPublisher(ctx, callerClinicID, publisher) {
		return fmt.Errorf("marketplace.service.DeleteListingDraftByOwner: not owner: %w", domain.ErrForbidden)
	}
	if listing.Status != "draft" {
		return fmt.Errorf("marketplace.service.DeleteListingDraftByOwner: only drafts can be deleted: %w", domain.ErrConflict)
	}
	if err := s.repo.DeleteListingDraft(ctx, listingID); err != nil {
		return fmt.Errorf("marketplace.service.DeleteListingDraftByOwner: %w", err)
	}
	return nil
}

// ── Publisher earnings ───────────────────────────────────────────────────────

// EarningsResponse is the API-safe row representation. Cents are kept as
// integers; clients format the currency. Buyer clinic is exposed by ID
// only — buyer identity beyond that lives behind a separate moderation
// surface.
//
//nolint:revive
type EarningsResponse struct {
	AcquisitionID    string `json:"acquisition_id"`
	ListingID        string `json:"listing_id"`
	ListingName      string `json:"listing_name"`
	BuyerClinicID    string `json:"buyer_clinic_id"`
	AmountPaidCents  int    `json:"amount_paid_cents"`
	PlatformFeeCents int    `json:"platform_fee_cents"`
	NetCents         int    `json:"net_cents"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
	FulfilledAt      string `json:"fulfilled_at"`
}

// EarningsListResponse is paginated.
//
//nolint:revive
type EarningsListResponse struct {
	Items  []*EarningsResponse `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

// EarningsMonthlyResponse is one bucket of the summary chart.
//
//nolint:revive
type EarningsMonthlyResponse struct {
	Month       string `json:"month"`
	Currency    string `json:"currency"`
	GrossCents  int    `json:"gross_cents"`
	FeeCents    int    `json:"fee_cents"`
	NetCents    int    `json:"net_cents"`
	OrderCount  int    `json:"order_count"`
	RefundCount int    `json:"refund_count"`
}

// EarningsSummaryResponse wraps the monthly buckets.
//
//nolint:revive
type EarningsSummaryResponse struct {
	Items []*EarningsMonthlyResponse `json:"items"`
}

// ListMyEarnings returns paid acquisitions for the caller's publisher with
// platform-fee splits applied. Free acquisitions are excluded. Returns an
// empty page when the clinic has no publisher_accounts row yet — same
// shape the UI expects for "no earnings yet" rather than a 404.
func (s *Service) ListMyEarnings(ctx context.Context, callerClinicID uuid.UUID, limit, offset int) (*EarningsListResponse, error) {
	limit = clampLimit(limit)
	publisher, err := s.repo.GetPublisherByClinicID(ctx, callerClinicID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &EarningsListResponse{Items: []*EarningsResponse{}, Total: 0, Limit: limit, Offset: offset}, nil
		}
		return nil, fmt.Errorf("marketplace.service.ListMyEarnings: %w", err)
	}
	rows, total, err := s.repo.ListPublisherEarnings(ctx, publisher.ID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListMyEarnings: %w", err)
	}
	items := make([]*EarningsResponse, len(rows))
	for i, r := range rows {
		items[i] = &EarningsResponse{
			AcquisitionID:    r.AcquisitionID.String(),
			ListingID:        r.ListingID.String(),
			ListingName:      r.ListingName,
			BuyerClinicID:    r.BuyerClinicID.String(),
			AmountPaidCents:  r.AmountPaidCents,
			PlatformFeeCents: r.PlatformFeeCents,
			NetCents:         r.NetCents,
			Currency:         r.Currency,
			Status:           r.Status,
			FulfilledAt:      r.FulfilledAt.Format(time.RFC3339),
		}
	}
	return &EarningsListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// MyEarningsSummary returns up to 24 months of bucketed gross/net earnings
// for the caller's publisher. Empty list when the clinic has no
// publisher_accounts row yet.
func (s *Service) MyEarningsSummary(ctx context.Context, callerClinicID uuid.UUID, monthsBack int) (*EarningsSummaryResponse, error) {
	publisher, err := s.repo.GetPublisherByClinicID(ctx, callerClinicID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &EarningsSummaryResponse{Items: []*EarningsMonthlyResponse{}}, nil
		}
		return nil, fmt.Errorf("marketplace.service.MyEarningsSummary: %w", err)
	}
	rows, err := s.repo.PublisherEarningsSummary(ctx, publisher.ID, monthsBack)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.MyEarningsSummary: %w", err)
	}
	items := make([]*EarningsMonthlyResponse, len(rows))
	for i, r := range rows {
		items[i] = &EarningsMonthlyResponse{
			Month:       r.Month,
			Currency:    r.Currency,
			GrossCents:  r.GrossCents,
			FeeCents:    r.FeeCents,
			NetCents:    r.NetCents,
			OrderCount:  r.OrderCount,
			RefundCount: r.RefundCount,
		}
	}
	return &EarningsSummaryResponse{Items: items}, nil
}

// PublishListingByOwner transitions a listing from draft → published with
// ownership enforced (caller's clinic must own the publisher).
func (s *Service) PublishListingByOwner(ctx context.Context, callerClinicID, listingID uuid.UUID) (*ListingResponse, error) {
	listing, err := s.repo.GetListingByID(ctx, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: %w", err)
	}
	publisher, err := s.repo.GetPublisherByID(ctx, listing.PublisherAccountID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: publisher: %w", err)
	}
	if !s.callerOwnsPublisher(ctx, callerClinicID, publisher) {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: not owner: %w", domain.ErrForbidden)
	}
	if err := s.checkCanPublish(ctx, callerClinicID); err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: %w", err)
	}

	versions, err := s.repo.ListVersionsByListing(ctx, listingID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: versions: %w", err)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: no versions: %w", domain.ErrConflict)
	}

	rec, err := s.repo.PublishListing(ctx, listingID, domain.TimeNow())
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.PublishListingByOwner: %w", err)
	}
	return toListingResponse(rec), nil
}
