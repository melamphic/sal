// Package middleware provides HTTP middleware for the Chi router.
package middleware

import (
	"context"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

type contextKey string

const (
	contextKeyClinicID contextKey = "clinic_id"
	contextKeyStaffID  contextKey = "staff_id"
	contextKeyRole     contextKey = "staff_role"
	contextKeyPerms    contextKey = "permissions"
)

// WithClinicID stores the authenticated clinic's ID in the request context.
func WithClinicID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, contextKeyClinicID, id)
}

// WithStaffID stores the authenticated staff member's ID in the request context.
func WithStaffID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, contextKeyStaffID, id)
}

// WithRole stores the authenticated staff member's role in the request context.
func WithRole(ctx context.Context, role domain.StaffRole) context.Context {
	return context.WithValue(ctx, contextKeyRole, role)
}

// WithPermissions stores the authenticated staff member's permissions in the context.
func WithPermissions(ctx context.Context, perms domain.Permissions) context.Context {
	return context.WithValue(ctx, contextKeyPerms, perms)
}

// ClinicIDFromContext returns the authenticated clinic ID from context.
// Panics if called on an unauthenticated request — always use behind Authenticate middleware.
func ClinicIDFromContext(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(contextKeyClinicID).(uuid.UUID)
	return id
}

// StaffIDFromContext returns the authenticated staff ID from context.
func StaffIDFromContext(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(contextKeyStaffID).(uuid.UUID)
	return id
}

// RoleFromContext returns the authenticated staff role from context.
func RoleFromContext(ctx context.Context) domain.StaffRole {
	role, _ := ctx.Value(contextKeyRole).(domain.StaffRole)
	return role
}

// PermissionsFromContext returns the authenticated staff permissions from context.
func PermissionsFromContext(ctx context.Context) domain.Permissions {
	perms, _ := ctx.Value(contextKeyPerms).(domain.Permissions)
	return perms
}
