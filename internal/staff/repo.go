package staff

import (
	"context"

	"github.com/google/uuid"
)

// repo is the interface the staff Service depends on for data access.
type repo interface {
	Create(ctx context.Context, p CreateParams) (*StaffRecord, error)
	GetByID(ctx context.Context, staffID, clinicID uuid.UUID) (*StaffRecord, error)
	GetByEmailHash(ctx context.Context, emailHash string, clinicID uuid.UUID) (*StaffRecord, error)
	ExistsByEmailHash(ctx context.Context, emailHash string, clinicID uuid.UUID) (bool, error)
	List(ctx context.Context, clinicID uuid.UUID, p ListParams) ([]*StaffRecord, int, error)
	UpdatePermissions(ctx context.Context, staffID, clinicID uuid.UUID, p UpdatePermsParams) (*StaffRecord, error)
	UpdateRegulatoryIdentity(ctx context.Context, staffID, clinicID uuid.UUID, authority, regNo *string) (*StaffRecord, error)
	Deactivate(ctx context.Context, staffID, clinicID uuid.UUID) (*StaffRecord, error)
	CountStandardActive(ctx context.Context, clinicID uuid.UUID) (int, error)
}
