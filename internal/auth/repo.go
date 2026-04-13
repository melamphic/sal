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
	GetInviteByTokenHash(ctx context.Context, tokenHash string) (*inviteRow, error)
	MarkInviteAccepted(ctx context.Context, tokenHash string) error
	DeleteRefreshTokensForStaff(ctx context.Context, staffID uuid.UUID) error
	UpdateLastActive(ctx context.Context, staffID uuid.UUID) error
}
