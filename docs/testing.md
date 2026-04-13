# Testing

`sal` has two test tiers: **unit tests** (fast, no Docker) and **integration tests** (real PostgreSQL via testcontainers).

---

## Running tests

```bash
# Unit tests only — runs in seconds, no Docker required
make test
# equivalent: go test ./internal/... -count=1

# Integration tests — requires Docker
make test-integration
# equivalent: go test -tags integration ./internal/... -count=1
```

---

## Unit tests

Unit tests live alongside the code they test (`service_test.go` next to `service.go`). They use **in-memory fakes** for all I/O:

- **Repository fakes:** hand-rolled `fakeRepo` structs (defined in `*_test.go` files) satisfy the domain's `repo` interface using `sync.Mutex`-protected maps. They live inside the production package (not `_test`) so they can access unexported types.
- **Mailer fake:** `testutil.FakeMailer` records sent emails in memory. Use `m.Count("magic_link")` and `m.Last()` to assert on email sends.
- **Cipher:** `testutil.TestCipher(t)` returns a real `*crypto.Cipher` seeded with a fixed test key — encryption is exercised but non-deterministic.
- **Time:** `testutil.FreezeTime(t)` replaces `domain.TimeNow` with a fixed clock. Restored via `t.Cleanup`.

### What unit tests cover

| Package | Tests |
|---|---|
| `platform/crypto` | Key validation, encrypt/decrypt roundtrip, nonce uniqueness, empty strings, tampered ciphertext, hash determinism/case-insensitivity |
| `auth` | SendMagicLink, VerifyMagicLink (valid/replay/expired/wrong type), RefreshTokens (rotation/invalidation), Logout |
| `clinic` | generateSlug (unicode, truncation), Register (success/defaults/duplicate/encryption), GetByID, Update |
| `staff` | Create (encryption), GetByID (multi-tenant isolation), Invite (duplicate/cross-clinic), List (pagination), UpdatePermissions (role guard), Deactivate (self-guard) |

---

## Integration tests

Integration tests carry the build tag `//go:build integration` and are not compiled during normal `go test`. They test the **repository layer** against a real PostgreSQL instance.

### How it works

Each module that needs a DB test has a `TestMain`:

```go
func TestMain(m *testing.M) {
    testutil.IntegrationMain(m)
}
```

`testutil.IntegrationMain`:
1. Starts a `postgres:17-alpine` container via testcontainers-go
2. Runs all goose migrations against it
3. Exposes `testutil.NewTestDB(t)` which returns the shared `*pgxpool.Pool`
4. Before returning the pool, `NewTestDB` truncates all tables (in FK-safe order) so each test starts clean

The container is shared across all tests in the binary — starting one container per binary (not per test) keeps the suite fast.

### Writing a new integration test

```go
//go:build integration

package mymodule_test

import (
    "testing"
    "github.com/melamphic/sal/internal/testutil"
)

func TestMain(m *testing.M) {
    testutil.IntegrationMain(m)
}

func TestRepository_SomeMethod(t *testing.T) {
    t.Parallel()
    pool := testutil.NewTestDB(t)  // truncates tables, returns pool
    r := mymodule.NewRepository(pool)
    // ...
}
```

### What integration tests cover

| Package | Tests |
|---|---|
| `clinic` | Create (roundtrip, duplicate email_hash), GetByID, GetByEmailHash, Update |
| `auth` | FindStaffByEmailHash, CreateAuthToken, GetAndConsumeAuthToken (roundtrip/replay/expired/notfound), DeleteRefreshTokensForStaff |
| `staff` | Create, GetByID (wrong clinic), ExistsByEmailHash (cross-clinic), List (isolation/pagination), UpdatePermissions, Deactivate |

---

## Test naming convention

```
Test{Type}_{Method}_{Scenario}

Examples:
  TestService_Register_DuplicateEmail_ReturnsConflict
  TestRepository_GetByID_WrongClinic_ReturnsNotFound
  TestGenerateSlug_SpecialCharacters
```

---

## Shared test helpers (`internal/testutil`)

| Helper | Description |
|---|---|
| `TestCipher(t)` | `*crypto.Cipher` with fixed test key |
| `TestJWTSecret` | Fixed `[]byte` JWT secret |
| `FixedTime` | `time.Time` at `2026-04-05T10:00:00Z` |
| `FreezeTime(t)` | Replaces `domain.TimeNow`, restores on cleanup |
| `NewID()` | Returns `uuid.New()` — convenience wrapper |
| `Ptr[T](v)` | Returns pointer to value — reduces test clutter |
| `FakeMailer` | Records sent emails; `Count(template)`, `Last()`, `Reset()` |
| `IntegrationMain(m)` | Starts Postgres container, runs migrations, calls `m.Run()` |
| `NewTestDB(t)` | Returns shared pool after truncating all tables |

---

## CI

`make test` runs on every push. `make test-integration` runs on PRs targeting `main`, with Docker available in the CI environment. A PR that fails either check does not merge.
