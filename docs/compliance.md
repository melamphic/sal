# Compliance

`sal` is designed to meet **HIPAA**, **GDPR**, and **SOC 2 Type II** requirements from day one. This page documents the technical controls in place.

---

## PII / PHI classification

| Field | Classification | Storage |
|---|---|---|
| Email address | PII | AES-256-GCM encrypted + HMAC hash |
| Phone number | PII | AES-256-GCM encrypted |
| Physical address | PII | AES-256-GCM encrypted |
| Full name | PII | AES-256-GCM encrypted |
| Patient records | PHI | AES-256-GCM encrypted (Phase 1+) |
| Audio recordings | PHI | Encrypted at rest in object storage (Phase 1+) |
| Clinic name, slug | Non-sensitive | Plaintext |
| UUIDs, timestamps | Non-sensitive | Plaintext |

---

## Encryption

### At-rest (application layer)

All PII/PHI is encrypted with **AES-256-GCM** before being written to the database. The database never holds plaintext sensitive data. Key material comes from the `ENCRYPTION_KEY` environment variable and is never committed to source control.

Rationale for application-layer encryption over pgcrypto:
- Key rotation does not require DB migration — decrypt old, re-encrypt new, write back
- Backups are safe to store unencrypted (data is already ciphertext)
- Works identically across all environments and database versions

### In-transit

All external communication uses TLS. Internal service-to-service calls (when applicable) also use TLS. Plain HTTP is only permitted on `localhost` in development.

### Key management

- Development: fixed test key in environment
- Production: keys stored in AWS Secrets Manager / GCP Secret Manager (TBD at deployment phase)
- Key rotation: supported by design — the `Cipher` struct can be initialized with multiple keys for decryption while encrypting with the latest

---

## GDPR controls

| Requirement | Implementation |
|---|---|
| Right to erasure | `archived_at` soft-delete + GDPR anonymisation job (replaces PII with tombstone values, retains non-PII for legal records) |
| Data portability | Export endpoint returns decrypted PII in structured JSON (Phase 1+) |
| Consent records | Consent timestamps stored with subject records |
| Data minimisation | Only fields explicitly required are collected |
| Breach notification | Audit log provides full access history for 7-year retention |
| DPA agreements | Vendor checklist maintained in `docs/vendor-dpa.md` |

**Deletion policy:** GDPR erasure does NOT hard-delete rows. Instead, all PII fields are overwritten with deterministic tombstone values and `archived_at` is set. Non-PII (UUIDs, timestamps, vertical metadata) is retained for 7 years to satisfy medical record retention laws.

---

## HIPAA controls

| Safeguard | Implementation |
|---|---|
| Access control | JWT with embedded permissions; per-route `RequirePermission` middleware |
| Audit controls | `access_logs` table records every PHI access (resource type, resource ID, staff ID, clinic ID, timestamp) |
| Integrity | AES-GCM provides authenticated encryption — any tampering is detected on decryption |
| Transmission security | TLS required in production |
| Workforce training | Documented policies in `docs/` |
| Business Associate Agreements | Required for all vendors handling PHI |

---

## SOC 2 Type II controls

| Trust criterion | Control |
|---|---|
| Security | Authentication, authorisation, encryption, vulnerability scanning |
| Availability | Health checks, graceful shutdown, connection pooling |
| Confidentiality | PII encrypted, audit logs, access control |
| Processing integrity | Error handling, validation, idempotent operations |
| Privacy | GDPR controls above |

---

## Audit logging

### Clinical note event trail (`note_events`)

Every mutation to a clinical note is recorded in the `note_events` table:

```sql
CREATE TABLE note_events (
    id          UUID PRIMARY KEY,
    note_id     UUID NOT NULL REFERENCES notes(id),
    clinic_id   UUID NOT NULL REFERENCES clinics(id),   -- denormalised for tenant scoping
    subject_id  UUID,                                   -- denormalised for subject timeline
    event_type  TEXT NOT NULL,  -- 'note.created' | 'note.field_changed' | 'note.submitted' | ...
    actor_id    UUID NOT NULL,  -- staff who triggered the event
    actor_role  TEXT NOT NULL,  -- role snapshotted at event time
    field_id    UUID,           -- set for field_changed events
    old_value   JSONB,          -- previous value (field_changed only)
    new_value   JSONB,          -- new value (field_changed only)
    reason      TEXT,           -- optional staff-provided reason
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Events are **immutable and append-only**. Actor role is snapshotted so the record is accurate even if the staff member's role changes later. Old and new values are stored as JSON, never as plaintext PII — only field IDs and values the staff explicitly entered are captured.

A PostgreSQL trigger fires `pg_notify('salvia_note_events', ...)` on every insert, driving real-time SSE delivery to connected clients.

### Compliance report exports

The `reports` module provides queryable audit views and async CSV export:

| Report | What it covers |
|---|---|
| **Clinical audit** | All note events for the clinic in a date range |
| **Staff actions** | All events performed by a specific staff member |
| **Note history** | Full event trail for a single note (oldest first) |
| **Consent log** | All `note.submitted` events (sign-off records) |

Exports are generated as CSV, uploaded to S3, and retrieved via presigned URLs valid for 1 hour. All export endpoints require the `generate_audit_export` permission.

See [Reports](reports.md) for endpoint details.

---

## Data residency

The `data_region` field on `clinics` records which AWS region the clinic's data is stored in. Currently all data is in `ap-southeast-2` (Sydney). Future multi-region support will route data to the appropriate region pool.

---

## Incident response

1. Detect — audit logs + alerting (Grafana/PagerDuty, Phase 2+)
2. Contain — revoke all tokens for affected staff via `DeleteRefreshTokensForStaff`
3. Assess — export audit log for affected resources
4. Notify — GDPR requires notification within 72 hours of breach discovery
5. Remediate — key rotation procedure documented separately
