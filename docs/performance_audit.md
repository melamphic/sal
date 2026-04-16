# Sal Backend — Performance & Correctness Audit

**Branch:** `feat/performance-audit`  
**Date:** 2026-04-16  
**Scope:** Full backend audit — performance, cost, correctness, safety, edge conditions.

---

## Summary

| Severity | Count | Fixed |
|----------|-------|-------|
| Critical | 4     | ✅    |
| High     | 5     | ✅    |
| Medium   | 4     | ✅    |
| Low      | 2     | ✅    |

---

## Critical

### C-1 — HTTP server has no timeouts

**File:** `internal/app/app.go:266-275`

`http.Server` is created with only `Addr` and `Handler`. No `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`. A slow or malicious client can hold a connection open indefinitely, exhausting the goroutine pool and eventually the DB connection pool.

**Fix:** Set `ReadTimeout: 30s`, `WriteTimeout: 60s`, `IdleTimeout: 120s`.

---

### C-2 — DB connection pool not tuned

**File:** `internal/app/db.go:17-31`

`pgxpool.ParseConfig` + `pgxpool.NewWithConfig` with zero configuration. pgx defaults `MaxConns` to `max(4, numCPU)` — typically 4–8 on a small VM. Under sustained load, all 10 River workers (plus HTTP handlers) compete for ≤8 connections, causing queuing and timeouts.

**Fix:** Configure `MaxConns=30`, `MinConns=2`, `MaxConnLifetime=30m`, `MaxConnIdleTime=5m`. Expose as env vars `DB_MAX_CONNS`, `DB_MIN_CONNS`.

---

### C-3 — N+1 query in `policyClauseProviderAdapter.GetClausesForNote`

**File:** `internal/app/app.go:411-444`

For a note with N linked policies, this issues `2 + 2N` queries:
1. `GetVersionByID(formVersionID)` → formID
2. `ListLinkedPolicies(formID)` → policyIDs
3. For each policy: `GetLatestPublishedVersion(pid)` + `ListClauses(pv.ID)`

Called by `ComputePolicyAlignmentWorker` for every note that completes extraction. 10 linked policies = 22 queries per note.

**Fix:** Add `GetLatestClausesForPolicies(ctx, []uuid.UUID)` to `policy.Repository` using a CTE that fetches all latest published versions + clauses in one query. Reduces to 3 queries regardless of policy count.

---

### C-4 — N+1 query in `formPolicyClauseFetcherAdapter.GetClausesForForm`

**File:** `internal/app/app.go:454-481`

Same pattern as C-3 — called during `forms.Service.CheckPolicy`. Issues `1 + 2N` queries.

**Fix:** Same `GetLatestClausesForPolicies` method. Reduces to 2 queries.

---

## High

### H-1 — Presigned URL expires before River can retry

**File:** `internal/audio/jobs.go:14`

`downloadTTLForJob = 1 * time.Hour`. River's default backoff reaches ~1 hour by retry 4–5 (exponential: 1m, 5m, 20m, 1h…). If transcription fails at retry 4, the presigned URL from the original job attempt has expired — subsequent retries always get a 403 and the job is stuck.

**Fix:** Set TTL to `6 * time.Hour`. Covers up to retry 6 (~3h cumulative backoff) with margin.

---

### H-2 — `GetWordConfidences` error silently swallowed

**File:** `internal/notes/jobs.go:176-178`

```go
if wc, wcErr := w.recording.GetWordConfidences(ctx, *note.RecordingID); wcErr == nil {
    wordIndex = wc
}
```

An actual DB error is treated identically to "no word data available". All fields get `GroundingSource = "no_asr_data"` confidence scores even when the data exists but was unavailable due to a transient error — silently producing lower-quality extraction results.

**Fix:** Log the error and return it (fail the job so River retries).

---

### H-3 — SSE event channel buffer too small

**File:** `internal/notifications/broker.go:79`

`ch = make(chan Event, 16)`. During a burst (e.g. bulk note import), `fanOut` silently drops events for slow/catching-up clients. Clients miss real-time updates and must poll or refresh.

