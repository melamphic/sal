# Engineering Backlog

Items deferred or planned for future phases. Ordered by dependency and complexity.

---

## Completed (previously deferred)

These items were listed as backlog but are now implemented:

| Item | Delivered |
|---|---|
| Timeline & SSE notifications (`note_events`, subject/clinic audit, real-time SSE) | Phase 2 |
| Policy engine (block content, semver versioning, clause tagging, form links, retire) | Phase 2 |
| Compliance reports (query endpoints + async CSV export via River + S3) | Phase 2 |

---

## Pending: Policy Alignment Score on Notes

**Spec rule:** After review, the note shows what percentage of the linked form's policies are satisfied by the transcript + extracted field values.

**What's needed:**

- Add `policy_alignment_pct DECIMAL(5,2)` column to `notes` table.
- After note submission (or as a separate async River job), compute alignment:
  1. Load clause list from linked policy versions (via `policy_clauses`).
  2. Score each clause against note field values and transcript.
  3. Compute weighted score by parity: `high` clauses weighted 3×, `medium` 2×, `low` 1×.
- Store result on `notes.policy_alignment_pct`.
- Surface in `NoteResponse`.
- `RunPolicyCheck()` stub in `forms/service.go` should invoke the real scorer.

**Dependencies:** Policy engine ✅ (built). Requires scorer implementation in `extraction` or new `compliance` package.

---

## Pending: Deterministic Confidence Scoring

**Spec formula:**
```
evidence ∈ transcript
confidence = avg(word_confidence(evidence_words))
if confidence < min_confidence → reject
```

**Current state:** Gemini returns a confidence float stored as-is. This is AI-estimated, not deterministic.

**What's needed:**

- Store Deepgram word-level confidence data in `recordings` table:
  ```sql
  ALTER TABLE recordings ADD COLUMN word_confidences JSONB;
  -- array of {word, start, end, confidence} from Deepgram response
  ```
- In `ExtractNoteWorker`, after receiving AI results:
  1. Find `source_quote` words in `word_confidences`.
  2. Average the word-level confidence scores for matched words.
  3. Apply `min_confidence` threshold (per-clinic or global config).
  4. Replace AI confidence with the computed value.
- Add `MinConfidence float64` field to extraction config or `FieldSpec`.

**Dependencies:** Deepgram word-confidence data must be persisted (currently discarded). Requires migration + changes to audio transcription worker.

---

## Pending: PDF/Doc Import → Policy Extraction

**Spec:** Clinics upload existing policy documents (PDF, Word). AI parses them into block-based policy content in the policy engine.

**What's needed:**

- Upload endpoint: `POST /api/v1/policies/import` — accepts multipart PDF/DOCX.
- Store file temporarily in S3, enqueue a River job.
- River job: extract text from document → call AI to structure it into AppFlowy blocks + identify clauses + assign parity levels → create a new policy draft with the result.
- Suitable for the add-on tier; gate behind a feature flag or plan check.

**Dependencies:** Policy engine ✅ (built). Requires document parsing library (e.g. `pdfcpu` for PDF).

---

## Pending: Policy RAG Chat

**Spec:** Staff can query the clinic's policies in natural language ("What is the protocol for dispensing controlled substances?").

**What's needed:**

- Vector embeddings of published policy clause content (pgvector or external vector DB).
- Embedding job triggered on each policy publish.
- Chat endpoint: accepts a question, retrieves top-K clauses by similarity, sends to AI with policy context, returns cited answer.
- Access gated by a new `query_policies` permission.

**Dependencies:** Policy engine ✅. Requires pgvector extension or external vector store.

---

## Pending: Marketplace Module

Not yet started. Clinics can discover and install form templates and policy packs published by Salvia and third parties.

See `salvia_specs.md` for requirements. No dependencies on current modules beyond auth and forms.

---

## Pending: Billing (Phase 3)

- Stripe customer + subscription lifecycle
- Usage caps per plan tier (note quota, staff seats)
- Webhook handler for subscription events
- `NoteTier` enforcement (standard / nurse / none) already modelled in `domain` — needs usage counting
