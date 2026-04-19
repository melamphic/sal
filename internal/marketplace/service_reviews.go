package marketplace

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Reviews ──────────────────────────────────────────────────────────────────

// CreateReview records a rating for a listing. The caller must hold an active
// acquisition for that listing. Review eligibility is immediate — no
// minimum-usage gate.
func (s *Service) CreateReview(ctx context.Context, input CreateReviewInput) (*ReviewResponse, error) {
	if input.Rating < 1 || input.Rating > 5 {
		return nil, fmt.Errorf("marketplace.service.CreateReview: rating out of range: %w", domain.ErrValidation)
	}

	acq, err := s.repo.GetAcquisitionByID(ctx, input.AcquisitionID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.CreateReview: %w", err)
	}
	if acq.Status != "active" {
		return nil, fmt.Errorf("marketplace.service.CreateReview: acquisition not active: %w", domain.ErrConflict)
	}

	rec, err := s.repo.CreateReview(ctx, CreateReviewParams{
		ID:            domain.NewID(),
		ListingID:     acq.ListingID,
		AcquisitionID: acq.ID,
		ClinicID:      input.ClinicID,
		StaffID:       input.StaffID,
		Rating:        input.Rating,
		Body:          input.Body,
	})
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.CreateReview: %w", err)
	}

	return toReviewResponse(rec), nil
}

// ListReviews returns published reviews for a listing, newest first.
func (s *Service) ListReviews(ctx context.Context, listingID uuid.UUID, limit, offset int) (*ReviewListResponse, error) {
	limit = clampLimit(limit)
	rows, total, err := s.repo.ListReviewsByListing(ctx, listingID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListReviews: %w", err)
	}
	items := make([]*ReviewResponse, len(rows))
	for i, r := range rows {
		items[i] = toReviewResponse(r)
	}
	return &ReviewListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func toReviewResponse(r *ReviewRecord) *ReviewResponse {
	return &ReviewResponse{
		ID:        r.ID.String(),
		ListingID: r.ListingID.String(),
		Rating:    r.Rating,
		Body:      r.Body,
		CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
}
