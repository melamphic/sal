# AI Drafts

The `internal/aidrafts` module orchestrates **audio → transcribe → AI →
prefilled fields** for any target domain that wants AI assistance
without hand-rolling its own pipeline. The clinician records audio,
asks for a draft, and reviews the prefilled fields in the modal — the
AI never auto-applies values to the target domain.

Today the module ships drafts for **incidents** and **consent**.
Pain and pre-encounter brief target_types are reserved in the schema
and ready to wire when the corresponding aigen services land.

**Drugs are deliberately excluded** — regulator stakes are too high to
surface AI-suggested values on a controlled-drugs register; the drugs
module stays manual by design.

---

## Pipeline

```
client uploads audio   → POST /api/v1/recordings (existing)
                       → enqueues TranscribeAudioWorker
client requests draft  → POST /api/v1/ai-drafts { target_type, recording_id, context_payload? }
                       → ai_drafts row created (status=pending_transcript)
                       → if transcript already done: enqueue ExtractAIDraftArgs immediately

TranscribeAudioWorker saves transcript
  → fans out to TranscriptListeners
      ├── notes.Service             — for any extracting notes on this recording
      └── aidrafts.Service          — finds pending_transcript drafts on this recording
                                       → enqueues ExtractAIDraftArgs (UniqueOpts dedupes)

ExtractAIDraftWorker.Work()
  → load draft (no clinic scope — internal)
  → if status is done|failed: idempotent return
  → if recording_id missing: mark failed, return
  → fetch transcript via audio.Repository.GetTranscript
  → if transcript empty: river.JobSnoozeError{3s} (backstop for missed listener fan-out)
  → mark extracting
  → resolve clinic vertical/country/tier via aigen clinic lookup
  → dispatch by target_type:
        incident → aigen.IncidentDraftService
        consent  → aigen.ConsentDraftService
  → mark done with draft_payload + AI provenance (provider/model/prompt_hash)
```

The Flutter modal polls `GET /api/v1/ai-drafts/{id}` every ~1.5 s until
status flips to `done` (or `failed`), then parses `draft_payload` and
populates the target form fields. The clinician reviews + edits + hits
**Submit**, which goes through the regular create-incident /
create-consent endpoint — SIRS/CQC classification, witness rules, and
all other regulator decisions still run on the FINAL committed values.

---

## Status flow

```
pending_transcript → extracting → done | failed
```

| Status | Meaning |
|---|---|
| `pending_transcript` | Draft row inserted; transcript not yet on the recording row. The audio listener will fire `ExtractAIDraftArgs` when transcription completes. |
| `extracting` | Worker has the transcript and is calling the aigen service. |
| `done` | `draft_payload` is populated. Client reads it, clinician reviews, submits via the regular create endpoint. |
| `failed` | `error_message` carries the cause (provider error, missing recording, unsupported target_type). |

---

## Idempotency + race safety

Two enqueue paths can fire `ExtractAIDraftArgs` for the same draft:
1. `aidrafts.Service.CreateDraft` enqueues immediately when the
   transcript is already on the recording row (e.g. user requests a
   draft from an old recording).
2. `audio.TranscribeAudioWorker` listener fan-out enqueues when a
   fresh transcript lands.

`river.InsertOpts.UniqueOpts{ByArgs: true}` collapses both paths to a
single in-flight job per `(kind, DraftID)`. Same pattern the notes
module uses (see `docs/notes.md` for the race-fix history).

The worker itself is also idempotent: if it loads a draft already in
`done` or `failed`, it returns nil immediately rather than running
extraction again.

---

## Target types

| target_type | aigen service | context_payload shape |
|---|---|---|
| `incident` | `aigen.IncidentDraftService` | (none — transcript IS the input) |
| `consent` | `aigen.ConsentDraftService` | `{ procedure, consent_type, audience }` JSON. When `procedure` is empty the worker falls back to using the transcript itself, but the UI should encourage the clinician to provide explicit context. |
| `pain` | reserved | TBD |
| `pre_encounter_brief` | reserved | TBD |

Adding a new target_type is two changes:
1. Implement an aigen service that takes a transcript + ClinicContext
   and returns structured fields with `AIMetadata`.
2. Add a `case` in `ExtractAIDraftWorker.Work()` to dispatch.

---

## Database

Migration: `00064_create_ai_drafts.sql`. One table:

```sql
CREATE TABLE ai_drafts (
    id              UUID PRIMARY KEY,
    clinic_id       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL CHECK (target_type IN
                        ('incident','consent','pain','pre_encounter_brief')),
    recording_id    UUID REFERENCES recordings(id),
    context_payload JSONB,                    -- per-target structured input
    draft_payload   JSONB,                    -- AI output, shape matches GeneratedXxxDraft
    status          TEXT NOT NULL DEFAULT 'pending_transcript'
                        CHECK (status IN ('pending_transcript','extracting','done','failed')),
    error_message   TEXT,
    ai_provider     TEXT,
    ai_model        TEXT,
    prompt_hash     TEXT,
    requested_by    UUID NOT NULL REFERENCES staff(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
```

A partial index `ai_drafts_recording_idx WHERE status = 'pending_transcript'`
makes the audio-listener fan-out cheap.

---

## API surface

```
POST /api/v1/ai-drafts                 (manage_patients)
  body: { target_type, recording_id, context_payload? }
  → 202 Accepted with the queued draft row

GET  /api/v1/ai-drafts/{id}            (manage_patients)
  → draft row; poll until status is done | failed
```

There is no `apply` endpoint by design — clients submit reviewed values
through the regular create-incident / create-consent endpoint so the
domain modules' validation, classification, and audit logging always run
on the final values.

---

## What's not done yet

- Pain target_type — needs `aigen.PainScoreDraftService` (single-field
  extraction: NRS score 0–10 from a phrase like "she rated it 7 today").
- Pre-encounter brief target_type — needs an aigen service that pulls
  recent notes + drug ledger + pain trend for the subject and produces
  a one-paragraph summary. Reserved in the schema; no service yet.
- Drafts older than 30 days could be GC'd to keep the table small —
  currently no retention sweep.
