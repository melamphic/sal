package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// Claims is the JWT payload structure for Salvia access tokens.
type Claims struct {
	jwt.RegisteredClaims
	ClinicID uuid.UUID          `json:"clinic_id"`
	StaffID  uuid.UUID          `json:"staff_id"`
	Role     domain.StaffRole   `json:"role"`
	Perms    domain.Permissions `json:"perms"`
}

// Authenticate is a Chi middleware that validates a Bearer JWT on every request.
// It rejects unauthenticated requests with 401 and sets clinic/staff context values
// for downstream handlers.
//
// Usage:
//
//	r.Group(func(r chi.Router) {
//	    r.Use(middleware.Authenticate([]byte(cfg.JWTSecret)))
//	    // protected routes...
//	})
func Authenticate(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := ""

			// Check Authorization header first (standard).
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
			}

			// Fall back to ?token= query parameter (useful for SSE EventSource).
			if tokenStr == "" {
				tokenStr = r.URL.Query().Get("token")
			}

			if tokenStr == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid authorization header or token query parameter")
				return
			}

			claims := &Claims{}
			token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return jwtSecret, nil
			})

			if err != nil || !token.Valid {
				writeError(w, http.StatusUnauthorized, "TOKEN_INVALID", "access token is invalid or expired")
				return
			}

			// Set all auth context values for downstream use.
			ctx := r.Context()
			ctx = WithClinicID(ctx, claims.ClinicID)
			ctx = WithStaffID(ctx, claims.StaffID)
			ctx = WithRole(ctx, claims.Role)
			ctx = WithPermissions(ctx, claims.Perms)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HumaContext is a type alias for huma.Context. Using an alias avoids a
// field-name collision with the Context() method we override on the wrapper.
type HumaContext = huma.Context

// humaContextWrapper wraps a huma.Context and overrides its Context() method
// to return a modified context.Context. This is necessary because huma.Context
// is an interface without a SetContext method.
type humaContextWrapper struct {
	HumaContext
	ctx context.Context
}

func (w *humaContextWrapper) Context() context.Context {
	return w.ctx
}

// AuthenticateHuma returns a Huma-level middleware that validates a Bearer JWT.
// Unlike the Chi-level Authenticate, this middleware injects auth context values
// directly into a huma.Context wrapper, ensuring downstream Huma handlers
// receive the correct context regardless of initialization order.
//
// Usage:
//
//	auth := mw.AuthenticateHuma(api, jwtSecret)
//	huma.Register(api, huma.Operation{
//	    Security:    []map[string][]string{{"bearerAuth": {}}},
//	    Middlewares: huma.Middlewares{auth},
//	}, handler)
func AuthenticateHuma(api huma.API, jwtSecret []byte) func(huma.Context, func(huma.Context)) {
	return func(hctx huma.Context, next func(huma.Context)) {
		tokenStr := ""

		authHeader := hctx.Header("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		}

		if tokenStr == "" {
			tokenStr = hctx.Query("token")
		}

		if tokenStr == "" {
			_ = huma.WriteErr(api, hctx, http.StatusUnauthorized, "missing or invalid authorization header or token query parameter")
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			_ = huma.WriteErr(api, hctx, http.StatusUnauthorized, "access token is invalid or expired")
			return
		}

		ctx := hctx.Context()
		ctx = WithClinicID(ctx, claims.ClinicID)
		ctx = WithStaffID(ctx, claims.StaffID)
		ctx = WithRole(ctx, claims.Role)
		ctx = WithPermissions(ctx, claims.Perms)

		next(&humaContextWrapper{HumaContext: hctx, ctx: ctx})
	}
}

// RequirePermission returns a Chi middleware that checks a single boolean permission.
// Use this on Chi router groups (r.Use / r.With).
//
// Usage:
//
//	r.With(middleware.RequirePermission(func(p domain.Permissions) bool {
//	    return p.ManageStaff
//	})).Post("/staff/invite", handler)
func RequirePermission(check func(domain.Permissions) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			perms := PermissionsFromContext(r.Context())
			if !check(perms) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "you do not have permission to perform this action")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequirePermissionHuma returns a huma operation middleware that checks a permission.
// Use this in huma.Operation{Middlewares: ...} — it is NOT a Chi middleware.
//
// Huma middleware has a different signature to Chi middleware:
//
//	func(ctx huma.Context, next func(huma.Context))
//
// The huma.API reference is required by huma.WriteErr for error serialisation.
//
// Usage:
//
//	huma.Operation{
//	    Middlewares: huma.Middlewares{
//	        middleware.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManageStaff }),
//	    },
//	}
func RequirePermissionHuma(api huma.API, check func(domain.Permissions) bool) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		perms := PermissionsFromContext(ctx.Context())
		if !check(perms) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "you do not have permission to perform this action")
			return
		}
		next(ctx)
	}
}

// writeError writes a consistent JSON error response.
// Handlers also use huma's error helpers, but middleware runs before huma
// so it writes raw JSON using the same envelope shape.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"$schema": "http://jerv.it/json/errors",
		"status":  status,
		"title":   message,
		"errors": []map[string]string{
			{"code": code, "message": message, "path": ""},
		},
	})
}
