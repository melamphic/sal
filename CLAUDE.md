# Sal — Backend Engineering Rules
**Read this fully before writing any code. These are non-negotiable.**

This is the Go backend for Salvia (Melamphic). The product spec is in `/BACKEND_PLAN.md`. The architecture, technology choices, and patterns below are locked decisions. Do not deviate without explicit sign-off from the team lead.

---

## 0. Before You Write Anything

Ask yourself:
1. Which phase are we in? Only build what that phase requires.
2. Which domain does this belong to? Put it in the right package.
3. Does a similar abstraction already exist? Reuse it.
4. Is this the right layer? SQL belongs in repository. Logic belongs in service. HTTP belongs in handler.
5. Will this compile with `make lint`? If not, fix it before committing.

If you are unsure about any architectural decision, **stop and ask** before writing code. Wrong structure costs more to fix than slow code.

---

## 1. Project Structure Rules

### Package layout
```
internal/
  platform/     ← infrastructure only (DB, config, mailer, storage, middleware, logger)
  domain/       ← shared types ONLY (IDs, enums, errors, pagination) — no business logic here
  <module>/     ← one package per domain (auth, clinic, staff, patient, audio, ...)
```

### Domain package anatomy — ALWAYS four files
```
internal/<module>/
  handler.go      ← HTTP ONLY
  service.go      ← business logic ONLY
  repository.go   ← database ONLY
  routes.go       ← route mounting ONLY
```

**No exceptions.** If a file doesn't fit these four, either you need a helper file within the same package (e.g. `tokens.go` inside `auth/`) or you're putting something in the wrong layer.

### Cross-domain communication
- Module A **must not** import Module B's internal types directly
- Module A calls Module B's exported service interface (defined in `internal/domain/`)
- Module A **never** queries Module B's database tables

---

## 2. Layer Rules (Hard Constraints)

### Handler (`handler.go`)
- Parses request → calls service → writes response. That is ALL it does.
- Never contains SQL, business logic, or direct DB calls
- Never imports `pgx` or `sqlc` generated packages
- Always uses huma operation types for request/response (not raw `http.ResponseWriter`)
- Always extracts `clinicID` and `staffID` from context via `platform/middleware`
- Always validates permissions before calling service

### Service (`service.go`)
- Contains all business logic
- Never imports `net/http` or huma packages
- Never writes SQL directly — calls repository methods only
- Accepts and returns domain types, not HTTP types
- Always accepts `context.Context` as first parameter
- Always wraps errors: `fmt.Errorf("auth.service.CreateMagicLink: %w", err)`

### Repository (`repository.go`)
- Contains only database interaction — sqlc-generated calls + raw pgx for complex queries
- Never contains business logic or HTTP concerns
- Always accepts `context.Context` as first parameter
- Never returns raw `pgx` errors to callers — wrap them: `fmt.Errorf("auth.repo.FindByEmail: %w", err)`
- For transactions, accept a `pgx.Tx` parameter — do not manage transaction lifecycle inside repository methods

### Routes (`routes.go`)
- One exported function: `func (h *Handler) Mount(r chi.Router)`
- Only mounts routes, applies route-level middleware
- Zero business logic

---

## 3. Error Handling

```go
// CORRECT — always wrap with module.layer.function context
return nil, fmt.Errorf("patient.service.Create: %w", err)

// CORRECT — use sentinel errors from internal/domain/errors.go for known cases
if errors.Is(err, domain.ErrNotFound) { ... }

// WRONG — never swallow errors
result, _ := repo.Get(ctx, id)

// WRONG — never return raw errors without context
return nil, err

// WRONG — never use panic for recoverable errors
panic("something went wrong")
```

Every error must be:
1. Wrapped with `fmt.Errorf("module.layer.function: %w", err)`
2. Either handled at the call site or propagated up with the wrap
3. Logged at the point where it crosses the handler boundary (not at every intermediate layer)

---

## 4. Database Rules

### Queries
- Static queries → `sqlc` (run `make generate` to regenerate after changing `.sql` files)
- Dynamic filters/search → `squirrel` query builder
- Complex transactions → raw `pgx/v5`
- **Never write raw SQL strings inside Go handler or service files**

### Migrations
- Every migration is a new `goose` SQL file in `/migrations/`
- Files are named `{5-digit-sequence}_{descriptive_name}.sql`
- Example: `00003_create_patients.sql`
- Every migration has both `-- +goose Up` and `-- +goose Down` sections
- Never modify an existing migration file that has been committed
- Migrations run automatically at startup in development; manually in production with `make migrate`

### Schema conventions
- All primary keys: `UUID` (UUIDv7 — time-ordered)
- All timestamps: `TIMESTAMPTZ` in UTC
- Soft deletes: `archived_at TIMESTAMPTZ NULL` (not a `deleted` boolean)
- Multi-tenant scoping: every tenant table has `clinic_id UUID NOT NULL REFERENCES clinics(id)`
- String enums: `VARCHAR NOT NULL CHECK (col IN ('a','b','c'))` — never Postgres `ENUM` type
- PII columns: document with comment `-- PII: encrypted` and use pgcrypto wrapper functions

---

## 5. Security & Compliance Rules

These are never skipped, never deferred to "later":

