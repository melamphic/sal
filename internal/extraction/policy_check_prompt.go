package extraction

// formCoveragePromptInstructions is the trailing JSON-output spec + analysis
// rules shared by every provider's CheckFormCoverage prompt. Lives here so a
// single edit propagates to Gemini + OpenAI (and future providers).
//
// The crucial half of this prompt is the "Clauses the platform satisfies
// without a form field" section — without it the AI mistakes governance
// boilerplate (Policy Statement / Purpose / Scope) and platform-managed
// concerns (signatures / dates / record retention / drug ledgers) for
// missing form fields, then suggests adding text fields that would
// duplicate or bypass the system widget that already captures the data.
const formCoveragePromptInstructions = `
## Clauses the platform satisfies without a form field

Before judging any clause, decide which of the following categories it falls into. Clauses in any of these categories MUST be marked **satisfied**, even when no form field is named after them, because the Salvia platform handles them outside the form-fields surface. Do **not** suggest adding a form field for any of these.

1. **Policy-document / governance clauses.** Clauses that describe the policy itself rather than data collected during an encounter — Policy Statement, Purpose, Scope, Definitions, Principles, Roles & Responsibilities, References, Approvals, Effective Date, Review Date, Version, Acknowledgement. These live in the published policy document; they are not a data-capture concern of the form. Mark satisfied with reasoning like "governance clause — described in the policy document, not captured per-encounter."

2. **Patient identity.** Patient name, ID/MRN/microchip, date of birth, age, sex, species, breed, weight, owner. Captured by the system patient header pinned above every form. Reasoning: "captured by the system patient header."

3. **Author identity & signature.** Clinician name, role, registration number, signature, attending vet/nurse/RN. Captured by the system note-author identity + the clinic-themed signature footer rendered on every PDF. Reasoning: "captured by the system signature footer."

4. **Date / time / encounter timestamp.** Visit date, time of entry, time of administration, date of record. Stamped automatically on every note + every system-widget entry (consent, drug op, incident, pain). Reasoning: "stamped automatically by the system on submit."

5. **Record retention.** Retention period, archival policy, storage location of records. Configured per clinic on the form theme + enforced by the platform's archival pipeline. Reasoning: "platform-managed via clinic record-retention configuration."

6. **Audit trail / amendment log.** Who edited what and when, change reasons. The platform writes an immutable audit log on every note edit — no form field needed. Reasoning: "captured by the system audit log."

7. **System widget coverage.** When the form contains one of the system.* widgets, treat the entire compliance family as satisfied without checking for parallel free-text fields:
   - A field of type ` + "`system.consent`" + ` satisfies every clause about informed consent — including consent type, consent giver, witness, capacity, scope, written-vs-verbal, declined-consent handling. Do **not** ask for an extra "Consent Type" or "Consent Giver" field; the system.consent modal captures those.
   - A field of type ` + "`system.drug_op`" + ` satisfies every clause about controlled-drug administration / dispensing — drug name, dose, route, time, witness, balance, ledger. Do **not** ask for a free-text "Medication Administered" or "Drug Administration Records" field; the system.drug_op modal writes the ledger row.
   - A field of type ` + "`system.incident`" + ` satisfies every clause about adverse events, near-misses, falls, complaints, sentinel events.
   - A field of type ` + "`system.pain_score`" + ` satisfies every clause about pain assessment using any scale (NRS, CMPS-SF, FAS, Glasgow, Abbey, FLACC, Wong-Baker).

If a system.* widget is **missing** when its compliance family is required (e.g. the policy demands a controlled-drug record but the form has no system.drug_op field), that IS a real violation — flag it as a gap and suggest adding the matching system widget by name (not a generic text field).

8. **Salvia platform guarantees.** The following compliance obligations are automatically satisfied by the Salvia platform for every clinic, regardless of which form fields are present. Never suggest adding a form field for any of these, and mark clauses that only require them as **satisfied**:
   - **Electronic / digital signatures**: Every note submitted in Salvia is cryptographically signed by the authenticated clinician. The signer identity, role, and timestamp are locked into the audit log. Clauses requiring clinician signature, countersignature, or digital record authentication are satisfied by the platform.
   - **Encryption at rest and in transit**: All health records are encrypted at rest (AES-256) and in transit (TLS 1.3). Clauses requiring secure electronic storage or encrypted records are satisfied.
   - **Role-based access control**: Salvia enforces staff-role permissions on every record. Clauses requiring access control, least-privilege, or need-to-know are satisfied.
   - **Immutable audit log / amendment log**: Every create, read (by a disclosed clinician), edit, sign, and delete event is written to an immutable audit trail with actor identity, timestamp, and before/after values. Clauses requiring amendment logs, who-changed-what records, or versioned change history are satisfied.
   - **Record retention enforcement**: Retention periods are configured per clinic per jurisdiction and enforced automatically by the platform's archival pipeline. Clauses requiring retention periods to be defined or enforced are satisfied.
   - **Audio deletion on note submit**: Audio captured for AI extraction is deleted by default immediately after note submission. This satisfies clauses requiring that voice recordings not be kept longer than necessary.
   - **Patient consent for AI / audio recording**: Captured on the AI scribe consent form (` + "`salvia.shared.ai_scribe_consent`" + `) before any recording begins. Do not flag this as missing on other clinical forms — it is a pre-condition captured once per patient, not per encounter.
   - **Third-party processor agreements**: Salvia holds BAAs (US) and DPAs (UK/AU/NZ) with all data processors (Deepgram, Google Gemini). Clauses requiring vendor/business-associate agreements are satisfied at the platform level.
   - **Privacy notice / Notice of Privacy Practices**: Salvia provides a clinic-configurable privacy notice. Clauses requiring patients to be notified of data collection purposes are satisfied at enrolment (see ` + "`salvia.shared.ai_scribe_consent`" + ` and ` + "`salvia.general_clinic.new_patient_registration`" + `), not by individual clinical forms.
   - **Record legibility / non-alteration after signing**: Salvia records are immutable once signed. No form field needed.
   - **Contemporaneous recording**: The platform stamps every widget entry with the moment it is submitted. Clauses requiring entries to be contemporaneous or time-stamped are satisfied.

## Output

Return a JSON object with two keys:

- "narrative": a markdown-formatted analysis with these four sections in order:

    ## Overall
    One sentence summary of how well the form covers the policy.

    ## Covered
    - One bullet per clause that is satisfied. Reference the clause title and what covers it (named field, system widget, system header / footer / audit log, or "policy document" for governance clauses). Group governance and platform-managed clauses on one line if there are several.

    ## Gaps
    - One bullet ONLY for clauses that are genuinely uncovered — i.e. the data is captured per-encounter and no form field or system widget addresses it. For each gap suggest a specific field; if the gap is a compliance family, suggest the matching system widget (e.g. "add a system.drug_op field" rather than "add a Medications text field").
    - Do NOT list governance clauses, identity clauses, signature clauses, date/time clauses, retention clauses, or audit clauses here. Those are never gaps.

    ## Suggestions
    - Up to 3 actionable bullets to strengthen coverage, if any. Skip suggesting fields the platform handles automatically (date/time, signature, identity, audit). Skip suggesting plain-text duplicates of system.* widgets the form already includes.

    Use **bold** (with double asterisks) for field and clause names. Separate sections with blank lines. End every bullet with a newline.

- "clauses": an array with one object per input clause (same block_id). Each object:
    { "block_id": "<id>", "status": "satisfied"|"violated", "reasoning": "<one sentence>" }
    status=satisfied if any of the categories above (1-7) covers it, OR if at least one form field captures the data.
    status=violated only when the data is genuinely uncovered.
`
