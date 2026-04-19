package marketplace

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Badge grant / revoke ─────────────────────────────────────────────────────

// GrantBadge sets verified_badge + authority_type on a target publisher.
//
// Authorisation rules (§4.2 in docs/marketplace.md):
//   - Salvia (authority_type='salvia')   → can grant anything, any vertical
//   - Authority (authority_type='authority') → can grant verified_badge only,
//     and only to publishers in the same vertical as the grantor. Cannot grant
//     authority_type (only Salvia can).
func (s *Service) GrantBadge(ctx context.Context, input GrantBadgeInput) (*PublisherResponse, error) {
	granter, err := s.repo.GetPublisherByClinicID(ctx, input.GranterClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.GrantBadge: granter: %w", err)
	}
	if granter.AuthorityType == nil {
		return nil, fmt.Errorf("marketplace.service.GrantBadge: granter lacks authority: %w", domain.ErrForbidden)
	}

	target, err := s.repo.GetPublisherByID(ctx, input.TargetPublisherID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.GrantBadge: target: %w", err)
	}

	granterIsSalvia := *granter.AuthorityType == "salvia"
	granterIsAuthority := *granter.AuthorityType == "authority"

	// Only Salvia can grant/revoke authority_type. Authorities cannot.
	if input.AuthorityType != nil && !granterIsSalvia {
		return nil, fmt.Errorf("marketplace.service.GrantBadge: only Salvia can grant authority: %w", domain.ErrForbidden)
	}

	// If authority grantor, target must be in same vertical.
	if granterIsAuthority {
		granterVertical, err := s.getPublisherVertical(ctx, granter)
		if err != nil {
			return nil, fmt.Errorf("marketplace.service.GrantBadge: granter vertical: %w", err)
		}
		targetVertical, err := s.getPublisherVertical(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("marketplace.service.GrantBadge: target vertical: %w", err)
		}
		if granterVertical != targetVertical {
			return nil, fmt.Errorf("marketplace.service.GrantBadge: cross-vertical grant forbidden: %w", domain.ErrForbidden)
		}
	}

	var grantedAt *time.Time
	if input.AuthorityType != nil {
		t := domain.TimeNow()
		grantedAt = &t
	}

	if err := s.repo.SetPublisherBadge(ctx, target.ID, granter.ID, input.VerifiedBadge, input.AuthorityType, grantedAt); err != nil {
		return nil, fmt.Errorf("marketplace.service.GrantBadge: %w", err)
	}
	updated, err := s.repo.GetPublisherByID(ctx, target.ID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.GrantBadge: reload: %w", err)
	}
	return toPublisherResponse(updated), nil
}

// RevokeBadge clears verified_badge + authority_type on a target publisher.
// Revoke rules (§4.2): grantor can revoke their own grants (authority_granted_by
// = granter.id) OR Salvia can revoke anything.
func (s *Service) RevokeBadge(ctx context.Context, granterClinicID, targetPublisherID uuid.UUID) (*PublisherResponse, error) {
	granter, err := s.repo.GetPublisherByClinicID(ctx, granterClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.RevokeBadge: granter: %w", err)
	}
	if granter.AuthorityType == nil {
		return nil, fmt.Errorf("marketplace.service.RevokeBadge: granter lacks authority: %w", domain.ErrForbidden)
	}
	target, err := s.repo.GetPublisherByID(ctx, targetPublisherID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.RevokeBadge: target: %w", err)
	}

	granterIsSalvia := *granter.AuthorityType == "salvia"
	if !granterIsSalvia {
		// Must be the original grantor.
		if target.AuthorityGrantedBy == nil || *target.AuthorityGrantedBy != granter.ID {
			return nil, fmt.Errorf("marketplace.service.RevokeBadge: not original grantor: %w", domain.ErrForbidden)
		}
	}

	if err := s.repo.SetPublisherBadge(ctx, target.ID, granter.ID, false, nil, nil); err != nil {
		return nil, fmt.Errorf("marketplace.service.RevokeBadge: %w", err)
	}
	updated, err := s.repo.GetPublisherByID(ctx, target.ID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.RevokeBadge: reload: %w", err)
	}
	return toPublisherResponse(updated), nil
}

// getPublisherVertical looks up the clinic vertical for a publisher.
func (s *Service) getPublisherVertical(ctx context.Context, p *PublisherRecord) (string, error) {
	if s.clinicInfo == nil {
		return "", nil
	}
	info, err := s.clinicInfo.GetClinicInfo(ctx, p.ClinicID)
	if err != nil {
		return "", fmt.Errorf("get clinic info: %w", err)
	}
	return info.Vertical, nil
}

// SuspendListing moves a listing to status='suspended'. Restricted to Salvia.
func (s *Service) SuspendListing(ctx context.Context, callerClinicID, listingID uuid.UUID) (*ListingResponse, error) {
	caller, err := s.repo.GetPublisherByClinicID(ctx, callerClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.SuspendListing: caller: %w", err)
	}
	if caller.AuthorityType == nil || *caller.AuthorityType != "salvia" {
		return nil, fmt.Errorf("marketplace.service.SuspendListing: salvia only: %w", domain.ErrForbidden)
	}
	rec, err := s.repo.UpdateListingStatus(ctx, listingID, "suspended")
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.SuspendListing: %w", err)
	}
	return toListingResponse(rec), nil
}
