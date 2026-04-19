package marketplace

import (
	"context"
	"errors"
	"fmt"

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
func (s *Service) ListMyPublisherListings(ctx context.Context, clinicID uuid.UUID, limit, offset int) (*ListingListResponse, error) {
	publisher, err := s.repo.GetPublisherByClinicID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListMyPublisherListings: %w", err)
	}
	limit = clampLimit(limit)
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
