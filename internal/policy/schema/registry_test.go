package schema

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIsValidParity_Whitelist(t *testing.T) {
	t.Parallel()
	for _, p := range AllParities() {
		if !IsValidParity(string(p)) {
			t.Errorf("AllParities lists %q but IsValidParity rejects it", p)
		}
	}
	if IsValidParity("critical") {
		t.Error("IsValidParity should reject unknown values")
	}
}

// TestParityWeight_HasArmForEvery enforces that every Parity constant has a
// weight branch — adding a new parity without updating ParityWeight would
// fall through to the default of 1, silently mis-scoring policies.
func TestParityWeight_HasArmForEvery(t *testing.T) {
	t.Parallel()
	for _, p := range AllParities() {
		w := ParityWeight(string(p))
		// All known parities must yield a distinct, sensible weight.
		// The default-fall-through weight is 1; high/medium must exceed that.
		switch p {
		case ParityHigh:
			if w != 3 {
				t.Errorf("ParityWeight(%q) = %d, want 3", p, w)
			}
		case ParityMedium:
			if w != 2 {
				t.Errorf("ParityWeight(%q) = %d, want 2", p, w)
			}
		case ParityLow:
			if w != 1 {
				t.Errorf("ParityWeight(%q) = %d, want 1", p, w)
			}
		default:
			t.Errorf("Parity %q has no weight assertion in test — update test when adding parity", p)
		}
	}
}

func TestParityEnumValues_MatchesAllParities(t *testing.T) {
	t.Parallel()
	if got, want := len(ParityEnumValues()), len(AllParities()); got != want {
		t.Fatalf("enum drift: %d enum values vs %d Parity constants", got, want)
	}
}

func TestParseContent_Valid(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"id":"a","type":"heading"},{"id":"b","type":"paragraph"}]`)
	blocks, err := ParseContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	ids := CollectBlockIDs(blocks)
	if _, ok := ids["a"]; !ok {
		t.Error("collected ids missing 'a'")
	}
}

func TestParseContent_DuplicateID(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"id":"a","type":"heading"},{"id":"a","type":"paragraph"}]`)
	_, err := ParseContent(raw)
	if !errors.Is(err, ErrBlockDuplicateID) {
		t.Fatalf("got %v, want ErrBlockDuplicateID", err)
	}
}

func TestParseContent_MissingID(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"type":"heading"}]`)
	_, err := ParseContent(raw)
	if !errors.Is(err, ErrBlockMissingID) {
		t.Fatalf("got %v, want ErrBlockMissingID", err)
	}
}

func TestParseContent_NotArray(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"id":"a"}`)
	_, err := ParseContent(raw)
	if !errors.Is(err, ErrContentNotArray) {
		t.Fatalf("got %v, want ErrContentNotArray", err)
	}
}

func TestParseContent_Empty(t *testing.T) {
	t.Parallel()
	blocks, err := ParseContent(nil)
	if err != nil {
		t.Fatalf("nil content returned error: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}
