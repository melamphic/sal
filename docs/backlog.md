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

---

## Pending: Reports v2 — vertical-specific reports (deferred from V1, 2026-05-04)

**Context.** V1 of the HTML+Gotenberg report rebuild ships **6 universal reports**
(audit pack, CD register, incident report, CD reconciliation, pain trend, MAR grid)
that work across vet · dental · GP / general clinic · aged care. The items below
are vertical-specific reports we identified during the per-(vertical, country)
research but explicitly punted because they need new schema or new capture widgets
that don't exist yet.

Sandbox HTML mockups for the V1 visual spec live at
`~/work/melamphic/reports-mockups/index.html`.

### Dental

- **Sterilisation / decontamination cycle log** — UK HTM 01-05, AU AS/NZS 4815/4187,
  NZ infection control. **Gap:** no `sterilisation_cycles` table. Needs cycle id,
  autoclave id, biological indicator result, operator, date.
- **Odontogram + perio chart** in clinical record — all countries. **Gap:** no
  system widget; clinical note can hold prose but not a structured tooth chart.
- **PDMP query log** (US — CA CURES, TX PMP, NY I-STOP, others state-specific)
  required before prescribing Sch II–IV. **Gap:** no `pdmp_queries` table.

### General clinic / GP / Family Medicine

- **Clinical audit cycle artifact** (UK CQC Reg 17 + AU RACGP Std 5th + NZ RNZCGP
  Cornerstone CQI) — standards-vs-actual + intervention + remeasure. **Gap:** no
  `audit_cycles` entity; events exist but no cycle wrapper.
- **Significant Event Analysis log aggregator** (UK GP + UK dental + NZ GP) —
  periodic SEA digest distinct from generic incident report. **Gap:** small —
  uses `incident_events` filtered to a "lessons learned" SEA view; ~1 day, no
  schema change. Could be promoted to V1.5 cheaply.
- **QOF achievement scorecard** (UK NHS GMS) — annual indicator-level performance.
  **Gap:** no eligibility + indicator tracking schema.
- **MIPS Quality measures** (US CMS QPP) — annual eCQM aggregation. **Gap:** no
  eCQM tracking.
- **Cornerstone Equity module evidence** (NZ — demographic-stratified outcomes
  by ethnicity). **Gap:** subjects table doesn't have structured demographics.

### Vet (vertical-specific, beyond CD register / audit pack)

- **Cascade prescribing log** (UK VMD reg) — written justification per Cascade
  use, retain 5 yrs. **Gap:** notes hold narrative; no Cascade-specific
  field/widget.
- **ACVM record / Veterinary Operating Instructions** (NZ MPI) — formatted to MPI
  VOI template. **Gap:** links exist, format doesn't match template.
- **Anaesthesia / surgical monitoring time-series** (UK RCVS PSS · AU ASAVA · US
  AAHA) — periodic vital-sign capture during procedures. **Gap:** no time-series
  capture widget.
- **Adverse drug event report (ADRS)** (NZ MPI portal) — structured form mirroring
  portal schema. **Gap:** `incident_events` is generic; not ADRS-shaped.

### Aged care (beyond V1's MAR)

- **Restrictive practices / restraint / DoLS register** (AU Quality Standards · UK
  MCA/DoLS · NZ Ngā Paerewa). AU adds behaviour-support-plan + consent linkage.
  **Gap:** no register table.
- **Per-jurisdiction regulator submission with classification + deadline timer**
  (AU SIRS · NZ HQSC SAC · UK NHS LFPSE · UK CQC Reg 18 statutory notifications).
  **Gap:** `incident_events` is generic; needs per-jurisdiction classifier +
  deadline clock + submission lineage with regulator reference number tracking.
- **MDS 3.0 resident assessments** (US LTC mandatory) — admission, quarterly,
  annual, significant change. **Gap:** no MDS schema.
- **National Quality Indicators (NQI)** (AU mandatory program) — quarterly
  aggregation of pressure injuries · physical restraint · weight loss ·
  falls/fractures · medication management. **Gap:** no aggregation entity.

### Universal / cross-vertical

- **Accreditation evidence bundle** (RCVS PSS · AAHA · ASAVA · CQC SAF · RACGP ·
  Cornerstone) — multi-section bundle: cover sheet + sampled records + audit
  cycles + CD audit + complaints + staff CPD. **Gap:** schema mostly OK; needs
  orchestration entity to define + execute "bundle these N sources for this
  accreditation cycle."
- **Statutory regulator notification submissions as form-image archive** mirroring
  portal forms (one-off PDF rendering of the submitted regulator form for our
  records). **Gap:** per-jurisdiction structured forms.

### Out of scope (not building, ever, unless product pivots)

- HIPAA Accounting of Disclosures — billing/payer-adjacent; we don't do billing.
- DEA Form 222 / CSOS records — billing/payer-adjacent.
- Per-vertical/country regulator-portal API integrations — manual submission with
  our PDF as evidence is enough for the foreseeable future.

---

## Pending: Migrate legacy report worker → v2 + delete fpdf (follow-up to V1)

