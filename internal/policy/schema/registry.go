// Package schema is the single source of truth for policy clause parity values
// and policy block-content shape constraints. Single edit propagates to the
// validator and provider response-schema enum values.
//
// Block IDs MUST be unique within content and stable across edits — clauses
// reference them by string. Adding a new parity value requires updating
// AllParities AND ParityWeight; the consistency test enforces this.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Parity is the canonical enum of clause severities used in the
// parity-weighted policy-coverage score.
type Parity string

const (
	ParityHigh   Parity = "high"
	ParityMedium Parity = "medium"
	ParityLow    Parity = "low"
)

// AllParities returns the canonical parity list. Order is stable so it can be
// fed to provider response schemas as an enum.
func AllParities() []Parity {
	return []Parity{ParityHigh, ParityMedium, ParityLow}
}

// IsValidParity reports whether s is one of the canonical parity strings.
func IsValidParity(s string) bool {
	for _, p := range AllParities() {
		if string(p) == s {
			return true
		}
	}
	return false
}

// ParityWeight returns the integer weight used when computing the
// parity-weighted policy-coverage score (high=3, medium=2, low=1).
// Unknown values default to 1 so unrecognized data does not crash the score.
func ParityWeight(p string) int {
	switch p {
	case string(ParityHigh):
		return 3
	case string(ParityMedium):
		return 2
	case string(ParityLow):
		return 1
	default:
		return 1
	}
}

// ParityEnumValues returns the canonical parity list as []string for use in
// OpenAPI / provider response-schema enum constraints.
func ParityEnumValues() []string {
	parities := AllParities()
	out := make([]string, len(parities))
	for i, p := range parities {
		out[i] = string(p)
	}
	return out
}

// Block represents a single editor block in policy content JSONB. Each block
// has a stable ID; clauses reference it by string. Backend treats most fields
// opaquely — only ID + uniqueness is enforced server-side.
type Block struct {
	ID   string          `json:"id"`
	Type string          `json:"type,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Sentinel errors so callers can branch with errors.Is.
var (
	ErrInvalidParity    = errors.New("schema: invalid parity")
	ErrContentNotArray  = errors.New("schema: content must be JSON array of blocks")
	ErrBlockMissingID   = errors.New("schema: block missing id")
	ErrBlockDuplicateID = errors.New("schema: duplicate block id")
)

// ParseContent parses a policy content JSONB blob into typed blocks and
// validates structural invariants:
//   - content is a JSON array
//   - every block has a non-empty id
//   - block ids are unique within the array
//
// Block type/data is otherwise opaque — the editor owns rendering.
func ParseContent(raw json.RawMessage) ([]Block, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var blocks []Block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrContentNotArray, err)
	}
	seen := make(map[string]struct{}, len(blocks))
	for i, b := range blocks {
		if b.ID == "" {
			return nil, fmt.Errorf("%w: index %d", ErrBlockMissingID, i)
		}
		if _, ok := seen[b.ID]; ok {
			return nil, fmt.Errorf("%w: %s", ErrBlockDuplicateID, b.ID)
		}
		seen[b.ID] = struct{}{}
	}
	return blocks, nil
}

// CollectBlockIDs returns the set of block IDs present in content. Useful for
// validating that clauses reference blocks that actually exist.
func CollectBlockIDs(blocks []Block) map[string]struct{} {
	out := make(map[string]struct{}, len(blocks))
	for _, b := range blocks {
		out[b.ID] = struct{}{}
	}
	return out
}
