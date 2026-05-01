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

---

## Pending: Policy Engine UI gaps (UX audit, 2026-04-21)

Authoring, form-linking, and check-result visualisation are shipped. Remaining gaps
surfaced during the cross-vertical audit — each item is independent and can be
tackled on its own.

### Compliance dashboard (Flutter)

**Spec:** A clinic-wide view of policy health. "All notes failing policy X in the
last 30 days", violation heatmap by policy / by author, trend of alignment score
over time. Currently violations are only visible one note at a time.

**What's needed:**

- Backend: aggregate query endpoint `GET /api/v1/policies/violations` with
  filters (policy_id, author_id, date range, severity).
- Flutter: new `lib/features/compliance/` feature with list + filters + drilldown
  into note detail.
- Gate behind an admin/compliance-officer permission.

### Audit export UI (Flutter)

`generate_audit_export` permission exists and the backend produces CSVs, but
there is no Flutter UI to run it. Settings → Compliance page with date pickers,
policy multi-select, and a "Request export" button that polls the River job
status and offers a signed download link when ready.

### Admin-only gating on policy authoring (Flutter)

Policy list + editor are visible to every logged-in staff member. Per our UI
gating rule, non-admin staff should not see the "New policy" / edit / retire
affordances at all — hide the controls, don't just disable. Requires a
permission check against the signed-in staff's role.

### Vet-flavoured placeholder strings (Flutter)

`policies_list_page.dart:599` and `clauses_step.dart:209` still use "NSAID
contraindications" / "Verify allergies before NSAIDs" as hints. Swap to
vertical-neutral examples ("Consent documented before treatment", "Record
baseline observations") or rotate hints based on `clinic.vertical` if we want
to lean discipline-specific.

### Clause attachments / evidence (Flutter + Backend)

Clauses are text-only. Compliance teams often reference external standards —
allow a clause to link to a PDF/URL (legal reference, professional guideline,
regulatory citation). Backend: new `policy_clause_attachments` table; Flutter:
upload button on clause card + render in PDF/check drawer.

### Clause library / templates (Flutter + Backend)

Every policy rewrites clauses from scratch. Add a per-clinic "saved clauses"
library the editor can pull from; optionally seed Salvia-curated templates per
vertical (sterilisation, medication handling, consent, falls prevention).
Overlaps with the existing marketplace feature — a "clause pack" listing type
may be the right vehicle.

### "Superseded by" forward pointer on retired policies (Flutter + Backend)

When a policy is retired, there's no way to point staff at the replacement.
Add an optional `superseded_by_policy_id` on retirement; show "Replaced by X"
badge on the retired policy and in any audit trail that references it.

### Bulk retire (Flutter)

Policy list supports one-at-a-time retire only. Add multi-select + bulk retire
with a shared reason field — matters when a clinic re-structures its policy set.

### Override-modal at note submit (in progress, task #66)

Backend already accepts override-with-justification on high-parity violations
(task #63, shipped). UI to prompt the clinician at submit time is still pending —
without it the backend capability is unreachable.

---

## Pending: Compliance aggregator + register tabs (deferred from BUILD_PLAN Phase 2/3)

**Status:** Phase 0 stub UIs (`ComplianceInboxPage`, `RegistersPage`) were removed from
the Flutter app on 2026-05-01 — they had been empty scaffolds since they shipped. The
underlying IA goal stands (see `salvia/COMPLIANCE_IA.md` and `salvia/BUILD_PLAN.md`),
but the surfacing flipped: registers fold into existing Drugs / Incidents activities
as tabs, and the inbox folds into home-dashboard watchcards.

**Backend work needed before re-introducing the UI:**

- `GET /compliance/today` aggregator endpoint — returns counts + top-3 items per
  bucket (deadlines, witness pending, reconciliations due, expiring consents,
  missing ACDs aged-care-only, near-misses awaiting review)
- Per-bucket list endpoints behind the watchcards' "see all" deep links —
  these largely already exist (incidents list, drug ops list, consents list)
  but need filters: `?has_deadline=true&hours_remaining_lt=N`, `?awaiting_witness=true`,
  `?reconciliation_overdue=true`, `?expires_within_days=30`
- CD register reconciliation export: regulator-format CSV/PDF per (vertical, country) —
  ship NZ + UK first, AU per-state later. New endpoint
  `POST /drugs/reconciliation/export` driven by River job + S3, similar to
  the existing audit pack export.
- Incident register de-identification + regulator-format export — similar pattern.

**Why deferred:** the inline capture (system widgets) is what generates regulator-
binding rows in the first place; that shipped in 2026-04. The aggregator + export
formats are cheaper to build now that the data shape is real, and there's no
external pressure (no clinic has hit a regulator deadline yet) to ship before
real customers tell us which bucket they need first.

**Dependencies:** existing `consent_records`, `drug_operations_log`, `incident_events`
tables ✅. Per-vertical / per-country regulator export formats need a small spike
to confirm shape (CQC SIRS PDF, VCNZ format, etc).