**Fix:** Increase buffer to `64`. For high-volume clinics this covers ~4s of events at 16 events/s without dropping.

---

### H-4 — Health check does not ping DB

**File:** `internal/app/app.go:259-264`

`/health` always returns `{"status":"ok"}` regardless of DB state. Load balancers and orchestrators (ECS, k8s) use this endpoint to decide whether to route traffic. A dead DB connection silently passes health checks.

**Fix:** `db.Ping(ctx)` inside the health handler; return `503` on failure.

---

### H-5 — `DetachPolicyFromForms` issues N individual deletes

**File:** `internal/app/app.go:488-499`

Called when a policy is retired. Loops over form IDs and calls `UnlinkPolicy` individually. With many linked forms this is an N-query serial loop inside a single request.

**Fix:** Replace with a single `DELETE FROM form_policies WHERE policy_id = $1` query. No need to list IDs first.

---

## Medium

### M-1 — Report worker loads 50 000 rows into memory

**File:** `internal/reports/jobs.go:87-112`

`fetchAll` fetches up to `maxRows = 50_000` records into a `[]*AuditEventRecord` slice, then streams them through `bytes.Buffer` before uploading to S3. At ~500 bytes/row that's 25 MB per report job peak RSS. Concurrent report jobs multiply this.

**Fix:** Stream rows directly to an `io.PipeWriter` connected to a multipart S3 upload, or process in pages of 1 000. The simplest safe fix is pagination: fetch 1 000 rows at a time, write each batch to the CSV writer before fetching the next.

---

### M-2 — River jobs have no deduplication

**File:** `internal/notes/jobs.go:140-141`, `309-310`

`ComputePolicyAlignmentArgs` jobs are enqueued with `nil` opts. If a note is re-extracted (manual retry), two alignment jobs run concurrently for the same note — both write `UpdatePolicyAlignment`, last write wins. Harmless in practice but wastes AI tokens and DB writes.

**Fix:** Pass `&river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}` so River deduplicates by note ID.

---

### M-3 — No pool config in environment

**File:** `internal/platform/config/config.go`

Pool tuning from C-2 requires env vars. Without them, changing pool size requires a code deploy.

**Fix:** Add `DBMaxConns`, `DBMinConns` to `Config` with sensible defaults (`30`, `2`).

---

### M-4 — `ReplaceClauses` uses individual INSERT loop

**File:** `internal/policy/repository.go:493-498`

Clauses are inserted one-by-one inside a transaction. A form with 20 clauses = 20 round-trips inside one TX. pgx supports batch queries.

**Fix:** Use `pgx.Batch` to send all INSERTs in a single round-trip.

---

## Low

### L-1 — No request body size limit

**File:** `internal/app/app.go:219-232`

Chi router has no `MaxBytesReader` middleware. A large POST body (e.g. malformed audio upload, crafted JSON) will be fully buffered in memory before handlers reject it.

**Fix:** Add `chimw.RequestSize(8 * 1024 * 1024)` (8 MB) to the middleware chain. Audio uploads go directly to S3 presigned URLs so this does not affect recordings.

---

### L-2 — River worker count can exceed pool connections

**File:** `internal/app/app.go:180-184`

`MaxWorkers: 10` with a DB pool defaulting to `max(4, numCPU)` connections. River workers that hit the DB concurrently queue on the pool. After C-2 fix (pool=30) this is no longer a concern, but the relationship should be documented.

**Note:** Resolved by C-2.

---

## Non-issues (investigated, no action needed)

- `idx_policy_clauses_version_id` — exists in `00014_create_policies.sql:98`. No missing index.
- CORS config — `AllowCredentials: false` is correct; JWT is in `Authorization` header, not cookies.
- River retry policy — 25 retries with exponential backoff is appropriate for external AI providers.
- `uuid.Nil` in internal workers — intentional bypass of tenant scoping for system-level jobs; documented in code.
- Graceful shutdown order — Broker → River → HTTP → DB is correct.
