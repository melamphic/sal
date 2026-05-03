package drugs

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestChainKey_DeterministicAcrossCosmeticEdits(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	// Same (clinic, drug, strength, form) — even with whitespace + casing
	// differences — must produce identical chain keys. Otherwise a
	// catalog rename would silently fork the chain.
	a := chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule")
	b := chainKey(clinicID, " methadone ", "10 MG/ML", "Ampoule")
	if !bytes.Equal(a, b) {
		t.Fatalf("chainKey should normalise inputs; got %x vs %x", a, b)
	}
}

func TestChainKey_DiffersByDrug(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	a := chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule")
	b := chainKey(clinicID, "Morphine", "10 mg/mL", "ampoule")
	if bytes.Equal(a, b) {
		t.Fatalf("chainKey should differ by drug name")
	}
}

func TestChainKey_DiffersByStrength(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	a := chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule")
	b := chainKey(clinicID, "Methadone", "5 mg/mL", "ampoule")
	if bytes.Equal(a, b) {
		t.Fatalf("chainKey should differ by strength (per UK Reg 20: each page = one strength)")
	}
}

func TestChainKey_DiffersByForm(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	a := chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule")
	b := chainKey(clinicID, "Methadone", "10 mg/mL", "vial")
	if bytes.Equal(a, b) {
		t.Fatalf("chainKey should differ by form")
	}
}

func TestChainKey_DiffersByClinic(t *testing.T) {
	t.Parallel()

	a := chainKey(
		uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		"Methadone", "10 mg/mL", "ampoule",
	)
	b := chainKey(
		uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		"Methadone", "10 mg/mL", "ampoule",
	)
	if bytes.Equal(a, b) {
		t.Fatalf("chainKey must be per-clinic; got identical for different clinics")
	}
}

func TestZeroHash_LengthAndZero(t *testing.T) {
	t.Parallel()

	z := ZeroHash()
	if len(z) != HashLen {
		t.Fatalf("ZeroHash length = %d, want %d", len(z), HashLen)
	}
	for _, b := range z {
		if b != 0 {
			t.Fatalf("ZeroHash should be all zeros")
		}
	}
}

func TestComputeRowHash_TamperingDetected(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	opID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	chainK := chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule")

	// Original row.
	canonical := canonicalRowBytes(opID, clinicID, chainK, 1, "administer", 0.5, "mL",
		"Methadone", "10 mg/mL", "ampoule", 9.5, ZeroHash())
	hashA := computeRowHash(canonical, ZeroHash())

	// Tamper: change the quantity (a regulator-meaningful field).
	canonicalT := canonicalRowBytes(opID, clinicID, chainK, 1, "administer", 5.0, "mL",
		"Methadone", "10 mg/mL", "ampoule", 5.0, ZeroHash())
	hashT := computeRowHash(canonicalT, ZeroHash())

	if bytes.Equal(hashA, hashT) {
		t.Fatalf("row_hash must change when quantity changes — chain useless otherwise")
	}
}

func TestComputeRowHash_ChainsForward(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	chainK := chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule")

	row1 := canonicalRowBytes(uuid.New(), clinicID, chainK, 1, "receive", 10.0, "mL",
		"Methadone", "10 mg/mL", "ampoule", 10.0, ZeroHash())
	hash1 := computeRowHash(row1, ZeroHash())

	row2 := canonicalRowBytes(uuid.New(), clinicID, chainK, 2, "administer", 0.5, "mL",
		"Methadone", "10 mg/mL", "ampoule", 9.5, hash1)
	hash2A := computeRowHash(row2, hash1)

	// Replay row2 with a different prev_row_hash — hash must differ.
	row2B := canonicalRowBytes(uuid.New(), clinicID, chainK, 2, "administer", 0.5, "mL",
		"Methadone", "10 mg/mL", "ampoule", 9.5, ZeroHash())
	hash2B := computeRowHash(row2B, ZeroHash())

	if bytes.Equal(hash2A, hash2B) {
		t.Fatalf("row_hash must depend on prev_row_hash (chain integrity)")
	}
}

func TestQuantityKindForOp(t *testing.T) {
	t.Parallel()

	if quantityKindForOp("receive") != "+" {
		t.Errorf("receive should be +")
	}
	if quantityKindForOp("transfer") != "0" {
		t.Errorf("transfer should be 0")
	}
	for _, op := range []string{"administer", "dispense", "discard", "adjust"} {
		if quantityKindForOp(op) != "-" {
			t.Errorf("%s should be -", op)
		}
	}
}

func TestChainAdvisoryLockID_Stable(t *testing.T) {
	t.Parallel()

	clinicID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	a := chainAdvisoryLockID(chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule"))
	b := chainAdvisoryLockID(chainKey(clinicID, "Methadone", "10 mg/mL", "ampoule"))
	if a != b {
		t.Fatalf("advisory lock id should be deterministic for the same chain")
	}
	if a == 0 {
		t.Fatalf("advisory lock id should be non-zero for normal chains")
	}
}
