// Package testutil provides shared helpers for unit and integration tests.
// Nothing in this package may be imported by production code.
package testutil

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/platform/crypto"
)

// TestKey is a fixed 32-byte AES-256 key for use in tests.
// Never use this in production — it is publicly known.
var TestKey = [32]byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
	0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F,
}

// TestCipher returns a Cipher initialised with TestKey.
func TestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(TestKey[:])
	if err != nil {
		t.Fatalf("testutil.TestCipher: %v", err)
	}
	return c
}

// TestJWTSecret is a fixed JWT HMAC secret for use in tests.
var TestJWTSecret = []byte("test-jwt-secret-that-is-at-least-32-bytes-long!")

// FixedTime is a fixed point in time used to make tests deterministic.
var FixedTime = time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)

// FreezeTime replaces the domain clock with FixedTime and restores it when the
// test ends. Safe to call from parallel tests — uses a mutex internally.
func FreezeTime(t *testing.T) {
	t.Helper()
	restore := domain.SetTimeNow(func() time.Time { return FixedTime })
	t.Cleanup(restore)
}

// NewID returns a deterministic UUID based on a sequence number.
// Use this to produce predictable IDs in tests.
func NewID() uuid.UUID {
	return uuid.New()
}

// Ptr returns a pointer to the given value — removes clutter in test cases.
func Ptr[T any](v T) *T { return &v }
