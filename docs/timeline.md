# Timeline & Notifications

The timeline module records every meaningful event on a clinical note and exposes three audit views: per-note history, per-subject history, and a clinic-wide audit log. Real-time delivery is handled by the notifications module via server-sent events (SSE).

---

## Note events

Every mutation to a note emits a `NoteEvent` that is written to `note_events` and delivered in real-time via SSE. Events are immutable — the table is append-only.

### Event types

| Event type | When |
|---|---|
| `note.created` | Note row inserted |
| `note.field_changed` | Staff overrides an extracted field value |
| `note.submitted` | Staff reviews and submits the note |
| `note.archived` | Note is archived with a reason |
| `note.extraction_complete` | River job finished AI extraction successfully |
| `note.extraction_failed` | River job exhausted retries without a result |

### Event record

| Field | Description |
|---|---|
| `event_type` | One of the types above |
| `actor_id` | Staff UUID who triggered the event (or note creator for system events) |
| `actor_role` | Role snapshotted at event time — accurate even if role changes later |
| `field_id` | Non-null for `field_changed` events; references the specific form field |
| `old_value` | Previous field value as JSON (field_changed only) |
| `new_value` | New field value as JSON (field_changed only) |
| `reason` | Optional staff-provided reason (used on archive) |
| `subject_id` | Denormalised from the note for efficient subject timeline queries |
| `clinic_id` | Denormalised for multi-tenant query scoping |
| `occurred_at` | Timestamp of the event |

---

## Cross-module event emission

The `notes` package defines an `EventEmitter` interface; the concrete implementation is wired in `app.go` via `timelineEventAdapter` which calls `timeline.Repository.InsertNoteEvent`. Errors from event emission are logged but never propagated — note operations succeed even if timeline writing fails.

The `ExtractNoteWorker` (River job) also emits events with `actor_role = "system"`.

---

## Real-time delivery: SSE broker

The notifications module maintains a broker that:

1. Acquires a dedicated PostgreSQL connection and issues `LISTEN salvia_note_events`.
2. On `NOTIFY`, parses the payload (`clinic_id:event_id:note_id:event_type`) and fans out to all SSE clients subscribed to that `clinic_id`.
3. Auto-reconnects with a 5-second backoff on connection loss.
4. Drops events to slow clients (non-blocking channel send) rather than blocking the broker.

Clients connect once and receive a stream of JSON events:

```
GET /api/v1/events
Authorization: Bearer <token>

: connected

data: {"event_id":"...","note_id":"...","event_type":"note.field_changed","occurred_at":"..."}

data: {"event_id":"...","note_id":"...","event_type":"note.submitted","occurred_at":"..."}
```

---

## Timeline endpoints

All endpoints require `SubmitForms` permission (read access to notes). The clinic audit log additionally requires `GenerateAuditExport`.

### Note timeline

Full event history for a single note, oldest first.

```http
GET /api/v1/notes/{note_id}/timeline
Authorization: Bearer <token>
```

Query parameters: `limit` (default 20, max 100), `offset`.

### Subject timeline

All note events for a subject across all notes, newest first. Useful for a patient's medical history view.

```http
GET /api/v1/subjects/{subject_id}/timeline
Authorization: Bearer <token>
```

Query parameters: `limit`, `offset`.

### Clinic audit log

All note events for the clinic in reverse chronological order. Supports date range and staff filter.

```http
GET /api/v1/timeline
Authorization: Bearer <token>
```

Query parameters: `limit`, `offset`, `from` (RFC3339), `to` (RFC3339), `staff_id`.

---

## Endpoint summary

| Method | Path | Permission | Description |
|---|---|---|---|
| `GET` | `/api/v1/notes/{note_id}/timeline` | SubmitForms | Event trail for a note |
| `GET` | `/api/v1/subjects/{subject_id}/timeline` | SubmitForms | All events for a subject |
| `GET` | `/api/v1/timeline` | GenerateAuditExport | Clinic-wide audit log |
| `GET` | `/api/v1/events` | (any authenticated) | SSE stream for real-time updates |

---

## Database

### `note_events`

Migration: `00012_create_note_events.sql`

Indexes:
- `(clinic_id, occurred_at DESC)` — clinic audit log
- `(note_id, occurred_at ASC)` — note history (oldest first)
- `(subject_id) WHERE subject_id IS NOT NULL` — subject timeline
- `(actor_id, occurred_at DESC)` — staff actions filter

A `BEFORE INSERT` trigger calls `pg_notify('salvia_note_events', clinic_id::text || ':' || id::text || ':' || note_id::text || ':' || event_type)` on every insert.
