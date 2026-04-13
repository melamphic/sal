# Sal — Backend Engineering Rules
**Read fully before writing code. Non-negotiable. Product spec: `/BACKEND_PLAN.md`.**

## Architecture
- Every domain package has exactly 4 files: `handler.go` (HTTP only) · `service.go` (logic only) · `repository.go` (SQL only) · `routes.go` (mounts only)
- Extra files within a package are fine for domain-specific helpers (e.g. `auth/tokens.go`)
- Cross-domain: call exported service interfaces only — never import another domain's types or query another domain's tables

## Layer rules
| Layer | Forbidden |
|---|---|
| Handler | SQL, `pgx` imports, business logic |
| Service | `net/http`, huma imports, SQL |
| Repository | Business logic, returning raw pgx errors |

## Error handling
- Always wrap: `fmt.Errorf("module.layer.func: %w", err)` — no naked `return nil, err`
- Sentinel errors: `domain.ErrNotFound`, `domain.ErrConflict`, `domain.ErrForbidden` — map these in handlers
- PG unique violations: **use `domain.IsUniqueViolation(err)`** — never check `pgErr.Code == "23505"` manually
- Scan/row helpers must wrap errors too — `fmt.Errorf("pkg.repo.scanRow: %w", err)` — wrapcheck enforces this

## Database
- `clinic_id` must be in EVERY WHERE clause on tenant tables — omitting it is a multi-tenancy bug
- All SQL lives in `repository.go` — written by hand using raw `pgx/v5`. No sqlc, no ORM.
- Use `scanOne`/`scanRow` helpers to avoid repeating Scan calls; they must wrap errors (wrapcheck)
- UUIDs: UUIDv7 for PKs · timestamps: TIMESTAMPTZ UTC · soft deletes: `archived_at TIMESTAMPTZ NULL`
- Migrations: goose SQL in `/migrations/`, always both Up and Down, never edit committed files

## Huma / API — non-obvious gotchas
- **Type names must be globally unique across ALL packages** — huma's OpenAPI schema registry panics on duplicates at startup
- Pattern: `ClinicResponse`, `StaffResponse`, `StaffListResponse` (prefix with package name; suppress revive stutter in `.golangci.yml`)
- Routes: `/api/v1/{resource}` — breaking changes require `/api/v2/`, never break v1

## Time — non-obvious gotchas
- Use `domain.TimeNow()` (mutex-safe function) — **never `time.Now()` directly**
- Freeze time in tests: `restore := domain.SetTimeNow(fn); t.Cleanup(restore)`
- **JWT tokens are deterministic**: same claims + same second = identical token — never assert two JWTs differ within the same test second (only assert refresh token rotates)

## Testing
- Unit tests: in-memory fake repo (see `staff/fake_repo_test.go` as template); no DB, no Docker
- Integration tests: `//go:build integration` tag + `TestMain` must call `testutil.IntegrationMain(m)`
- `testutil.NewTestDB(t)` shares a postgres container pool and truncates tables before each test
- Naming: `TestType_Method_Scenario` · always `t.Parallel()`

## Security (never deferred)
- Never log PII/PHI — audit logs record resource IDs only
- Every route must declare required permissions — no annotation = PR blocked
- Audio: UUID paths only, signed URLs in responses, never expose internal storage paths

## Make reference
```
make dev              → infra (postgres, mailpit, minio) + API server
make test             → unit tests only, no Docker
make test-integration → all tests including DB (requires Docker)
make lint             → golangci-lint (must pass before every commit)
make migrate          → goose up — runs directly via goose binary, NOT through app (app has no CLI)
make docs             → MkDocs site at http://localhost:8001
```
