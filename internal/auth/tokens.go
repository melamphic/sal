package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// generateOpaqueToken creates a cryptographically random 32-byte URL-safe token
// and returns both the raw token (sent to the user) and its SHA-256 hash (stored in DB).
// The raw token must never be stored — only the hash.
func generateOpaqueToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth.generateOpaqueToken: %w", err)
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return raw, hash, nil
}

// hashToken returns the SHA-256 hash of an incoming token string.
// Used to look up a token in the database without storing the raw value.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// issueAccessToken creates a signed JWT access token for the given staff member.
func issueAccessToken(
	staffID, clinicID uuid.UUID,
	role domain.StaffRole,
	perms domain.Permissions,
	secret []byte,
	ttl time.Duration,
) (string, error) {
	now := domain.TimeNow()
	claims := mw.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   staffID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "sal",
		},
		ClinicID: clinicID,
		StaffID:  staffID,
		Role:     role,
		Perms:    perms,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("auth.issueAccessToken: sign: %w", err)
	}
	return signed, nil
}
