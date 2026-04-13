// Package crypto provides AES-256-GCM encryption for PII and PHI fields stored
// in the database. All encryption and decryption happens in Go — the database
// stores only ciphertext and never sees plaintext values.
//
// Design rationale (application-level vs pgcrypto):
//   - Export: decrypt all PII in Go before writing to an archive file.
//   - Backups: DB dump files contain only ciphertext — safe to store without
//     additional encryption at rest.
//   - Key rotation: read rows → decrypt with old key → re-encrypt with new key →
//     write back. A background River job handles this without schema changes.
//   - Logs: query logs never contain plaintext PII.
//   - Searchable unique fields (e.g. email): store an HMAC-SHA256 hash alongside
//     the ciphertext for equality lookups. Use Cipher.Hash for this.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// Cipher encrypts and hashes PII/PHI values using a 32-byte AES-256 key.
type Cipher struct {
	key []byte
}

// New creates a Cipher from a 32-byte key.
// The key must be exactly 32 bytes — use config.Config.EncryptionKey() to obtain it.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be exactly 32 bytes, got %d", len(key))
	}
	// Copy the key so the caller cannot mutate it.
	k := make([]byte, 32)
	copy(k, key)
	return &Cipher{key: k}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns a base64-encoded
// ciphertext string. Each call produces a different ciphertext (random nonce)
// but all are decryptable with the same key.
//
// Store the result in a TEXT column annotated with -- PII: encrypted.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", fmt.Errorf("crypto.Encrypt: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto.Encrypt: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto.Encrypt: generate nonce: %w", err)
	}

	// Seal appends ciphertext to nonce so Decrypt can split them.
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decodes a base64 ciphertext and returns the original plaintext.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("crypto.Decrypt: base64 decode: %w", err)
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", fmt.Errorf("crypto.Decrypt: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto.Decrypt: new gcm: %w", err)
	}

	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("crypto.Decrypt: ciphertext too short")
	}

	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("crypto.Decrypt: open: %w", err)
	}

	return string(plaintext), nil
}

// Hash produces a deterministic HMAC-SHA256 of plaintext, suitable for
// equality checks on encrypted fields (e.g. looking up a staff member by email).
//
// Input is lowercased and trimmed before hashing so lookups are case-insensitive.
// The result is hex-encoded. Store alongside the encrypted value in an _hash column.
//
// Example: staff.email (TEXT, encrypted) + staff.email_hash (TEXT, indexed).
func (c *Cipher) Hash(plaintext string) string {
	normalised := strings.ToLower(strings.TrimSpace(plaintext))
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(normalised))
	return hex.EncodeToString(mac.Sum(nil))
}

// MustEncrypt is a convenience wrapper that panics on error.
// Only use this for known-safe inputs in tests or seeding — never in request paths.
func (c *Cipher) MustEncrypt(plaintext string) string {
	v, err := c.Encrypt(plaintext)
	if err != nil {
		panic(fmt.Sprintf("crypto.MustEncrypt: %v", err))
	}
	return v
}
