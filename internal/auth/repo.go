package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// repo is the interface the auth Service depends on for data access.
// The concrete *Repository satisfies this interface.
// In tests, a hand-rolled fake implements it — no real DB required.
//
// Rule: interfaces live in the package that uses them, not the package that
// provides them. (Go convention: accept interfaces, return structs.)
type repo interface {
	FindStaffByEmailHash(ctx context.Context, emailHash string) (*staffRow, error)
	CreateAuthToken(ctx context.Context, staffID uuid.UUID, tokenHash, tokenType, fromIP string, expiresAt time.Time) error
	GetAndConsumeAuthToken(ctx context.Context, tokenHash string) (*tokenRow, error)
	GetStaffByID(ctx context.Context, staffID uuid.UUID) (*staffRow, error)
	CreateInviteToken(ctx context.Context, p CreateInviteParams) error
	GetInviteByTokenHash(ctx context.Context, tokenHash string) (*inviteRow, error)
	GetInviteByID(ctx context.Context, id, clinicID uuid.UUID) (*inviteRow, error)
	ListInvitesByClinic(ctx context.Context, clinicID uuid.UUID) ([]*inviteRow, error)
	RevokeInviteByID(ctx context.Context, id, clinicID uuid.UUID) error
	MarkInviteAccepted(ctx context.Context, tokenHash string) error
	DeleteRefreshTokensForStaff(ctx context.Context, staffID uuid.UUID) error
	UpdateLastActive(ctx context.Context, staffID uuid.UUID) error
	ListLoginsByStaff(ctx context.Context, staffID uuid.UUID, limit int) ([]*LoginActivityRow, error)

	// ConsumeMelHandoffToken records the jti of a /mel handoff JWT so it
	// cannot be replayed. Returns domain.ErrTokenUsed if jti is already
	// present (single-use enforcement).
	ConsumeMelHandoffToken(ctx context.Context, jti string, expiresAt time.Time) error
}
