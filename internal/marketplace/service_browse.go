package marketplace

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Auth-gated vertical-scoped browse ────────────────────────────────────────

// BrowseListings is the authenticated browse entrypoint. The caller's
// clinic.vertical is always injected as a hard filter — clinics see only
// listings in their own vertical. Any ?vertical query parameter is ignored
// except for the reserved Salvia-platform clinic which may browse cross-vertical.
func (s *Service) BrowseListings(ctx context.Context, callerClinicID uuid.UUID, input ListListingsInput) (*ListingListResponse, error) {
	callerVertical, err := s.getCallerVertical(ctx, callerClinicID)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.BrowseListings: %w", err)
	}

	// Platform clinic can browse cross-vertical if it explicitly passes a vertical;
	// otherwise all clinics are scoped to their own vertical.
	crossVertical := false
	if s.clinicInfo != nil {
		info, _ := s.clinicInfo.GetClinicInfo(ctx, callerClinicID)
		if info != nil {
			// The Salvia platform clinic has no special vertical flag in schema, but
			// holders of authority_type='salvia' on their publisher account can
			// browse cross-vertical. Best-effort check; fall through on error.
			if publisher, err := s.repo.GetPublisherByClinicID(ctx, callerClinicID); err == nil {
				if publisher.AuthorityType != nil && *publisher.AuthorityType == "salvia" {
					crossVertical = true
				}
			}
		}
	}

	if !crossVertical && callerVertical != "" {
		v := callerVertical
		input.Vertical = &v
	}

	return s.ListListings(ctx, input)
}
