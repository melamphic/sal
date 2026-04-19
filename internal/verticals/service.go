package verticals

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ClinicVerticalProvider resolves a clinic's vertical from its ID.
// Implemented by an adapter in app.go that bridges to clinic.Service.
type ClinicVerticalProvider interface {
	GetClinicVertical(ctx context.Context, clinicID uuid.UUID) (domain.Vertical, error)
}

// Service exposes the per-vertical form schema registry to the HTTP layer.
// No persistent state — the registry lives in the domain package and is
// looked up at request time.
type Service struct {
	clinics ClinicVerticalProvider
}

// NewService creates a new verticals Service.
func NewService(cp ClinicVerticalProvider) *Service {
	return &Service{clinics: cp}
}

// SchemaForClinic returns the VerticalSchema for the clinic's configured vertical.
// Returns ErrNotFound when the clinic's vertical has no registered schema.
func (s *Service) SchemaForClinic(ctx context.Context, clinicID uuid.UUID) (*domain.VerticalSchema, error) {
	v, err := s.clinics.GetClinicVertical(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("verticals.service.SchemaForClinic: %w", err)
	}
	schema, ok := domain.SchemaFor(v)
	if !ok {
		return nil, fmt.Errorf("verticals.service.SchemaForClinic: %w", domain.ErrNotFound)
	}
	return &schema, nil
}
