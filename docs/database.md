# Database

`sal` uses **PostgreSQL 17** exclusively. There is no other data store.

---

## Migrations

Migrations are managed with **goose v3** and live in `/migrations/`. They are embedded into the binary at compile time via `//go:embed *.sql` in `migrations/migrations.go`.

### File naming

```
00001_create_clinics.sql
00002_create_staff.sql
00003_create_auth_tokens.sql
```

5-digit zero-padded sequence followed by a descriptive snake_case name. Every file has both directions:

```sql
-- +goose Up
CREATE TABLE ...;

-- +goose Down
DROP TABLE ...;
```

**Never modify a committed migration.** Always add a new file.

### Running migrations

```bash
make migrate        # apply pending migrations
make migrate-down   # rollback one step
```

In development, migrations run automatically at startup. In production, they run via the Makefile target before deploying.

---

## Schema conventions

| Convention | Rule |
|---|---|
| Primary keys | `UUID` (UUIDv7 — time-ordered for index locality) |
| Timestamps | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` in UTC |
| Soft deletes | `archived_at TIMESTAMPTZ NULL` — NULL means active |
| Tenant scoping | `clinic_id UUID NOT NULL REFERENCES clinics(id)` on every tenant table |
| String enums | `VARCHAR CHECK (col IN ('a','b'))` — never PostgreSQL `ENUM` type (ENUM requires migration to add values) |
| PII columns | Documented with `-- PII: encrypted` comment; values encrypted by Go before insert |

---

## PII / PHI encryption

Sensitive fields (email, phone, full name, address) are encrypted with **AES-256-GCM** in Go before the value is written to the database. The database never sees plaintext PII.

For equality lookups (e.g. "does this email already exist?"), an **HMAC-SHA256 hash** of the normalised value is stored alongside:

```
clinics.email       TEXT  -- AES-256-GCM ciphertext, base64 encoded
clinics.email_hash  TEXT  -- HMAC-SHA256 of lowercased+trimmed email
```

Key operations live in `internal/platform/crypto`. The AES key comes from the `ENCRYPTION_KEY` environment variable (32-byte hex-encoded).

---

## Core tables

### `clinics`

The root tenant table. Every other tenant table references this via `clinic_id`.

```sql
id              UUID PRIMARY KEY
name            TEXT NOT NULL
slug            VARCHAR(60) NOT NULL UNIQUE
email           TEXT NOT NULL          -- PII: encrypted
email_hash      TEXT NOT NULL UNIQUE   -- for deduplication lookups
phone           TEXT                   -- PII: encrypted, nullable
address         TEXT                   -- PII: encrypted, nullable
vertical        VARCHAR NOT NULL       -- 'veterinary' | 'dental' | 'aged_care'
status          VARCHAR NOT NULL       -- 'trial' | 'active' | 'suspended' | 'cancelled'
trial_ends_at   TIMESTAMPTZ NOT NULL
note_cap        INT                    -- NULL = unlimited
note_count      INT NOT NULL DEFAULT 0
data_region     VARCHAR NOT NULL       -- e.g. 'ap-southeast-2'
archived_at     TIMESTAMPTZ            -- NULL = active
```

### `staff`

One row per staff member per clinic. A person can have accounts in multiple clinics.

```sql
id              UUID PRIMARY KEY
clinic_id       UUID NOT NULL REFERENCES clinics(id)
email           TEXT NOT NULL          -- PII: encrypted
email_hash      TEXT NOT NULL          -- for login lookup
full_name       TEXT NOT NULL          -- PII: encrypted
role            VARCHAR NOT NULL       -- 'super_admin' | 'vet' | 'nurse' | 'receptionist'
note_tier       VARCHAR NOT NULL       -- 'standard' | 'advanced'
perm_*          BOOLEAN NOT NULL       -- individual permission flags (11 columns)
status          VARCHAR NOT NULL       -- 'active' | 'deactivated'
last_active_at  TIMESTAMPTZ
archived_at     TIMESTAMPTZ
```

### `auth_tokens`

Stores hashed magic link and refresh tokens.

```sql
id                UUID PRIMARY KEY
staff_id          UUID NOT NULL REFERENCES staff(id)
token_hash        TEXT NOT NULL UNIQUE   -- SHA-256 of raw token
token_type        VARCHAR NOT NULL       -- 'magic_link' | 'refresh'
expires_at        TIMESTAMPTZ NOT NULL
used_at           TIMESTAMPTZ            -- NULL = unused (single-use)
created_from_ip   TEXT
```

---

## Indexes

Key indexes beyond primary keys:

```sql
CREATE UNIQUE INDEX ON clinics (email_hash);
CREATE UNIQUE INDEX ON clinics (slug);
CREATE INDEX ON staff (clinic_id, email_hash) WHERE archived_at IS NULL;
CREATE INDEX ON staff (clinic_id) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX ON auth_tokens (token_hash);
CREATE INDEX ON auth_tokens (staff_id, token_type) WHERE used_at IS NULL;
```

---

## Connection pooling

`pgxpool.Pool` is used throughout — one pool per application process, shared across all request goroutines. Pool configuration:

| Setting | Value |
|---|---|
| `MaxConns` | `25` |
| `MinConns` | `2` |
| `MaxConnLifetime` | `1h` |
| `HealthCheckPeriod` | `1m` |

Configured in `internal/platform/db/db.go`.

---

### `recordings` (additional columns — migration 00008, 00017)

| Column | Type | Notes |
|---|---|---|
| `transcript` | TEXT? | Plain-text transcript from ASR provider |
| `duration_seconds` | INTEGER? | Audio duration from Deepgram metadata |
| `word_confidences` | JSONB? | Deepgram word array `[{word, start, end, confidence, punctuated_word, ...}]`; NULL for GeminiTranscriber |

### `note_fields` (additional columns — migration 00010, 00011, 00017)

| Column | Type | Notes |
|---|---|---|
| `confidence` | DECIMAL(5,2)? | LLM-estimated confidence (fallback when no ASR data) |
| `source_quote` | TEXT? | Verbatim transcript snippet cited by LLM |
| `transformation_type` | VARCHAR(20)? | `"direct"` or `"inference"` |
| `asr_confidence` | DECIMAL(5,4)? | Mean ASR word confidence for matched quote span |
| `min_word_confidence` | DECIMAL(5,4)? | Minimum word confidence — review trigger |
| `alignment_score` | DECIMAL(5,4)? | Quote-to-transcript match quality (1.0=exact) |
| `grounding_source` | VARCHAR(20)? | `"exact"` / `"fuzzy"` / `"ungrounded"` / `"no_asr_data"` |
| `requires_review` | BOOLEAN | true when grounding_source=`"ungrounded"` |

---

## Transactions

Repository methods that require atomicity accept no `tx` parameter — they manage their own transaction internally. The one exception is `GetAndConsumeAuthToken`, which uses `FOR UPDATE` to prevent concurrent token consumption.

Multi-step operations that span multiple repositories are orchestrated in the service layer by calling repository methods sequentially; true cross-repo transactions are not yet needed in Phase 0.
