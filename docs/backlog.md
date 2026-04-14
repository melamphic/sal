# Engineering Backlog

Items deferred from the April 2026 audit. Ordered by dependency and complexity.

---

## Phase 2: Timeline & Metamorphosis History

**Spec rule:** Notes appear in a subject's profile with a full audit trail — when the recording was made, when the note was reviewed, when it was submitted, and every field value change in between.

**What's needed:**

- New table `note_events` (or `note_audit_log`):
  ```sql
  CREATE TABLE note_events (
      id          UUID PRIMARY KEY,
      note_id     UUID NOT NULL REFERENCES notes(id),
      event_type  VARCHAR NOT NULL, -- 'created', 'field_changed', 'reviewed', 'submitted', 'archived'
      field_id    UUID REFERENCES form_fields(id), -- set for field_changed events
      old_value   TEXT,  -- JSON-encoded previous value
      new_value   TEXT,  -- JSON-encoded new value
      actor_id    UUID NOT NULL REFERENCES staff(id),
      created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  ```
- Emit events from `notes/service.go` on every state transition and field override.
- New endpoint: `GET /api/v1/notes/{note_id}/timeline` — paginated event log.
- Subject timeline endpoint: `GET /api/v1/subjects/{subject_id}/timeline` — all notes and events for a patient, primary sort by recording time.
- Real-time push: websocket or SSE so the frontend updates live.

**Dependencies:** None — can be built standalone.

---

## Phase 2: Policy Alignment Score

**Spec rule:** After review, the note shows what percentage of the linked form's policies are satisfied by the transcript + extracted values.

**What's needed:**

- Policy engine must be built first (currently `forms.RunPolicyCheck()` is a stub returning placeholder text).
- Add `policy_alignment_pct DECIMAL(5,2)` column to `notes` table.
- After submission (or as a separate async job), run policy check against the note's field values and transcript.
- Store result on `notes.policy_alignment_pct`.
- Surface in `NoteResponse`.

**Dependencies:** Policy engine (clause enforcement, scoring logic). Blocked until policy engine is functional.

---

## Phase 3: Deterministic Confidence Scoring

**Spec formula:**
```
evidence ∈ transcript
confidence = avg(word_confidence(evidence_words))
if confidence < min_confidence → reject
```

**Current state:** Gemini returns a confidence float which is stored as-is. This is AI-guessed, not deterministic.

**What's needed:**

- Deepgram's transcription response already includes word-level confidence scores. Store these in the `recordings` table (currently only the transcript text is stored).
- New column: `recordings.word_confidences JSONB` — array of `{word, start, end, confidence}` objects from Deepgram.
- In `ExtractNoteWorker`, after receiving AI results, compute deterministic confidence:
  1. For each field result, find `source_quote` words in `word_confidences`.
  2. Average the word-level confidence scores for matched words.
  3. Apply `min_confidence` threshold (configurable per clinic or global).
  4. Replace AI-provided confidence with the computed value.
- Add `MinConfidence float64` to `extraction.FieldSpec` or pass as a global config.

**Dependencies:** Deepgram word-confidence data must be persisted (currently discarded). Requires migration + changes to audio transcription worker.

---

## Notes: Subject Timeline Integration

**Spec:** The subject profile shows a chronological timeline of all notes, sorted by recording time (not note creation time). Staff can drill into any note to see its full metamorphosis.

**What's needed:**

- `GET /api/v1/subjects/{subject_id}/timeline` endpoint returning notes (and eventually policies, appointments) sorted by `recordings.created_at` (the recording time), not `notes.created_at`.
- Archived notes hidden unless `include_archived=true`.
- Pagination.
- Requires timeline events table (Phase 2) for full metamorphosis detail.

**Dependencies:** Phase 2 timeline table.

---

## Policy Engine

**Current state:** `form_policies` is a link table with no target (no `policies` table). `RunPolicyCheck()` returns placeholder text.

**What's needed:**

- `policies` table with block-based content (Appflowy-style: blocks stored as JSONB).
- Policy versioning (same semver pattern as forms).
- Clause marking: blocks can be tagged `high`/`medium`/`low` enforcement level.
- Import flow: upload PDF/doc → AI extracts policy clauses → creates policy blocks.
- Policy-to-form compliance check: given a form version and its fields, compute what % of linked policy clauses are addressed.
- RAG chat (later): staff can query org policies via natural language.

**Dependencies:** None — standalone module. Enables policy alignment on notes.

---

## Marketplace Module

Not yet started. See `salvia_specs.md` for requirements. No dependencies on current modules beyond auth.
