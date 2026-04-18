# Architecture

`sal` is a **modularised monolith** — a single deployable Go binary organised into well-bounded internal packages. The goal is the development simplicity of a monolith with the code hygiene of microservices. Splitting into actual services is possible later by promoting packages into their own binaries; the layer discipline enforced here makes that migration mechanical.

---

## Top-level layout

```
sal/
├── cmd/api/          ← entry point — wires everything, starts HTTP server
├── internal/
│   ├── app/          ← dependency wiring, server lifecycle
│   ├── domain/       ← shared types, sentinel errors (NO business logic)
│   ├── platform/     ← infrastructure (DB, config, crypto, mailer, middleware, logger, confidence)
│   │   ├── confidence/ ← ASR word alignment + deterministic confidence scoring
│   │   ├── config/
│   │   ├── crypto/
│   │   ├── db/
│   │   ├── logger/
│   │   ├── mailer/
│   │   └── middleware/
│   ├── auth/         ← authentication domain
│   ├── clinic/       ← clinic registration & management
│   └── staff/        ← staff management
├── migrations/       ← goose SQL migrations + embed.FS
└── docs/             ← this directory
```

---

## Domain package anatomy

Every domain package (`auth`, `clinic`, `staff`, ...) contains exactly four files:

| File | Responsibility |
|---|---|
| `handler.go` | HTTP only — parse request, call service, write response |
| `service.go` | Business logic only — orchestrates repo calls, enforces rules |
| `repository.go` | Database only — SQL queries, row scanning |
| `routes.go` | Route mounting only — `Mount(r chi.Router)` |

Supporting files (e.g. `tokens.go` inside `auth/`) are allowed when they contain logic that belongs clearly to that domain but doesn't fit a single file.

**Layer rules are hard constraints.** A service may never import `net/http`. A handler may never write SQL. A repository may never contain business logic. See `CLAUDE.md` in the repo root for the full enforcement rules.

---

## Request lifecycle

```
HTTP Request
    │
    ▼
Chi router  ──── Authenticate middleware (JWT validation)
    │
    ▼
huma operation ── permission middleware (Permissions check)
    │
    ▼
Handler  ──── parses huma input types
    │
    ▼
Service  ──── business logic, encryption, validation
    │
    ▼
Repository ── SQL via pgx/v5
    │
    ▼
PostgreSQL
```

Context values set by middleware (`clinicID`, `staffID`, `claims`) are read by handlers via typed helpers in `platform/middleware`.

---

## Multi-tenancy

Every tenant table has `clinic_id UUID NOT NULL REFERENCES clinics(id)`. All repository queries **must** filter by `clinic_id`. There is no global admin view — all data access is scoped to a clinic. A missing `clinic_id` in a WHERE clause is a multi-tenancy bug and will be caught by code review.

---

## Domain agnosticism

The `vertical` field on the `clinics` table (`veterinary`, `dental`, `general_clinic`, `aged_care`) drives vertical-specific behaviour. Core modules (`timeline`, `forms`, `compliance`) contain no vertical-specific code. Vertical-specific logic satisfies interfaces defined in `internal/domain/`. Adding a new vertical never requires modifying existing packages.

The `subjects` table is intentionally generic. Vertical-specific fields live in extension tables (`vet_subject_details`, etc.) joined at the service layer.

---

## Dependency injection

All dependencies flow through constructors. There are no global variables for mutable state. The wiring happens in `internal/app/app.go`. The pattern:

```go
// Each domain follows this constructor shape.
func NewService(repo repo, cipher *crypto.Cipher, ...) *Service

// Wired in app.go:
clinicRepo := clinic.NewRepository(db)
clinicSvc  := clinic.NewService(clinicRepo, cipher)
clinicH    := clinic.NewHandler(clinicSvc)
clinicH.Mount(r)
```

---

## Error handling

Errors are always wrapped with context:

```go
return nil, fmt.Errorf("auth.service.SendMagicLink: %w", err)
```

Sentinel errors in `internal/domain/errors.go` (`ErrNotFound`, `ErrConflict`, `ErrForbidden`, ...) are used for known business cases. Handlers map these to HTTP status codes via `errors.Is`. Raw DB errors never cross the repository boundary.

---

## API

All routes follow `/api/v1/{resource}`. The API is documented via **huma v2**, which auto-generates OpenAPI 3.1 from Go types. Swagger UI is available at `/docs` during development (`make docs`).

Breaking changes require a new version prefix (`/api/v2/`) — `/api/v1/` is never broken.

---

## Background jobs

PostgreSQL-backed job queue via **River** (no Redis dependency). Jobs are defined alongside the domain that owns them. The worker pool starts with the API server and shuts down gracefully.

---

## Key third-party dependencies

| Package | Purpose |
|---|---|
| `jackc/pgx/v5` | PostgreSQL driver |
| `pressly/goose/v3` | SQL migrations |
| `danielgtaylor/huma/v2` | OpenAPI + HTTP routing adapter |
| `go-chi/chi/v5` | HTTP router |
| `golang-jwt/jwt/v5` | JWT signing/verification |
| `google/uuid` | UUIDv4/v7 generation |
| `riverqueue/river` | Background job queue |
| `testcontainers-go` | Real PostgreSQL in integration tests |
| `stretchr/testify` | Test assertions |
| `deepgram/deepgram-go-sdk/v3` | Deepgram Nova-3 Medical transcription |
| `google.golang.org/genai` | Gemini extraction + transcription (dev) |
| `openai/openai-go` | OpenAI GPT-4.1-mini extraction |
