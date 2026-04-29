package aigen

import (
	"crypto/sha256"
	"encoding/hex"
)

// promptShortHash returns the 16-char prefix of the SHA-256 of the prompt.
// Used in AIMetadata.PromptHash so audit logs can correlate generations
// without leaking full prompt content.
func promptShortHash(prompt string) string {
	h := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(h[:])[:16]
}
