package drugs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Tamper-evident chain helpers.
//
// Per docs/drug-register-compliance-v2.md §3.1 + §3.7 the chain scope is
// per-drug × strength × form within a clinic — matching UK MDR 2001
// Reg 20(1)(b) + NZ MDR 1977 Reg 37(2)(a) "each page = 1 form of 1
// drug". chain_key is a deterministic SHA256 over the page-identity
// tuple; service computes it once + repo uses it to find the chain
// head + write the new row.
//
// Hash computation:
//
//   row_hash = SHA256( canonical_row_bytes || prev_row_hash )
//
// canonical_row_bytes is a stable repr of the new row's regulator-meaningful
// fields. Order + delimiter matter; never reorder fields without a
// migration to recompute every chain.
//
// Zero hash = 32 zero bytes; used as prev_row_hash for the first row in
// a chain so verification logic doesn't need a special case.

// HashLen is the byte length of every hash in the chain.
const HashLen = 32

// ZeroHash is the prev_row_hash sentinel for the first row in a chain.
func ZeroHash() []byte {
	return make([]byte, HashLen)
}

// chainKey returns the SHA256 chain key for one drug-strength-form
// page in a clinic. Lowercased + trimmed inputs so cosmetic edits to
// the catalog don't fork the chain.
func chainKey(clinicID uuid.UUID, drugName, strength, form string) []byte {
	h := sha256.New()
	h.Write(clinicID[:])
	h.Write([]byte("|"))
	h.Write([]byte(strings.ToLower(strings.TrimSpace(drugName))))
	h.Write([]byte("|"))
	h.Write([]byte(strings.ToLower(strings.TrimSpace(strength))))
	h.Write([]byte("|"))
	h.Write([]byte(strings.ToLower(strings.TrimSpace(form))))
	sum := h.Sum(nil)
	return sum
}

// chainAdvisoryLockID coerces the first 8 bytes of a chain key into the
// int64 that pg_advisory_xact_lock expects. Collisions are possible
// (8 bytes ≠ unique) but tolerated — a false-positive lock just
// serialises two unrelated chains briefly. SHA256's first-8-bytes
// distribution is uniform; collisions are vanishingly rare in practice.
func chainAdvisoryLockID(key []byte) int64 {
	if len(key) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(key[:8])) //nolint:gosec
}

// canonicalRowBytes is the deterministic repr of a row's
// regulator-meaningful fields, used as the SHA256 input alongside the
// prev_row_hash. Order + delimiter (|) are part of the contract — the
// regulator export reproduces this format on verify, so any change to
// this function requires a migration to recompute every chain in every
// clinic.
//
// Subject + note IDs are intentionally NOT included: they are private
// to the encounter and a regulator inspector seeing the chain doesn't
// need them. Witness + prescriber are NOT included either — they're
// covered by the audit log + signature_hash. The chain proves the
// register is structurally intact, not the per-event metadata.
func canonicalRowBytes(
	id, clinicID uuid.UUID,
	chainK []byte,
	entrySeqInChain int64,
	operation string,
	quantity float64,
	unit string,
	drugName, strength, form string,
	balanceAfter float64,
	prevHash []byte,
) []byte {
	return []byte(fmt.Sprintf(
		"v2|%s|%s|%s|%d|%s|%g|%s|%s|%s|%s|%s|%g|%s",
		id.String(),
		clinicID.String(),
		hex.EncodeToString(chainK),
		entrySeqInChain,
		operation,
		quantity,
		unit,
		strings.ToLower(strings.TrimSpace(drugName)),
		strings.ToLower(strings.TrimSpace(strength)),
		strings.ToLower(strings.TrimSpace(form)),
		quantityKindForOp(operation),
		balanceAfter,
		hex.EncodeToString(prevHash),
	))
}

// quantityKindForOp returns "+" for ops that increase balance and "-"
// otherwise. Encoding the sign explicitly in the canonical bytes makes
// a tampering attempt that flips operation+quantity simultaneously
// detectable by the chain.
func quantityKindForOp(op string) string {
	switch op {
	case "receive":
		return "+"
	case "transfer":
		return "0"
	default:
		return "-"
	}
}

// computeRowHash returns SHA256(canonical || prev).
func computeRowHash(canonical, prevHash []byte) []byte {
	h := sha256.New()
	h.Write(canonical)
	h.Write(prevHash)
	return h.Sum(nil)
}

// nullIfEmpty maps an empty string to a nil-valued pgx parameter so
// the NOT NULL constraint shapes and the legacy backfill path stay
// orthogonal — a v1 caller passing zero-value DrugName lands as a SQL
// NULL rather than empty string.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullableBytes returns nil for an empty byte slice so pgx writes SQL
// NULL rather than an empty bytea.
func nullableBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
