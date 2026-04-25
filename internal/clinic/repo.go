package clinic

import (
	"context"

	"github.com/google/uuid"
)

// repo is the interface the clinic Service depends on for data access.
type repo interface {
	Create(ctx context.Context, p CreateParams) (*Clinic, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Clinic, error)
	GetByEmailHash(ctx context.Context, emailHash string) (*Clinic, error)
	GetByStripeCustomerID(ctx context.Context, stripeCustomerID string) (*Clinic, error)
	Update(ctx context.Context, id uuid.UUID, p UpdateParams) (*Clinic, error)
	ApplySubscriptionState(ctx context.Context, id uuid.UUID, p ApplySubscriptionStateParams) (*Clinic, error)
	SubmitCompliance(ctx context.Context, id uuid.UUID, p ComplianceParams) (*Clinic, error)
}
