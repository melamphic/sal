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
| Policy alignment score on notes (`policy_alignment_pct`, weighted by parity, Gemini) | Phase 2 |
| OpenAI extraction provider (strict JSON schema, GPT-4.1-mini) | Phase 2 |
| Gemini ResponseSchema + ThinkingBudget=0 (cost fix) | Phase 2 |
| GeminiTranscriber for dev/staging (replaces Deepgram in non-prod) | Phase 2 |
| RunPolicyCheck real LLM implementation (form coverage analysis) | Phase 2 |
| Factory functions for all providers (extractor, aligner, checker) | Phase 2 |
| Deterministic confidence scoring (ASR word alignment, LCS fuzzy match, inference penalty, requires_review) | Phase 2 |
| Per-field inference controls (`allow_inference`, `min_confidence` on `form_fields`) | Phase 2 |

---

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

## Pending: FCM Push Notifications

**Spec:** Mobile users need background push when extraction completes, policy updates, etc.

**What's needed:**

- Firebase Cloud Messaging integration for iOS + Android.
- Notification preferences per staff member (opt-in/out per event type).
- Push triggers: extraction complete, policy published, note assigned for review.
- SSE covers web clients already — FCM is mobile-only.

**Dependencies:** Auth ✅, Staff ✅. Requires Firebase project + service account key.

---

## Pending: Weekly Email Digest

**Spec:** Every Monday, super admin receives a compliance snapshot email.

**What's needed:**

- River periodic job or cron trigger (Monday 08:00 clinic timezone).
- Digest builder: notes submitted this week vs last, completion rate, top policy violations, overdue reviews.
- Configurable recipient list (super admin + additional admins).
- New mailer template `SendWeeklyDigest`.

**Dependencies:** Reports ✅, Mailer ✅.

---

## Pending: Billing (Phase 3)

- Stripe customer + subscription lifecycle
- Usage caps per plan tier (note quota, staff seats)
- Webhook handler for subscription events
- `NoteTier` enforcement (standard / nurse / none) already modelled in `domain` — needs usage counting

---

## Pending: Multi-Vertical Support (Phase 2)

**Spec:** Dental (Salvia Smile) and Aged Care (Salvia Care) patient types.

**What's needed:**

- Remove hardcoded `domain.VerticalVeterinary` in patient handler — pull from clinic context.
- Dental/aged care metadata JSONB schemas alongside vet_subject_details.
- Domain-specific form templates per vertical.
- Schema already supports it (`vertical` on clinics + subjects). Code change only.

**Dependencies:** Patient ✅, Forms ✅.
