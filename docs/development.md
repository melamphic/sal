# Development Guide

## Prerequisites

- Go 1.23+
- Docker (for local services and integration tests)
- `golangci-lint` v1.60+
- `goose` CLI (optional — `make migrate` uses the embedded binary)

---

## First-time setup

```bash
# Clone the repo
git clone <repo-url> melamphic/sal
cd melamphic/sal

# Copy environment template
cp .env.example .env
# Edit .env — at minimum set ENCRYPTION_KEY and JWT_SECRET

# Start local services (Postgres, Mailpit, MinIO)
make dev-deps

# Run migrations
make migrate

# Start the API
make dev
```

The API starts on `http://localhost:8080`. Swagger UI is at `http://localhost:8080/docs`.

---

## Local services

`docker-compose.yml` provides:

| Service | Port | Purpose |
|---|---|---|
| PostgreSQL 17 | `5432` | Primary database |
| Mailpit | `8025` (UI), `1025` (SMTP) | Catches outbound email in development |
| MinIO | `9000` (API), `9001` (console) | S3-compatible audio file storage |

```bash
make dev-deps   # start all services
make dev-stop   # stop all services
```

---

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | Yes | PostgreSQL DSN |
| `ENCRYPTION_KEY` | Yes | 32-byte hex AES key for PII encryption |
| `JWT_SECRET` | Yes | HMAC secret for JWT signing (min 32 bytes) |
| `APP_URL` | Yes | Public URL for magic link emails |
| `SMTP_HOST` | No | SMTP host (default: `localhost`) |
| `SMTP_PORT` | No | SMTP port (default: `1025`) |
| `SMTP_FROM` | No | From address (default: `noreply@salvia.io`) |
| `PORT` | No | API listen port (default: `8080`) |
| `LOG_LEVEL` | No | `debug` / `info` / `warn` / `error` (default: `info`) |
| `JWT_ACCESS_TTL` | No | Access token lifetime (default: `15m`) |
| `JWT_REFRESH_TTL` | No | Refresh token lifetime (default: `720h`) |
| `MAGIC_LINK_TTL` | No | Magic link expiry (default: `15m`) |

Generate a valid encryption key:
```bash
openssl rand -hex 32
```

Generate a valid JWT secret:
```bash
openssl rand -base64 48
```

---

## Makefile reference

```bash
make dev          # start docker-compose services + run API with hot-reload
make build        # compile production binary → bin/api
make test         # unit tests only (fast, no Docker)
make test-integration  # all tests including repository tests (requires Docker)
make lint         # golangci-lint (same checks as CI)
make migrate      # apply pending goose migrations
make migrate-down # rollback one migration step
make docs         # serve MkDocs documentation site locally
make clean        # remove build artifacts
```

---

## Adding a new domain module

1. Create `internal/<module>/` with the four required files: `handler.go`, `service.go`, `repository.go`, `routes.go`
2. Define the `repo` interface in `internal/<module>/repo.go`
3. Add the migration in `migrations/<sequence>_create_<module>.sql`
4. Wire the module in `internal/app/app.go`:
   ```go
   myRepo := mymodule.NewRepository(db)
   mySvc  := mymodule.NewService(myRepo, cipher)
   myH    := mymodule.NewHandler(mySvc)
   myH.Mount(r)
   ```
5. Write unit tests with a `fakeRepo` (see `internal/clinic/fake_repo_test.go` as a template)
6. Write integration tests with `testutil.NewTestDB(t)` (see `internal/clinic/repository_integration_test.go`)

---

## Linting

`golangci-lint` runs locally and in CI. Run before pushing:

```bash
make lint
```

Key linters enforced:
- `errcheck` — no unchecked errors
- `govet` — standard Go vet
- `staticcheck` — advanced analysis
- `wrapcheck` — external errors must be wrapped
- `exhaustruct` — all struct fields initialised in constructors

A pre-commit hook runs lint automatically if configured.

---

## Debugging

### View emails in development

Open `http://localhost:8025` in a browser — Mailpit catches all SMTP traffic.

### View API docs

Open `http://localhost:8080/docs` — Swagger UI served by huma.

### Inspect the database

```bash
psql postgresql://salvia:salvia@localhost:5432/salvia
```

### View structured logs

The logger emits JSON in production and coloured text in development. Set `LOG_LEVEL=debug` to see all SQL queries and request details.
