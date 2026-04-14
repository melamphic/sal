# Compliance Reports

The reports module provides compliance officers and clinic administrators with queryable audit views over the `note_events` table, plus asynchronous CSV export via a River background job.

All report endpoints require the `generate_audit_export` permission.

---

## Report types

| Report | Description | Key filter |
|---|---|---|
| **Clinical audit** | All note events for the clinic | Date range, staff, subject |
| **Staff actions** | All events performed by a specific staff member | `staff_id` (required) |
| **Note history** | Full event trail for a single note, oldest first | `note_id` (path param) |
| **Consent log** | All `note.submitted` events — the sign-off record | Date range, subject |

All reports query the `note_events` table only — no joins to other tables. The consent log uses `event_type = 'note.submitted'` to isolate submission events; `actor_id` is the reviewing staff member and `occurred_at` is the submission timestamp.

---

## Synchronous query endpoints

Return paginated JSON results suitable for in-app display. Max 100 rows per page.

### Clinical audit

```http
GET /api/v1/reports/clinical-audit
Authorization: Bearer <token>

?from=2026-01-01T00:00:00Z
&to=2026-03-31T23:59:59Z
&staff_id=<uuid>        # optional
&subject_id=<uuid>      # optional
&limit=20
&offset=0
```

### Staff actions

```http
GET /api/v1/reports/staff-actions
Authorization: Bearer <token>

?staff_id=<uuid>        # required
&from=...&to=...
&limit=20&offset=0
```

### Note history

```http
GET /api/v1/reports/note-history/{note_id}
Authorization: Bearer <token>
```

Returns the complete event trail for the note (no pagination — note history is always small).

### Consent log

```http
GET /api/v1/reports/consent-log
Authorization: Bearer <token>

?from=...&to=...
&subject_id=<uuid>      # optional
&limit=20&offset=0
```

---

## Async CSV export

For full-dataset exports (audits, regulatory submissions), use the async export flow:

### 1. Request export

```http
POST /api/v1/reports/export
Authorization: Bearer <token>
Content-Type: application/json

{
  "report_type": "clinical_audit",
  "format": "csv",
  "filters": {
    "from": "2026-01-01T00:00:00Z",
    "to": "2026-03-31T23:59:59Z",
    "staff_id": "<uuid>",       // optional
    "subject_id": "<uuid>",     // optional
    "note_id": "<uuid>"         // required for note_history type
  }
}
```

Returns `202 Accepted` with a job record:

```json
{
  "id": "<job_uuid>",
  "report_type": "clinical_audit",
  "format": "csv",
  "status": "pending",
  "created_at": "2026-04-14T10:00:00Z"
}
```

### 2. Poll for completion

```http
GET /api/v1/reports/export/{job_id}
Authorization: Bearer <token>
```

| Status | Meaning |
|---|---|
| `pending` | Job is queued, not yet started |
| `complete` | CSV ready; `download_url` contains a presigned S3 URL (valid 1 hour) |
| `failed` | Generation failed; `error_msg` explains why |

When `status=complete`:

```json
{
  "id": "<job_uuid>",
  "status": "complete",
  "download_url": "https://s3.../reports/<clinic_id>/<job_id>.csv?X-Amz-Expires=3600...",
  "completed_at": "2026-04-14T10:00:42Z"
}
```

The `download_url` is a **fresh presigned URL** generated on every GET — it is never stored in the database. The S3 key (`reports/{clinic_id}/{job_id}.csv`) is stored and used to generate the URL on demand.

---

## CSV format

All report types produce the same column layout:

| Column | Description |
|---|---|
| `occurred_at` | RFC3339 UTC timestamp |
| `event_type` | Event type string |
| `note_id` | UUID of the note |
| `subject_id` | UUID of the subject (blank if not linked) |
| `actor_id` | UUID of the staff member |
| `actor_role` | Role at the time of the event |
| `field_id` | UUID of the changed field (blank if not a field_changed event) |
| `old_value` | Previous value (blank if not applicable) |
| `new_value` | New value (blank if not applicable) |
| `reason` | Staff-provided reason (blank if not provided) |

Max rows per export: **50,000**. Larger exports should use date range filters to split into multiple jobs.

---

## Export job flow

```
POST /api/v1/reports/export
  → InsertReportJob (status=pending)
  → river.Insert(GenerateReportArgs)
  → return job record

GenerateReportWorker.Work()
  → fetchAll (queries note_events, max 50k rows)
  → writeCSV (in-memory buffer)
  → store.Upload(key="reports/{clinic_id}/{job_id}.csv")
  → repo.MarkComplete(key)

GET /api/v1/reports/export/{job_id}
  → GetReportJob
  → if complete: store.PresignDownload(key, 1 hour)
  → return job + download_url
```

---

## Database

Migration: `00013_create_report_jobs.sql`

```sql
CREATE TABLE report_jobs (
    id           UUID PRIMARY KEY,
    clinic_id    UUID NOT NULL,
    report_type  TEXT NOT NULL,
    format       TEXT NOT NULL DEFAULT 'csv',
    status       TEXT NOT NULL DEFAULT 'pending',
    filters      JSONB,
    storage_key  TEXT,          -- set on completion; used to generate presigned URL
    error_msg    TEXT,
    created_by   UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
```

## Endpoint summary

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/reports/clinical-audit` | Paginated clinical audit |
| `GET` | `/api/v1/reports/staff-actions` | Paginated staff actions (staff_id required) |
| `GET` | `/api/v1/reports/note-history/{note_id}` | Full note event trail |
| `GET` | `/api/v1/reports/consent-log` | Paginated consent/submission log |
| `POST` | `/api/v1/reports/export` | Request async CSV export (202) |
| `GET` | `/api/v1/reports/export/{job_id}` | Poll export status + get download URL |
