# Audio Module

The `internal/audio` package handles the full lifecycle of a clinical audio recording — from the moment a vet taps **Record** on the mobile app through to a transcribed text that feeds into AI form extraction.

---

## Design Principles

**Audio never passes through the API server.**
The server hands the mobile client a pre-signed URL and steps aside. The client uploads the file directly to object storage (MinIO in dev, Cloudflare R2 or AWS S3 in prod). This keeps the API server stateless and avoids memory pressure from large file uploads.

**Transcription is always asynchronous.**
After the client confirms the upload, a [River](https://riverqueue.com) background job calls the configured transcription provider. The client polls `GET /api/v1/recordings/{id}` or (in future) subscribes to an SSE topic to receive the status update.

**Transcription provider is swappable.**
`TRANSCRIPTION_PROVIDER=deepgram` (default, production) uses Deepgram Nova-3 Medical and stores word-level confidence data. `TRANSCRIPTION_PROVIDER=gemini` uses Gemini's audio understanding — free tier, no Deepgram account needed, but no word-level confidence data (deterministic scoring is skipped for those recordings).

---

## Upload Flow

```
Mobile app                  API server                 Object storage
─────────                   ──────────                 ──────────────
POST /recordings          →  create row (pending_upload)
                          ←  { recording, upload_url }

PUT {upload_url} ──────────────────────────────────────→  file stored

POST /recordings/{id}/confirm-upload
                          →  status = uploaded
                             enqueue TranscribeAudio job
                          ←  { recording }

(River worker fires)
                          →  status = transcribing
                             GET pre-signed download URL
                             POST to Deepgram Nova-3 Medical
                          ←  transcript stored
                             status = transcribed
                             fan-out to TranscriptListeners
```

---

## TranscriptListener fan-out

After the transcript is persisted, `TranscribeAudioWorker` invokes every
registered `audio.TranscriptListener` synchronously. This is the
mechanism that lets downstream modules react the instant a transcript
lands rather than polling or guessing at a delay.

```go
// audio/jobs.go
type TranscriptListener interface {
    OnRecordingTranscribed(ctx context.Context, recordingID uuid.UUID) error
}
```

Listeners registered today (via `app.go`):

| Module | Implementation | What it does |
|---|---|---|
| `notes` | `notes.Service.OnRecordingTranscribed` | Re-enqueues `ExtractNoteArgs` for every note in `extracting` status bound to this recording (UniqueOpts dedupes against the immediate enqueue from CreateNote) |
| `aidrafts` | `aidrafts.Service.OnRecordingTranscribed` | Enqueues `ExtractAIDraftArgs` for every draft in `pending_transcript` status bound to this recording (incidents + consent today; pain + pre_encounter_brief reserved) |

Listener errors are swallowed inside the audio worker — the transcript
is the load-bearing side effect. Each listener owns its own retry path
via River's exponential backoff. Adding a new module to the fan-out is
two changes: implement the interface in the new module, register it in
`app.go::audio.NewTranscribeAudioWorker(..., yourListener)`.

This pattern replaces an earlier `river.InsertOpts.ScheduledAt: now+8s`
hack in `notes.Service.CreateNote` that guessed at "transcribe usually
finishes in under 8 s." The current design is event-driven (listener)
with a deterministic retry backstop (worker uses
`rivertype.JobSnoozeError{Duration: 3*time.Second}` when transcript
still isn't ready).

---

## Recording Statuses

| Status | Meaning |
|---|---|
| `pending_upload` | Row created; client has not yet uploaded the file. |
| `uploaded` | Client confirmed upload; transcription job enqueued. |
| `transcribing` | River job is actively calling Deepgram. |
| `transcribed` | Transcript is stored and ready for AI extraction. |
| `failed` | All retries exhausted. `error_message` holds the last error. |

---

## API Endpoints

All endpoints require a valid JWT with `record_audio` permission.

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/recordings` | Create a recording and get a pre-signed upload URL. |
| `GET` | `/api/v1/recordings` | List recordings. Filters: `subject_id`, `staff_id`, `status`. |
| `GET` | `/api/v1/recordings/{id}` | Get recording metadata and current status. |
| `POST` | `/api/v1/recordings/{id}/confirm-upload` | Confirm upload complete; triggers transcription. |
| `GET` | `/api/v1/recordings/{id}/download-url` | Get a 1-hour pre-signed GET URL for audio playback. |
| `PATCH` | `/api/v1/recordings/{id}/subject` | Link a patient to a recording created without one. |

Full request/response schemas are in the Swagger UI at `/docs`.

---

## Object Storage

Storage is abstracted behind `internal/platform/storage.Store` using the AWS SDK v2. The same code runs against all S3-compatible backends — only env vars differ.

### Transcription provider env vars

| Env var | Values | Default |
|---|---|---|
| `TRANSCRIPTION_PROVIDER` | `deepgram` \| `gemini` | `deepgram` |
| `DEEPGRAM_API_KEY` | Deepgram API key | _(empty = skip transcription)_ |
| `GEMINI_API_KEY` | Google AI Studio key | _(empty = skip transcription)_ |

---

## Object Storage

| Env var | Dev value | Prod value |
|---|---|---|
| `STORAGE_ENDPOINT` | `http://localhost:9000` | Cloudflare R2 / AWS S3 endpoint |
| `STORAGE_BUCKET` | `salvia-audio` | your bucket name |
| `STORAGE_ACCESS_KEY` | `minioadmin` | IAM / R2 API token |
| `STORAGE_SECRET_KEY` | `minioadmin` | IAM / R2 secret |
| `STORAGE_REGION` | `ap-southeast-2` | your region |
| `STORAGE_USE_PATH_STYLE` | `true` | `false` for AWS S3 |

**File key format:** `clinics/{clinicID}/recordings/{recordingID}.{ext}`

UUIDs in the path ensure no PII is embedded in storage paths. The key is never exposed to the client — only the signed URL is returned.

### Supported content types

The client declares the content type at recording creation time. Allowed values:

```
audio/mp4   (iOS default — .m4a)
audio/m4a
audio/mpeg  (.mp3)
audio/webm  (Android Chrome)
audio/ogg
audio/wav
```

---

## Background Jobs (River)

River is a PostgreSQL-backed job queue — no Redis, no additional infrastructure. Jobs are persisted in the same database as application data, making enqueue + status update atomic.

### TranscribeAudio

**Kind:** `transcribe_audio`

**Triggered by:** `POST /recordings/{id}/confirm-upload`

**Payload:**
```json
{ "recording_id": "uuid" }
```

**Steps:**
1. Sets status → `transcribing`.
2. Generates a 1-hour pre-signed download URL for the stored audio file.
3. Calls the configured transcription provider:
   - **Deepgram** (`TRANSCRIPTION_PROVIDER=deepgram`): passes the pre-signed URL directly to Nova-3 Medical with `punctuate`, `diarize`, `utterances`. Receives a full word-level confidence array alongside the transcript.
   - **Gemini** (`TRANSCRIPTION_PROVIDER=gemini`): downloads audio bytes from the pre-signed URL, passes inline to Gemini 2.5 Flash. Returns transcript only — no word-level data.
4. Stores plain-text transcript, duration, and (Deepgram only) `word_confidences JSONB` on the recording row.
5. Sets status → `transcribed`.

On error: sets status → `failed` with `error_message`. River retries with exponential backoff (up to 25 attempts, ~4 days total window).

**Dev without any API key:** If the configured provider's key is empty, the worker runs but skips the transcription call. This lets you develop the pipeline locally with no paid keys.

### Worker configuration

Workers start and stop with the HTTP server (see `cmd/api/main.go`). The default queue runs 10 concurrent workers:

```go
river.QueueConfig{MaxWorkers: 10}
```

Increase this in production if transcription throughput requires it.

---

## River Migrations

River manages its own database tables (`river_job`, `river_queue`, etc.) separately from application migrations. They run automatically on startup via `rivermigrate.Migrate` in `internal/app/db.go` — no manual step needed.

---

## Adding a New Job Type

1. Define `type MyJobArgs struct { ... }` with a `Kind() string` method.
2. Implement `type MyWorker struct { river.WorkerDefaults[MyJobArgs] }` with a `Work` method.
3. Register with `river.AddWorker(workers, NewMyWorker(...))` in `app.Build`.
4. Enqueue with `riverClient.Insert(ctx, MyJobArgs{...}, nil)`.

---

## Testing

Unit tests use an in-memory fake repo and a `fakeStore` that returns predictable URLs without hitting storage. A `fakeEnqueuer` records `Insert` calls so tests can assert jobs were enqueued.

```
make test                # unit tests — no Docker needed
make test-integration    # full DB tests — requires Docker
```

Integration tests are in `repository_integration_test.go` with the `//go:build integration` tag. They run against a real Postgres container (testcontainers-go) and test the full SQL layer including status transitions and FK constraints.
