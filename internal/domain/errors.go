// Package domain contains types and errors shared across all internal modules.
// No business logic lives here — only the contracts that modules use to
// communicate with each other.
package domain

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Sentinel errors used throughout the application.
// Handlers map these to HTTP status codes via errors.Is.
var (
	// ErrNotFound is returned when a requested resource does not exist.
	ErrNotFound = errors.New("not found")

	// ErrConflict is returned when a create/update would violate a uniqueness constraint.
	ErrConflict = errors.New("conflict")

	// ErrUnauthorized is returned when a request has no valid credentials.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden is returned when authenticated credentials lack the required permission.
	ErrForbidden = errors.New("forbidden")

	// ErrTokenExpired is returned when a magic link or refresh token has expired.
	ErrTokenExpired = errors.New("token expired")

	// ErrTokenUsed is returned when a single-use token has already been consumed.
	ErrTokenUsed = errors.New("token already used")

	// ErrTokenInvalid is returned when a token cannot be parsed or verified.
	ErrTokenInvalid = errors.New("token invalid")

	// ErrClinicSuspended is returned when an operation is attempted on a suspended clinic.
	ErrClinicSuspended = errors.New("clinic suspended")

	// ErrNoteCap is returned when a clinic has reached its monthly note cap.
	ErrNoteCap = errors.New("note cap reached")

	// ErrValidation is returned for input that fails validation rules.
	ErrValidation = errors.New("validation error")
)

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505). Use this in repository Create methods to map
// DB errors to ErrConflict before returning to callers.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
