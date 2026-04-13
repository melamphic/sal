# Salvia Backend

`sal` is the Go backend for **Salvia** — a voice-first AI documentation and compliance platform for veterinary clinics (and future verticals: dental, aged care).

---

## What's built (Phase 0)

| Module | What it does |
|---|---|
| **platform/crypto** | AES-256-GCM encryption + HMAC-SHA256 hashing for PII/PHI |
| **platform/middleware** | JWT authentication + per-permission authorisation for Chi + huma |
| **platform/mailer** | Email abstraction (Resend in prod, Mailpit in dev) |
| **auth** | Passwordless magic link login, JWT access tokens, opaque refresh tokens |
| **clinic** | Clinic registration, profile management, multi-tenancy foundation |
| **staff** | Staff invitations, role-based permissions, deactivation |

---

## Architecture at a glance

- **Modularised monolith** — one binary, bounded internal packages, split-to-services later if needed
- **PostgreSQL only** — no Redis, no secondary store in Phase 0
- **AES-256-GCM at the app layer** — PII encrypted before it hits the DB
- **huma v2** — auto-generated OpenAPI 3.1 + Swagger UI from Go types
- **HIPAA / GDPR / SOC 2** — designed in from day one, not retrofitted

See [Architecture](architecture.md) for the full picture.

---

## Quick start

```bash
cp .env.example .env   # set ENCRYPTION_KEY and JWT_SECRET
make dev-deps          # start Postgres + Mailpit + MinIO
make migrate           # run migrations
make dev               # start API on :8080
```

Swagger UI → [http://localhost:8080/docs](http://localhost:8080/docs)  
Email UI → [http://localhost:8025](http://localhost:8025)

See [Development](development.md) for the full setup guide.

---

## Running tests

```bash
make test                # unit tests — seconds, no Docker
make test-integration    # repository tests — requires Docker
make lint                # golangci-lint
```

See [Testing](testing.md) for how the test harness works.

---

## Delivery phases

| Phase | Scope |
|---|---|
| **0 — Foundation** ✅ | Auth, clinic, staff, PII encryption, compliance baseline |
| 1 — Core workflow | Subjects, audio ingestion, AI transcription, notes |
| 2 — Intelligence | AI note generation, templates, smart suggestions |
| 3 — Compliance | Audit export, GDPR tools, retention policies |
| 4 — Billing | Stripe integration, usage caps, plan management |
| 5 — Growth | Multi-vertical, marketplace, SSO |

---

## Key decisions

- **No passwords** — magic link only reduces credential risk surface
- **Application-layer encryption** — key rotation without DB migrations; backups safe by default
- **`vertical` field** — single codebase supports multiple clinic types without forking
- **River for jobs** — PostgreSQL-backed queue; no Redis operational overhead
- **testcontainers** — integration tests run against real Postgres, not mocks
