package crypto_test

import (
	"strings"
	"testing"

	"github.com/melamphic/sal/internal/platform/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey is a fixed 32-byte key used only in tests.
var testKey = [32]byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
	0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F,
}

func newCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(testKey[:])
	require.NoError(t, err)
	return c
}

// ── New ───────────────────────────────────────────────────────────────────────

func TestNew_RejectsShortKey(t *testing.T) {
	t.Parallel()
	_, err := crypto.New([]byte("too-short"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")
}

func TestNew_RejectsLongKey(t *testing.T) {
	t.Parallel()
	_, err := crypto.New(make([]byte, 64))
	require.Error(t, err)
}

func TestNew_Accepts32ByteKey(t *testing.T) {
	t.Parallel()
	_, err := crypto.New(testKey[:])
	require.NoError(t, err)
}

// ── Encrypt / Decrypt ─────────────────────────────────────────────────────────

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()
	c := newCipher(t)

	plaintext := "dr.sarah@riversidevetclinic.co.nz"
	ciphertext, err := c.Encrypt(plaintext)
	require.NoError(t, err)
	require.NotEmpty(t, ciphertext)
	assert.NotEqual(t, plaintext, ciphertext)

	decrypted, err := c.Decrypt(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncrypt_ProducesUniqueOutputEachCall(t *testing.T) {
	t.Parallel()
	// AES-GCM uses a random nonce — the same plaintext must produce different ciphertexts.
	c := newCipher(t)
	plaintext := "same-plaintext"

	ct1, err := c.Encrypt(plaintext)
	require.NoError(t, err)
	ct2, err := c.Encrypt(plaintext)
	require.NoError(t, err)

	assert.NotEqual(t, ct1, ct2, "encryption must be non-deterministic (random nonce)")
}

func TestDecrypt_BothCiphertextsDecryptToSamePlaintext(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	plaintext := "same-plaintext"

	ct1, _ := c.Encrypt(plaintext)
	ct2, _ := c.Encrypt(plaintext)

	d1, err := c.Decrypt(ct1)
	require.NoError(t, err)
	d2, err := c.Decrypt(ct2)
	require.NoError(t, err)

	assert.Equal(t, plaintext, d1)
	assert.Equal(t, plaintext, d2)
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	t.Parallel()
	// Empty string must round-trip as empty — used for optional PII fields.
	c := newCipher(t)
	ct, err := c.Encrypt("")
	require.NoError(t, err)
	assert.Empty(t, ct)

	dt, err := c.Decrypt("")
	require.NoError(t, err)
	assert.Empty(t, dt)
}

func TestDecrypt_RejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	ct, err := c.Encrypt("sensitive-value")
	require.NoError(t, err)

	// Flip a character in the middle of the base64 string.
	tampered := []byte(ct)
	mid := len(tampered) / 2
	tampered[mid] ^= 0xFF

	_, err = c.Decrypt(string(tampered))
	require.Error(t, err, "tampered ciphertext must not decrypt successfully")
}

func TestDecrypt_RejectsInvalidBase64(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	_, err := c.Decrypt("not-valid-base64!!!")
	require.Error(t, err)
}

func TestDecrypt_RejectsTooShortCiphertext(t *testing.T) {
	t.Parallel()
	import64 := "aGVsbG8=" // "hello" in base64 — shorter than nonce size
	c := newCipher(t)
	_, err := c.Decrypt(import64)
	require.Error(t, err)
}

func TestEncryptDecrypt_UnicodeAndSpecialChars(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	cases := []string{
		"Wānaka Veterinary Clinic",
		"vet+clinic@例え.jp",
		"1234567890!@#$%^&*()",
		strings.Repeat("a", 10_000),
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc[:min(len(tc), 30)], func(t *testing.T) {
			t.Parallel()
			ct, err := c.Encrypt(tc)
			require.NoError(t, err)
			dt, err := c.Decrypt(ct)
			require.NoError(t, err)
			assert.Equal(t, tc, dt)
		})
	}
}

// ── Hash ──────────────────────────────────────────────────────────────────────

func TestHash_IsDeterministic(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	email := "sarah@clinic.co.nz"
	assert.Equal(t, c.Hash(email), c.Hash(email))
}

func TestHash_IsCaseInsensitive(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	assert.Equal(t, c.Hash("SARAH@CLINIC.CO.NZ"), c.Hash("sarah@clinic.co.nz"))
	assert.Equal(t, c.Hash("Sarah@Clinic.Co.Nz"), c.Hash("sarah@clinic.co.nz"))
}

func TestHash_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	assert.Equal(t, c.Hash("sarah@clinic.co.nz"), c.Hash("  sarah@clinic.co.nz  "))
}

func TestHash_DifferentInputsDifferentOutputs(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	assert.NotEqual(t, c.Hash("email1@test.com"), c.Hash("email2@test.com"))
}

func TestHash_ReturnsHexString(t *testing.T) {
	t.Parallel()
	c := newCipher(t)
	h := c.Hash("test@test.com")
	assert.Len(t, h, 64, "SHA-256 produces 32 bytes = 64 hex chars")
	for _, ch := range h {
		assert.True(t, (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f'),
			"hash must be lowercase hex, got %c", ch)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
