package marketplace

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Upgrade notifications ────────────────────────────────────────────────────

// ListMyUpgradeNotifications returns unseen marketplace upgrade notifications
// for the caller's clinic.
func (s *Service) ListMyUpgradeNotifications(ctx context.Context, clinicID uuid.UUID, limit int) ([]*UpgradeNotificationResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.repo.ListUnseenNotifications(ctx, clinicID, limit)
	if err != nil {
		return nil, fmt.Errorf("marketplace.service.ListMyUpgradeNotifications: %w", err)
	}
	out := make([]*UpgradeNotificationResponse, len(rows))
	for i, n := range rows {
		resp := &UpgradeNotificationResponse{
			ID:               n.ID.String(),
			AcquisitionID:    n.AcquisitionID.String(),
			NewVersionID:     n.NewVersionID.String(),
			NotificationType: n.NotificationType,
			CreatedAt:        n.CreatedAt.Format(time.RFC3339),
		}
		if n.SeenAt != nil {
			s := n.SeenAt.Format(time.RFC3339)
			resp.SeenAt = &s
		}
		out[i] = resp
	}
	return out, nil
}

// MarkNotificationSeen flags a notification as seen (dismisses the banner).
func (s *Service) MarkNotificationSeen(ctx context.Context, notificationID, clinicID uuid.UUID) error {
	if err := s.repo.MarkNotificationSeen(ctx, notificationID, clinicID, domain.TimeNow()); err != nil {
		return fmt.Errorf("marketplace.service.MarkNotificationSeen: %w", err)
	}
	return nil
}
