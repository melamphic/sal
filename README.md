# sal — Salvia Backend API

The Go backend for **Salvia**, a voice-first AI documentation and compliance platform for veterinary clinics (and future verticals: dental, aged care).

- **Language:** Go 1.23+
- **Database:** PostgreSQL 17
- **Architecture:** Modularised monolith — one binary, bounded internal packages
- **Compliance:** HIPAA / GDPR / SOC 2 by design

> **Engineering docs** → run `make docs` and open [http://localhost:8001](http://localhost:8001)  
> **API docs (Swagger)** → start the server then open [http://localhost:8080/docs](http://localhost:8080/docs)

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Local Services](#local-services)
- [Makefile Reference](#makefile-reference)
- [Project Structure](#project-structure)
- [Running Tests](#running-tests)
- [Database Migrations](#database-migrations)
- [Code Generation](#code-generation)
- [Linting](#linting)
- [Documentation Site](#documentation-site)

---

## Prerequisites

Install these before starting:

| Tool | Version | Install |
|---|---|---|
| Go | 1.23+ | [go.dev/doc/install](https://go.dev/doc/install) |
| Docker | any recent | [docs.docker.com/get-docker](https://docs.docker.com/get-docker/) |
| Docker Compose | v2+ | bundled with Docker Desktop |
| golangci-lint | 1.60+ | `brew install golangci-lint` or [install guide](https://golangci-lint.run/usage/install/) |

---

## Quick Start

```bash
# 1. Clone
git clone <repo-url> sal && cd sal

# 2. Create your local env file
cp .env.example .env

# 3. Generate secrets and paste them into .env
make gen-key          # → ENCRYPTION_KEY
make gen-jwt-secret   # → JWT_SECRET

# 4. Start Postgres, Mailpit, MinIO
make infra

# 5. Run database migrations
make migrate

# 6. Start the API server
make dev
```

The API is now running at **http://localhost:8080**.

---

## Configuration

All configuration comes from environment variables. Copy `.env.example` to `.env` and fill in the required values:

```bash
cp .env.example .env
```

### Required variables

| Variable | How to generate |
|---|---|
| `ENCRYPTION_KEY` | `make gen-key` (32-byte base64 AES key) |
| `JWT_SECRET` | `make gen-jwt-secret` (64-byte hex HMAC secret) |
| `DATABASE_URL` | Defaults to local Docker Postgres — no change needed for dev |
| `APP_URL` | Base URL for magic link emails, e.g. `http://localhost:3000` |

### Optional variables (defaults shown)

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | API listen port |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `JWT_ACCESS_TTL` | `15m` | Access token lifetime |
| `JWT_REFRESH_TTL` | `720h` | Refresh token lifetime (30 days) |
| `MAGIC_LINK_TTL` | `15m` | Magic link expiry window |
| `SMTP_HOST` | `localhost` | SMTP server (Mailpit in dev) |
| `SMTP_PORT` | `1025` | SMTP port |
| `SMTP_FROM` | `noreply@salvia.io` | Sender address |

### /mel handoff + billing

Paid signups start on the static `/mel` marketing site. When a visitor completes
Stripe Checkout, `/mel` POSTs to **`POST /api/v1/auth/signup/start`** which
mints a short-lived HS256 JWT and returns `HandoffURL` — the browser is then
redirected to `APP_URL/auth/handoff?token=…`. That second endpoint
provisions the clinic + super-admin staff row and issues a real session.

| Variable | Purpose |
|---|---|
| `MEL_HANDOFF_JWT_SECRET` | HS256 shared secret for the handoff JWT. When unset, `/api/v1/auth/signup/start` returns 401. |
| `STRIPE_WEBHOOK_SECRET` | Signing secret for `POST /api/v1/billing/webhook`. When unset, the webhook route is not mounted. |
| `STRIPE_API_KEY` | `sk_test_…` / `sk_live_…` key used to open Stripe customer-portal sessions from `POST /api/v1/billing/portal`. When unset, the portal endpoint returns 400. |
| `STRIPE_PRICE_MAP` | Comma-separated `price_id=plan_code` pairs, e.g. `price_abc=paws_pro_monthly,price_def=paws_pro_annual`. Used by the webhook to resolve `plan_code` on subscription events. |

To run the full 3-service stack locally:

```bash
# terminal 1 — backend
cd sal && make dev

# terminal 2 — /mel marketing site
cd mel && npm install && npm run dev     # http://localhost:5173

# terminal 3 — Salvia app (Flutter)
cd salvia/apps && flutter pub get && flutter run -d chrome --web-port 3000
```

> **Never commit `.env`** — it is in `.gitignore`. Secrets are never stored in source control.

---

## Local Services

Docker Compose starts three supporting services:

| Service | URL | Purpose |
|---|---|---|
| PostgreSQL 17 | `localhost:5432` | Primary database |
| Mailpit | [http://localhost:8025](http://localhost:8025) | Email inbox — catches all outgoing email in dev |
| MinIO | [http://localhost:9001](http://localhost:9001) | S3-compatible storage console (API on `:9000`) |

```bash
make infra        # start all services
make infra-down   # stop all services
```

---

## Makefile Reference

```bash
# ── Dev ────────────────────────────────────────────────────────────────────
make dev              # start Docker services + API server
make infra            # start Docker services only
make infra-down       # stop Docker services

# ── Build ──────────────────────────────────────────────────────────────────
make build            # compile → bin/api

# ── Tests ──────────────────────────────────────────────────────────────────
make test             # unit tests only (fast, no Docker)
make test-integration # unit + integration tests (requires Docker)

# ── Code quality ───────────────────────────────────────────────────────────
make lint             # golangci-lint
make fmt              # gofmt + goimports

# ── Database ───────────────────────────────────────────────────────────────
make migrate          # apply pending migrations
make migrate-down     # rollback one step
make migrate-status   # show migration state

make tidy             # go mod tidy

# ── Docs ───────────────────────────────────────────────────────────────────
make docs-install     # pip3 install mkdocs-material
make docs             # serve engineering docs at http://localhost:8001
make docs-api         # open Swagger UI (API must be running)

# ── Helpers ────────────────────────────────────────────────────────────────
make gen-key          # generate ENCRYPTION_KEY value
make gen-jwt-secret   # generate JWT_SECRET value
```

---

## Project Structure

```
sal/
├── cmd/api/              ← entry point (main.go)
├── internal/
│   ├── app/              ← dependency wiring, server lifecycle
│   ├── domain/           ← shared types, sentinel errors (no business logic)
│   ├── platform/         ← infrastructure
│   │   ├── config/       ← env-based configuration
│   │   ├── crypto/       ← AES-256-GCM encryption + HMAC hashing
│   │   ├── db/           ← pgxpool setup
│   │   ├── logger/       ← structured JSON logger
│   │   ├── mailer/       ← email abstraction
│   │   └── middleware/   ← JWT auth + permission middleware
│   ├── auth/             ← magic link auth, JWT, refresh tokens
│   ├── clinic/           ← clinic registration and management
│   ├── staff/            ← staff invitations, roles, permissions
│   ├── patient/          ← subjects (patients/animals/residents) and contacts
│   ├── audio/            ← recording upload, Deepgram transcription via River job
│   ├── forms/            ← form builder, versioning, policy links, PDF style
│   ├── notes/            ← AI extraction pipeline, field override, submission
│   ├── extraction/       ← AI provider abstraction (Gemini, OpenAI stub)
│   ├── timeline/         ← note event log, subject/clinic audit timelines
│   ├── notifications/    ← SSE broker backed by PostgreSQL LISTEN/NOTIFY
│   ├── policy/           ← policy engine, versioning, clauses, form linking
│   └── reports/          ← compliance reports, async CSV export via River + S3
├── migrations/           ← goose SQL migrations (embedded in binary)
├── docs/                 ← MkDocs engineering documentation
├── mkdocs.yml            ← MkDocs configuration
├── requirements-docs.txt ← Python deps for documentation site
├── docker-compose.yml
├── Makefile
├── go.mod
└── CLAUDE.md             ← coding rules enforced by AI and CI
```

### Domain package anatomy

Every domain package has exactly four files:

```
internal/<module>/
  handler.go      ← HTTP only
  service.go      ← business logic only
  repository.go   ← database only
  routes.go       ← route mounting only
```

Extra files within a package are fine for domain-specific helpers (e.g. `notes/jobs.go` for the River worker). Cross-domain imports are never allowed — modules communicate through interfaces wired in `app.go`.

---

## Running Tests

### Unit tests

Fast, no Docker required. Run on every save:

```bash
make test
# equivalent: go test -count=1 -race ./...
```

Packages covered: `platform/crypto`, `auth`, `clinic`, `staff`.

### Integration tests

Test repository methods against a real PostgreSQL instance (started automatically by testcontainers-go):

```bash
make test-integration
# equivalent: go test -count=1 -race -tags=integration ./...
```

Packages covered: `auth`, `clinic`, `staff` repositories.

### Test naming convention

```
Test{Type}_{Method}_{Scenario}

TestService_Register_DuplicateEmail_ReturnsConflict
TestRepository_GetByID_WrongClinic_ReturnsNotFound
TestGenerateSlug_SpecialCharacters
```

---

## Database Migrations

Migrations use **goose v3** and are embedded into the binary. Files live in `/migrations/`.

```bash
make migrate          # apply all pending migrations
make migrate-down     # rollback one step
make migrate-status   # show which migrations have run
```

### Adding a migration

```bash
# Create a new migration file
touch migrations/00004_create_subjects.sql
```

Every migration requires both directions:

```sql
-- +goose Up
CREATE TABLE subjects (...);

-- +goose Down
DROP TABLE subjects;
```

**Never modify a committed migration.** Always add a new file.

---

## Linting

```bash
make lint
```

golangci-lint runs the following checks (configured in `.golangci.yml`):

- `errcheck` — no unchecked errors
- `govet` — standard Go vet
- `staticcheck` — advanced static analysis
- `wrapcheck` — all external errors must be wrapped
- `exhaustruct` — all struct fields initialised in constructors
- `revive` — style rules including exported doc comments

A pre-commit hook can be configured to run lint automatically. **CI blocks merges on lint failures.**

---

## Documentation Site

Engineering documentation is built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/).

```bash
# One-time setup
make docs-install

# Serve locally at http://localhost:8001
make docs
```

Documentation pages:

| Page | Content |
|---|---|
| Architecture | Layer rules, request lifecycle, multi-tenancy |
| Development | Setup guide, env vars, adding modules |
| Database | Migrations, schema conventions, PII encryption |
| Testing | Unit vs integration, test harness, helpers |
| Auth | Magic link flow, token types, security properties |
| Compliance | HIPAA/GDPR/SOC 2 controls, audit logging |

---

## Security

- PII/PHI is encrypted with AES-256-GCM **before** hitting the database — the DB never holds plaintext
- Passwords do not exist — passwordless magic link auth only
- All tokens are stored as SHA-256 hashes, never raw
- Every route declares required permissions — no implicit access
- See [docs/compliance.md](docs/compliance.md) for the full compliance posture

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## License

Salvia is Source-Available software. You are free to view, modify, and self-host this project for personal or internal business use. However, **Competitive Use is prohibited**—you may not offer Salvia as a managed or hosted service to third parties.

See the [LICENSE](LICENSE) file for the full text of the PolyForm Shield License 1.0.0.