**Status:** Reports v2 ships (Gotenberg sidecar + 6 universal report HTML
templates + the signed clinical note PDF migrated). The signed clinical
note worker (`internal/notes/jobs.go`) is already on the new HTML path.

What's still on fpdf:

`internal/reports/jobs.go` dispatches **7 compliance report types** to the
legacy fpdf builders (`internal/reports/pdf.go` + `evidence_pack.go`):

| Report type slug | Legacy builder | Has v2 equivalent? |
|---|---|---|
| `controlled_drugs_register` | `BuildControlledDrugsRegisterPDF` | ✅ `v2.RenderCDRegister` |
| `audit_pack` | `BuildAuditPackPDF` | ✅ `v2.RenderAuditPack` |
| `evidence_pack` | `BuildEvidencePackPDF` | ⚠ Partial — covered by audit_pack body |
| `records_audit` | `BuildRecordsAuditPDF` | ❌ no v2 yet |
| `incidents_log` | `BuildIncidentsLogPDF` | ⚠ Partial — v2 has single-incident, no log aggregator |
| `sentinel_events_log` | `BuildSentinelEventsLogPDF` | ❌ no v2 yet |
| `hipaa_disclosure_log` | `BuildHIPAADisclosureLogPDF` | ❌ out of scope (billing-adjacent) |

**Why not deleted in V1:** the worker dispatch is still wired to the
legacy builders and live in production. Pulling fpdf would break 7 hot
report types. P3-Q (delete fpdf) shipped the runway (HTML pipeline, 6
v2 templates, signed-note migration); flipping the remaining 7 types
needs per-type service-data → v2-input mapping (each builder pulls
different domain views — DrugOpView, EvidencePackInput, IncidentView,
SubjectAccessView, etc.).

**Steps to close out:**

1. Write 5 mappers in `internal/reports/v2_dispatch.go`:
   - `legacyToV2CDRegister` — drugs.Service ops + reconciliations → `v2.CDRegisterInput`
   - `legacyToV2AuditPack` — clinic + ops + recons + counts → `v2.AuditPackInput`
   - For the 3 still-needed types without v2 builders yet, add the v2 templates
     first (records_audit, incidents_log, sentinel_events_log).
   - HIPAA disclosure log is out of scope — drop it from the supported list
     after a real customer either asks for it or doesn't.
2. Migrate each `case` in `(*GenerateCompliancePDFWorker).buildPDF` to call the
   v2 path through the mapper.
3. Once dispatch is clean, delete `internal/reports/pdf.go` (958 LOC),
   `internal/reports/evidence_pack.go` (983 LOC), and the related fpdf-only
   test files.
4. Drop `github.com/go-pdf/fpdf` from `go.mod`; run `go mod tidy`.
5. Re-run integration tests + `make lint`.

**Estimated effort:** ~1.5 days. No new infrastructure; pure refactor.

**Why deferred:** safety. The user explicitly said "no shortcuts" — doing
this without the per-type mapping would break shipped functionality. The
v2 runway is in place; the migration is straightforward but mechanical
work that should land in its own focused PR.

---

## Pending: Doc-theme V2 differentiators (deferred from V1, 2026-05-04)

V1 of the doc-theme HTML rebuild covers the table-stakes: logo, brand color with
auto-contrast, header/footer with jurisdiction-aware footer library, page size +
landscape toggle, margin presets, curated font list (4-6), per-document-type
header/footer toggles, live preview with real sample data, theme versioning, and
multi-site theme inheritance. Plus three differentiators that are cheap-to-build
and visibly premium: per-document-type override matrix, watermark state presets
(DRAFT/AMENDED/COPY/REGULATOR-SUBMITTED with AMENDED system-enforced), and the
signature block designer.

Deferred to V2:

- **Accessibility checker on theme save** — WCAG 2.2 AA contrast (4.5:1 body, 3:1
  large), minimum font size enforcement (≥10pt body, ≥8pt footer), tagged-PDF
  readiness. Green/amber/red badge.
- **Color-blind simulation toggle** in preview — deuteranopia / protanopia
  one-click swap. Important for MAR sheets that use red/green for given/missed.
- **Brand-voice / boilerplate phrase guard** — head-office-locked disclaimer
  wording so a site can't accidentally reword the HIPAA / Privacy Act notice.
- **Multi-language header/footer** — te reo Māori bilingual headers (NZ), Welsh
  (UK), bilingual EN/ES (US). Field-level translation, not full template
  duplication.
- **Print-bleed-aware preview** — show trim line, safe area, 3mm bleed overlay
  when positioning logo/header.
- **Saved theme library / cross-clinic templates** beyond simple multi-site
  inheritance — Salvia-curated starter packs per (vertical, country).

### Explicitly NOT shipping (designer foot-guns)

- Raw CSS editor / "advanced mode" — one bad selector breaks every PDF.
- Arbitrary font upload — licensing nightmare + Chromium font fallback shifts
  layout subtly. Curated Google Fonts + system fonts is enough.
- Custom HTML injection in header/footer — XSS vector + lets users add tracking
  pixels (HIPAA-bad).
- Drag-to-position free-form canvas — clinical docs need predictable structure
  for regulator submission.
- Per-page background images — file size, accessibility tagging issues, conflicts
  with the watermark system.