1. **Never log PII or PHI.** Audit logs record resource IDs, not values. If you log a user object, ensure PII fields implement `String() string { return "[REDACTED]" }`.
2. **Every route that touches data must declare its required permission.** No permission annotation = PR blocked.
3. **Every PHI/PII field access must be audited.** The audit middleware handles this if you use the correct service helpers — do not bypass it.
4. **clinic_id must be in every WHERE clause** on tenant-scoped tables. If a query does not filter by clinic_id, it is a multi-tenancy bug.
5. **Audio files are referenced by UUID path only.** Never construct a guessable file path. Never expose internal storage paths in API responses — only signed URLs.
6. **Never store secrets in code, config files, or comments.** Secrets come from environment variables only.
7. **TLS everywhere** — no plain HTTP endpoints in production. The dev Docker setup may use plain HTTP on localhost only.

---

## 6. Interfaces & Dependency Injection

### Every external dependency must be behind an interface

```go
// CORRECT — testable, swappable
type EmailSender interface {
    Send(ctx context.Context, to, subject, body string) error
}

// CORRECT — inject the interface, not the concrete type
type Service struct {
    repo   Repository
    mailer platform.EmailSender
}

// WRONG — concrete type as field means you can't test without hitting Resend
type Service struct {
    mailer *resend.Client
}
```

Interfaces live in the package that **uses** them, not the package that implements them. (Go convention: accept interfaces, return structs.)

### Dependency injection is constructor-based
```go
// CORRECT
func NewService(repo Repository, mailer platform.EmailSender, cfg *Config) *Service {
    return &Service{repo: repo, mailer: mailer, cfg: cfg}
}

// WRONG — global state, untestable
var globalDB *pgxpool.Pool
```

No global variables for dependencies. Everything flows through constructors and is wired in `internal/app/`.

---

## 7. Testing Rules

- Every service function has a unit test (mock the repository interface)
- Every repository function has an integration test (real PostgreSQL via testcontainers)
- Every handler has an integration test (real HTTP via `httptest`, real DB)
- Test files live alongside the code they test: `service_test.go` next to `service.go`
- Test names: `TestService_CreateMagicLink_ExpiredToken` — `Test{Type}_{Method}_{Scenario}`
- Use `t.Parallel()` in every test that doesn't share state
- Use `t.Cleanup()` not `defer` for teardown
- Build tags: `//go:build integration` for tests that need Docker

```
make test           → runs unit tests only (fast, no Docker)
make test-integration → runs all tests including DB integration (requires Docker)
```

---

## 8. API Design Rules

- All routes: `/api/v1/{resource}`
- Resource names: plural nouns (`/patients`, `/forms`, `/staff`)
- Use huma operations — every endpoint has: summary, description, tags, and typed request/response structs
- Response types are separate from domain types. Never return a DB model directly.
- List endpoints: always paginated with `cursor` + `limit` — never return unbounded lists
- Errors: always `{"error": {"code": "SNAKE_CASE_CODE", "message": "human readable"}}`
- Breaking changes require a new API version (`/api/v2/`) — never break `/api/v1/`

---

## 9. Vertical (Domain Agnosticism) Rules

- The `vertical` field on `clinics` drives domain-specific behaviour
- Generic core packages (`timeline`, `policy`, `compliance`, `forms`) must not contain any vertical-specific logic
- Vertical-specific behaviour is implemented by satisfying interfaces defined in `internal/domain/`
- Adding a new vertical must not require modifying any existing package
- The `subjects` table is generic — vertical-specific fields go in extension tables (`vet_subject_details`, etc.)

---

## 10. Code Style

- `gofmt` and `goimports` are always run (enforced by `golangci-lint`)
- Exported symbols have Go doc comments
- Non-obvious logic has a comment explaining **why**, not what
- No `// TODO` without a GitHub issue number: `// TODO(#123): ...`
- Max function length: ~50 lines. If it's longer, it's probably doing too much.
- Prefer early returns over nested conditionals

---

## 11. Git & PR Rules

- Branch naming: `feat/phase-0-auth`, `fix/timeline-sse-reconnect`, `chore/update-deps`
- Every PR links to the phase it implements
- `make lint` and `make test` must pass before requesting review
- No commented-out code in PRs
- Migration files in a PR must have a corresponding rollback in `-- +goose Down`

---

## 12. Makefile Reference

```
make dev          → start docker-compose (postgres, mailpit, minio) + run API
make build        → compile binary
make test         → unit tests
make test-integration → all tests (requires Docker)
make lint         → golangci-lint
make migrate      → run pending goose migrations
make migrate-down → rollback one migration
make generate     → run sqlc codegen
make docs         → open Swagger UI in browser
```

---

## Enforcement

`golangci-lint` runs on every commit (pre-commit hook) and in CI. The `.golangci.yml` config enforces:
- `errcheck` — no unchecked errors
- `govet` — go vet checks
- `staticcheck` — advanced static analysis
- `revive` — style rules including exported doc comments
- `exhaustruct` — all struct fields initialized explicitly (catches missing deps in constructors)
- `wrapcheck` — errors from external packages must be wrapped
- `noctx` — functions that should accept context.Context do
- `godot` — comments end with a period

A PR that fails lint does not get merged. No exceptions.
