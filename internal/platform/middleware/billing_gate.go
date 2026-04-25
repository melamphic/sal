package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ClinicStatusReader is the cross-domain port used by the grace-period
// gate to look up the current subscription lifecycle status of a clinic.
// Implemented in app.go by an adapter wrapping clinic.Service.GetStatus.
type ClinicStatusReader interface {
	GetStatus(ctx context.Context, clinicID uuid.UUID) (domain.ClinicStatus, error)
}

// gracePeriodBlockedPathPrefixes lists path prefixes that always pass
// through the grace-period gate. Auth must work so the clinic can sign
// in to recover; billing must work so they can pay; health is unauthed.
var gracePeriodBlockedPathPrefixes = []string{
	"/api/v1/auth/",
	"/api/v1/billing/",
	"/health",
}

// BlockWritesOnGracePeriod returns a Chi middleware that returns 402
// (Payment Required) on any non-read request when the authenticated
// clinic is in grace_period. Read methods (GET/HEAD/OPTIONS) and the
// auth/billing/health prefixes always pass through so the clinic can
// recover their account.
//
// The middleware re-parses the JWT to read clinic_id without trusting
// downstream auth ordering — same secret as Authenticate, so the
// re-parse cost is bounded and there's no race with route mounting.
// Requests without a valid JWT pass through; the per-operation auth
// middleware rejects them later.
func BlockWritesOnGracePeriod(reader ClinicStatusReader, jwtSecret []byte, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isWriteMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if isExemptPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			clinicID, ok := clinicIDFromAuthHeader(r, jwtSecret)
			if !ok {
				// No / invalid JWT — let the per-operation auth middleware
				// reject. We don't 401 here because some routes are public.
				next.ServeHTTP(w, r)
				return
			}

			status, err := reader.GetStatus(r.Context(), clinicID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					next.ServeHTTP(w, r)
					return
				}
				log.ErrorContext(r.Context(), "billing_gate: status lookup failed",
					slog.String("clinic_id", clinicID.String()),
					slog.String("err", err.Error()),
				)
				// Fail open: a transient DB blip shouldn't lock every clinic
				// out of the app. Status changes are rare; the next request
				// will try again.
				next.ServeHTTP(w, r)
				return
			}

			if status == domain.ClinicStatusGracePeriod {
				writeError(w, http.StatusPaymentRequired, "GRACE_PERIOD_BLOCKED",
					"your subscription is in grace period — settle the outstanding invoice to resume writes")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isExemptPath(p string) bool {
	for _, prefix := range gracePeriodBlockedPathPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// clinicIDFromAuthHeader pulls and verifies the Bearer JWT, returning
// the clinic_id claim. False on any parse / verification error so the
// caller can fall through and let auth.go produce the canonical 401.
func clinicIDFromAuthHeader(r *http.Request, jwtSecret []byte) (uuid.UUID, bool) {
	tokenStr := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
	}
	if tokenStr == "" {
		tokenStr = r.URL.Query().Get("token")
	}
	if tokenStr == "" {
		return uuid.Nil, false
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errAuthBadAlg
		}
		return jwtSecret, nil
	})
	if err != nil || token == nil || !token.Valid {
		return uuid.Nil, false
	}
	return claims.ClinicID, claims.ClinicID != uuid.Nil
}

var errAuthBadAlg = errors.New("unexpected signing method")
