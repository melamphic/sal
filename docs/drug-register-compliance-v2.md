# Drug Register Compliance v2 — Design

**Status:** Phases 1 + 2 + 3a shipped · 2026-05-01 · **rest backlogged** (see §13)
**Owner:** drugs module + dormant `mar` module
**Supersedes:** complements [drugs.md](drugs.md) — does not replace
**Pre-reads:** `drugs.md`, `BACKEND_PLAN.md`, `CLAUDE.md`

> **Stopping line decision · 2026-05-01.** Pivoting to Patients module +
> Stripe in-app to ship NZ vet pilot. v2 work paused at end of Phase 3a.
> Phases 1+2 (drug chain + validators + retention) live behind feature
> flag `drug_register.compliance_v2`, **flag stays off for NZ vet pilot**
> (v1 module ships unchanged). Phase 3a MAR scaffold left dormant —
> migration 00072 creates 4 empty tables, package compiles but routes
> unwired. Phases 3b–6b moved to backlog (§13). Reactivation trigger:
> first UK/US/aged-care customer in pipeline.

This document records the design + decisions for making Salvia's controlled-drug
module legally compliant across **4 verticals** (vet, GP, dental, aged care)
× **4 countries** (NZ, UK, US, AU). It is the source of truth for the schema
migrations + service-layer behaviour shipped in the `drug_register.compliance_v2`
feature flag.

## 1. Scope + non-goals

**In scope.** Schema + service-layer behaviour to satisfy the legal floor in
each jurisdiction we sell into. New aged-care MAR (Medication Administration
Record) sub-module. Vet partial-vial waste + procedure-grouping. Dental gas
cylinder tracking. Per-clinic retention policy + tamper-evident audit chain.
Regulator-shaped export endpoint.

**Out of scope (deferred).** Vial photo OCR · barcode scanning at entry ·
diversion-detection ML · cryptographic timestamping (RFC 3161). All have
either no validated demand (OCR) or no regulatory mandate (RFC 3161).

## 2. Why this work — what the existing module is missing

The current schema (migrations 00050-00054 + the catalog-overrides + shelf
tables) was built for a vet-only NZ MVP. Recent compliance audit found
gaps that block UK/US sales today and aged-care expansion entirely:

| Gap | Affects |
|---|---|
| No counterparty (supplier/recipient) name + address on ledger row | UK Sch 6 Part I + II · US 1304.22 · NZ Reg 40 · AU state regs |
| No DEA registration # field on clinic or staff | US 1304 + Form 222 |
| Single `quantity` field collapses commercial-container-count + units-per-container | US 1304.11(e)(1)(iv)(A) |
| No prescriber address snapshot on ledger | UK Sch 6 Part II · NZ Reg 40 |
| No collector identity + ID-evidence flags | UK Reg 16 (Health Act 2006 amendments) |
| No retention policy enforcement | UK 2y + 8y · NZ 4y + 10y · US 2y federal + state · AU per-state |
| No reconciliation cadence enforcement | NZ best practice · US 21 CFR 1304.11 biennial milestone |
| No partial-vial waste residual capture | RCVS · NSW S8 · Vic D&P |
| No aged-care MAR model — operations are per-event, MAR is per-resident-per-scheduled-dose with an outcome enum | CQC NICE NG67 · CMS F-Tag 755 · NZ HQSC NMC |
| No tamper-evident sequential numbering or chain | UK MDR 2001 Reg 20 · NZ Reg 37 ("consecutively numbered pages") |
| No N₂O / sedation-gas tracking (cylinders, not vials) | dental + sedation regs |

Source citations: [legislation.gov.uk MDR 2001 Sch 6](https://www.legislation.gov.uk/uksi/2001/3998/schedule/6) ·
[legislation.govt.nz MDR 1977 Reg 37-42](https://www.legislation.govt.nz/regulation/public/1977/0037/latest/whole.html) ·
[ecfr.gov 21 CFR 1304](https://www.ecfr.gov/current/title-21/chapter-II/part-1304) ·
[NICE NG67](https://www.nice.org.uk/guidance/ng67) ·
[NSW PD2022_032](https://www1.health.nsw.gov.au/pds/ActivePDSDocuments/PD2022_032.pdf).

## 3. Locked decisions

These are the eight architectural choices the design rests on. Each is the
"no-shortcut" option — chosen for legal defensibility and forward-compatibility
over short-term implementation ease.

### 3.1 Tamper-evident chain — YES, per-drug-strength-form

Each insert into `drug_operations_log` computes `row_hash = SHA256(canonical
row repr || prev_row_hash)` where `prev_row_hash` is read from the most-recent
prior row in the same `(clinic_id, drug_substance, strength, form)` chain.

Why per-drug-strength-form not per-clinic: UK MDR 2001 Reg 20(1)(b) and
NZ Misuse of Drugs Reg 1977 Reg 37(2)(a) both require *"each page shall
have entries relating only to 1 form of 1 controlled drug"*. A per-page
chain matches the bound-book legal model exactly. A per-clinic chain
would technically still detect tampering but would not match the
regulatory abstraction.

Hash chain is **not legally mandated** in any jurisdiction surveyed, but
it is cheap (≈1 dev day), it is a defensible product differentiator, and
it gives us a clean answer to any "prove the register wasn't tampered with"
question from an inspector or in an incident review.

### 3.2 Retention defaults per country

Verified against current legislation text (May 2026):

| Country | CD register | MAR (aged care) | Notes |
|---|---|---|---|
| **NZ** | **4 years** after last entry (MDR 1977 Reg 42) | **10 years** from last service (Health (Retention of Health Info) Regs 1996 Reg 2) | Reg 42 amended 1978 + 2008 |
| **UK** | **2 years** from last entry (MDR 2001) | **8 years** after care ends (NICE NG67) | CD floor; MAR floor |
| **US** | **7 years** (federal floor 2y per 21 CFR 1304.04(a); we default to 7 to cover strictest state — MA, WA) | 5 years federal (42 CFR 483.70) or state max | State override per clinic |
| **AU** | **7 years** blanket (covers state Health Records Acts; some states 7y for adults / 25y for minors) | 7 years | State override per clinic |

A `clinic_drug_retention_policy` table seeds these from country at clinic
creation. Per-clinic override allowed (always upward — a clinic can keep
records longer than the floor, never shorter).

### 3.3 Australia — all 8 states day one

We build a state-aware `au_validator.go` with a state-rules table covering
NSW, VIC, QLD, WA, SA, TAS, ACT, NT. WA is strictest on witness +
destruction, so the default is WA-strict and other states relax via
table flags. No per-state code branches — table-driven.

This conforms to the project rule (memory: *"build for all verticals × all
countries from Day 1; vertical/country are prompt context, not code branches"*)
adapted to backend law: country is structural (CDs are different drugs in
different countries) but the *enforcement mechanism* must be table-driven so
adding a state never requires a service rewrite.

### 3.4 Aged-care MAR — separate module `internal/mar/`

MAR is a different problem domain from CD ledgering:

- The unit of work is "scheduled dose" (per resident per drug per time-slot),
  not "operation" (per ledger row).
- The vast majority of MAR rows are **non-controlled** drugs (paracetamol,
  laxatives, vitamins) — they don't belong in `drug_operations_log` at all.
- The outcome model is a 14-option enum (administered / refused / vomited /
  asleep / hospitalised / …), not a 6-option operation type.

The new `internal/mar/` module follows the 4-file rule (`handler.go` ·
`service.go` · `repository.go` · `routes.go`). When a MAR administration
event fires for a controlled drug, the MAR service calls the
`drugs.Service.LogOperation()` exported interface to write the parallel
ledger row atomically. **No cross-domain table access** (per CLAUDE.md
rule: "call exported service interfaces only").

Module name `mar` not `aged_care_mar` — leaves room for hospital MAR
without rename. (Aged-care semantics are seeded by clinic vertical, not
hard-coded.)

### 3.5 Phase order

Phases are sequenced so each phase compiles + ships independently, behind
the `drug_register.compliance_v2` feature flag, before the next phase
starts:

1. Schema deltas to existing CD module (compliance fields, waste, retention, attachments, DEA registration table)
2. Validators + cadence enforcer + hash chain + retention purge + sequential numbering
3. New `mar` module (prescriptions · scheduled doses · administration events · rounds · outcome enum)
4. Vet additions (procedure grouping · weight + species snapshot)
5. Dental gas sub-module (cylinders · gas operations)
6. Regulator export (UK Sch 6 / US 1304 / NZ Reg 40 / AU state-shaped artefacts)

### 3.6 DEA registration storage — new table

US clinics and practitioners may hold multiple DEA registrations (different
sites, different schedule authorities, multi-location practices). Single
TEXT column on `clinics` and `staff` would force a migration the first
time we hit a multi-registration clinic.

```
dea_registrations (
  id UUID PRIMARY KEY,
  owner_type TEXT CHECK (owner_type IN ('clinic','staff')),
  owner_id UUID NOT NULL,
  registration_number TEXT NOT NULL,
  schedules_authorized TEXT[] NOT NULL,
  expires_at DATE,
  archived_at TIMESTAMPTZ,
  ...
)
```

A `drug_operations_log.dea_registration_id UUID` snapshots which registration
authorised each US Sch II op. NULL outside US.

### 3.7 Hash-chain scope — per-drug-strength-form

Already covered in 3.1. Restated here as a top-level decision because it
shapes the schema:

- `chain_key` derived column = `SHA256(clinic_id || drug_substance || strength || form)`
- Sequential numbering also per-`chain_key` — `entry_seq_in_chain BIGINT` + `entry_seq BIGSERIAL` (global)
- Both columns indexed; gap detection runs per-chain

### 3.8 Pilot rollout — flag-gated

All v2 behaviour lives behind `drug_register.compliance_v2`. NZ vet pilot
clinic enables it first; we run a parallel-write period (v1 + v2 both
live, v1 still source of truth) for ≥30 days, then flip v2 to source of
truth, then enable for the rest of the fleet.

Backfill scripts populate `entry_seq` + `entry_seq_in_chain` + `row_hash`
for legacy rows so the chain is intact from clinic onboarding.

## 4. Schema deltas

### 4.1 Migration 00060 — `drug_operations_log` compliance fields

All NULLABLE so legacy rows aren't broken. Validators enforce per-country
NOT NULL at service layer (so the same schema serves all countries).

```sql
ALTER TABLE drug_operations_log
  ADD COLUMN entry_seq BIGSERIAL,
  ADD COLUMN entry_seq_in_chain BIGINT,
  ADD COLUMN chain_key BYTEA,                   -- SHA256(clinic||sub||strength||form)
  ADD COLUMN prev_row_hash BYTEA,
  ADD COLUMN row_hash BYTEA,

  ADD COLUMN counterparty_name TEXT,
  ADD COLUMN counterparty_address TEXT,
  ADD COLUMN counterparty_dea_number TEXT,      -- US

  ADD COLUMN prescriber_name TEXT,              -- snapshot at entry
  ADD COLUMN prescriber_address TEXT,
  ADD COLUMN prescriber_dea_number TEXT,        -- US
  ADD COLUMN dea_registration_id UUID
    REFERENCES dea_registrations(id),

  ADD COLUMN patient_address TEXT,              -- snapshot at entry

  ADD COLUMN collector_name TEXT,               -- UK
  ADD COLUMN collector_id_evidence_requested BOOLEAN,
  ADD COLUMN collector_id_evidence_provided BOOLEAN,

  ADD COLUMN prescription_ref TEXT,             -- Rx number
  ADD COLUMN order_form_serial TEXT,            -- US Form 222 / CSOS

  ADD COLUMN commercial_container_count INTEGER,
  ADD COLUMN units_per_container NUMERIC(14,4),

  ADD COLUMN batch_number TEXT,                 -- snapshot at entry
  ADD COLUMN expiry_date DATE,                  -- snapshot at entry

  ADD COLUMN signature_hash TEXT,
  ADD COLUMN retention_until DATE;

CREATE UNIQUE INDEX idx_drug_op_chain_seq
  ON drug_operations_log (clinic_id, chain_key, entry_seq_in_chain);
CREATE INDEX idx_drug_op_retention
  ON drug_operations_log (retention_until)
  WHERE retention_until IS NOT NULL;
```

Snapshot rule: **prescriber_name / patient_address / batch_number / expiry_date
are denormalised at insert time and never updated.** The patient/staff/shelf
record can change, but the ledger row is the legal artefact and must remain
immutable. Service layer reads the live record, captures the snapshot,
writes the row.

### 4.2 Migration 00061 — partial-vial waste

```sql
ALTER TABLE drug_operations_log
  ADD COLUMN waste_residual_qty NUMERIC(14,4),
  ADD COLUMN waste_reason TEXT,
  ADD COLUMN waste_witnessed_by UUID REFERENCES staff(id);

ALTER TABLE drug_operations_log
  ADD CONSTRAINT discard_requires_residual
  CHECK (operation <> 'discard' OR waste_residual_qty IS NOT NULL);
```

### 4.3 Migration 00062 — retention policy

```sql
CREATE TABLE clinic_drug_retention_policy (
  clinic_id UUID PRIMARY KEY REFERENCES clinics(id),
  ledger_years INTEGER NOT NULL,
  recon_years INTEGER NOT NULL,
  mar_years INTEGER NOT NULL,
  set_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  set_by UUID REFERENCES staff(id)
);
```

Seeded from `clinics.country` at clinic creation per the table in §3.2.

### 4.4 Migration 00063 — operation attachments

```sql
CREATE TABLE drug_op_attachments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  drug_op_id UUID REFERENCES drug_operations_log(id) ON DELETE RESTRICT,
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  kind TEXT NOT NULL CHECK (kind IN
    ('invoice','destruction_cert','vial_photo','witness_photo','other')),
  s3_url TEXT NOT NULL,
  uploaded_by UUID NOT NULL REFERENCES staff(id),
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ
);
CREATE INDEX idx_drug_op_attachments_op ON drug_op_attachments(drug_op_id);
```

Pending attachments (drug_op_id NULL) cleaned up by a 24h sweep.

### 4.5 Migration 00064 — DEA registrations

```sql
CREATE TABLE dea_registrations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  owner_type TEXT NOT NULL CHECK (owner_type IN ('clinic','staff')),
  owner_id UUID NOT NULL,
  registration_number TEXT NOT NULL,
  schedules_authorized TEXT[] NOT NULL,
  expires_at DATE,
  archived_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_dea_reg_unique
  ON dea_registrations (clinic_id, owner_type, owner_id, registration_number)
  WHERE archived_at IS NULL;
```

### 4.6 Migration 00065 — MAR module schema

The biggest delta. Five new tables in their own namespace.

```sql
CREATE TABLE mar_prescriptions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  resident_id UUID NOT NULL REFERENCES subjects(id),
  catalog_entry_id UUID,                        -- nullable for compounded
  override_id UUID,                             -- nullable; one of {catalog,override} required
  formulation TEXT NOT NULL,
  strength TEXT NOT NULL,
  dose TEXT NOT NULL,
  route TEXT NOT NULL,
  frequency TEXT NOT NULL,                      -- "BD", "TDS", "QID", "PRN"
  schedule_times TEXT[],                        -- e.g. ['08:00','13:00','20:00']
  is_prn BOOLEAN NOT NULL DEFAULT false,
  prn_indication TEXT,
  prn_max_24h NUMERIC(14,4),
  indication TEXT,
  prescriber_id UUID REFERENCES staff(id),
  prescriber_external_name TEXT,                -- when not a staff member
  prescriber_external_address TEXT,
  start_at TIMESTAMPTZ NOT NULL,
  stop_at TIMESTAMPTZ,
  review_at TIMESTAMPTZ,
  instructions TEXT,
  allergies_checked BOOLEAN NOT NULL DEFAULT false,
  is_controlled BOOLEAN NOT NULL DEFAULT false,
  schedule_class TEXT,                          -- 'CD2','S8','CII' etc.
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mar_scheduled_doses (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  prescription_id UUID NOT NULL REFERENCES mar_prescriptions(id),
  scheduled_at TIMESTAMPTZ NOT NULL,
  dose_qty NUMERIC(14,4) NOT NULL,
  route TEXT NOT NULL,
  generated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_mar_scheduled_due ON mar_scheduled_doses(clinic_id, scheduled_at);

CREATE TABLE mar_rounds (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  started_by UUID NOT NULL REFERENCES staff(id),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  shift_label TEXT,                             -- 'morning'|'afternoon'|'evening'|'night'
  location TEXT
);

CREATE TYPE mar_outcome_code AS ENUM (
  'administered','partial','refused',
  'omitted_clinical_hold','omitted_npo_fasting',
  'vomited','asleep','unavailable_off_site','hospitalised',
  'out_of_stock','not_required_prn','discontinued',
  'destroyed','error_not_given'
);

CREATE TABLE mar_administration_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  resident_id UUID NOT NULL REFERENCES subjects(id),
  prescription_id UUID NOT NULL REFERENCES mar_prescriptions(id),
  scheduled_dose_id UUID REFERENCES mar_scheduled_doses(id),  -- null for PRN
  round_id UUID REFERENCES mar_rounds(id),
  actual_at TIMESTAMPTZ NOT NULL,
  actual_dose_qty NUMERIC(14,4),
  route TEXT,
  outcome_code mar_outcome_code NOT NULL,
  outcome_reason TEXT,
  administered_by UUID NOT NULL REFERENCES staff(id),
  witness_id UUID REFERENCES staff(id),
  notes TEXT,
  prn_indication_trigger TEXT,
  prn_effectiveness TEXT,
  prn_effectiveness_reviewed_at TIMESTAMPTZ,
  drug_op_id UUID REFERENCES drug_operations_log(id),  -- set when CD
  corrects_id UUID REFERENCES mar_administration_events(id),
  prev_row_hash BYTEA,
  row_hash BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_mar_event_resident ON mar_administration_events(clinic_id, resident_id, actual_at DESC);
CREATE INDEX idx_mar_event_round ON mar_administration_events(round_id) WHERE round_id IS NOT NULL;

ALTER TABLE mar_administration_events
  ADD CONSTRAINT outcome_reason_required
  CHECK (outcome_code = 'administered' OR outcome_reason IS NOT NULL);

ALTER TABLE mar_administration_events
  ADD CONSTRAINT cd_witness_required
  CHECK (drug_op_id IS NULL OR witness_id IS NOT NULL);
```

The `cd_witness_required` constraint enforces "per-event, never per-round"
witness for CDs at the database level — service-layer rule made physical.

### 4.7 Migration 00066 — vet procedure grouping

```sql
CREATE TABLE drug_procedures (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  subject_id UUID NOT NULL REFERENCES subjects(id),
  procedure_type TEXT NOT NULL,                 -- 'anaesthesia','sedation','euthanasia',...
  started_at TIMESTAMPTZ NOT NULL,
  ended_at TIMESTAMPTZ,
  anaesthetist_id UUID REFERENCES staff(id),
  surgeon_id UUID REFERENCES staff(id),
  notes TEXT,
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE drug_operations_log
  ADD COLUMN procedure_id UUID REFERENCES drug_procedures(id),
  ADD COLUMN subject_weight_kg NUMERIC(8,3),    -- snapshot at entry
  ADD COLUMN subject_species TEXT;              -- snapshot at entry
```

### 4.8 Migration 00067 — dental gas sub-module

```sql
CREATE TABLE gas_cylinders (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  gas_type TEXT NOT NULL CHECK (gas_type IN ('n2o','o2','sevoflurane','isoflurane','medical_air')),
  cylinder_size_l NUMERIC(8,2) NOT NULL,
  serial_number TEXT,
  batch_number TEXT,
  expiry_date DATE,
  received_from TEXT,
  received_at TIMESTAMPTZ,
  current_pressure_kpa NUMERIC(10,2),
  archived_at TIMESTAMPTZ
);

CREATE TABLE gas_operations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  clinic_id UUID NOT NULL REFERENCES clinics(id),
  cylinder_id UUID NOT NULL REFERENCES gas_cylinders(id),
  used_at TIMESTAMPTZ NOT NULL,
  used_by UUID NOT NULL REFERENCES staff(id),
  witness_id UUID REFERENCES staff(id),
  subject_id UUID REFERENCES subjects(id),
  pressure_start_kpa NUMERIC(10,2) NOT NULL,
  pressure_end_kpa NUMERIC(10,2) NOT NULL,
  derived_volume_l NUMERIC(10,3),               -- computed by service from pressure delta + cylinder size
  procedure_id UUID REFERENCES drug_procedures(id),
  notes TEXT,
  prev_row_hash BYTEA,
  row_hash BYTEA
);
```

## 5. Service-layer additions

### 5.1 Per-country validators — `internal/drugs/validators/`

One file per country: `uk_validator.go`, `us_validator.go`, `nz_validator.go`,
`au_validator.go`. Each exports a `Validate(op LogOperationInput, clinic Clinic)
error` function. The dispatcher in `service.go` reads `clinic.country` and
calls the matching validator.

Country-specific NOT-NULL rules:

| Country | Required fields beyond v1 |
|---|---|
| UK | counterparty_name + counterparty_address (receive/dispense); prescriber_name + prescriber_address (dispense); collector_name + collector_id_evidence_requested + collector_id_evidence_provided (dispense to public) |
| US | counterparty_dea_number + dea_registration_id (Sch II receive); commercial_container_count + units_per_container (Sch II); order_form_serial (Form 222 receives) |
| NZ | counterparty_name + counterparty_address (receive/supply Class A/B); prescriber_name (dispense); patient_name + patient_address (dispense); running balance always |
| AU | batch_number + expiry_date (S8 always); witness_id required for all S8 destruction (WA strict default; other states relax via state-rules table) |

The state-rules table for AU lives in `internal/drugs/validators/au_state_rules.go`
as a `map[State]Rules` literal. No code branches on state.

### 5.2 Reconciliation cadence enforcer

New service method `EnforceReconciliationCadence(ctx, clinicID)`. Run by a
daily cron. Reads clinic country + vertical, calculates next-due date from
last completed reconciliation, emits warnings at 7 days before due, locks
new ops at 14 days overdue (configurable per clinic).

Cadences:

- **NZ vet** — monthly (NZVA Code best practice, codified in product not law)
- **NZ pharmacy** — monthly (Medsafe guidance)
- **UK GP/dental** — 6-monthly (CQC standard)
- **UK pharmacy** — weekly (RCVS / CD audit floor)
- **US** — biennial inventory milestone (21 CFR 1304.11) + monthly recon
- **AU** — monthly (state guidance)

### 5.3 Hash-chain compute + verify

`repository.LogOperation()` extends to compute `chain_key` from the canonical
key tuple, read the prev row's `row_hash` for that chain, compute new
`row_hash`, write atomically with the rest of the row insert + balance
update. Single transaction.

New service method `VerifyChain(ctx, clinicID, chainKey, fromSeq, toSeq)`
returns `ChainStatus { Intact bool, FirstBrokenSeq int64 }`. Exposed at
`GET /api/v1/drugs/operations/verify-chain?clinic_id&drug&strength&form&from&to`.

### 5.4 Sequential gap detector

`SELECT entry_seq_in_chain FROM drug_operations_log WHERE clinic_id=$1 AND
chain_key=$2 ORDER BY entry_seq_in_chain` — flag any gap. Wired into the
verify-chain endpoint as a secondary check (gap → chain claim is invalid
even if hashes verify, because rows were physically deleted).

### 5.5 Retention purge job

Daily worker reads `clinic_drug_retention_policy`, computes per-row
`retention_until = created_at + ledger_years` if NULL, soft-deletes
(set `archived_at`) any row where `retention_until < now()`. Purge is
soft-only — physical purge requires a separate admin endpoint with
1-year grace + double-confirmation.

Reconciliation rows respect their own `recon_years` from the policy table.

### 5.6 MAR ↔ drugs cross-domain link

`mar.Service.RecordAdministration()` calls `drugs.Service.LogOperation()`
when `prescription.is_controlled = true` and `outcome_code IN
('administered','partial','destroyed')`. Single DB transaction across both
domains via shared `pgx.Tx`. The drugs service exposes a `LogOperationTx`
variant that takes an existing transaction so the MAR service can
participate without breaking the cross-domain rule (no direct table
access; everything goes through service interfaces).

Linkage is recorded by stamping `mar_administration_events.drug_op_id`
inside the same transaction.

## 6. API surface additions

### 6.1 Drugs module — new endpoints

```
POST   /api/v1/drugs/op-photos                     (multipart upload, returns extracted fields if vial photo)
GET    /api/v1/drugs/operations/verify-chain
POST   /api/v1/drugs/procedures
GET    /api/v1/drugs/procedures
POST   /api/v1/drugs/procedures/{id}/operations    (atomic N-row create)
GET    /api/v1/drugs/regulator-export              (?country&from&to&format)
POST   /api/v1/drugs/dea-registrations
GET    /api/v1/drugs/dea-registrations
DELETE /api/v1/drugs/dea-registrations/{id}
```

### 6.2 MAR module — full surface

```
POST   /api/v1/mar/prescriptions
GET    /api/v1/mar/prescriptions/{resident_id}
PATCH  /api/v1/mar/prescriptions/{id}
POST   /api/v1/mar/prescriptions/{id}/discontinue
GET    /api/v1/mar/scheduled-doses                 (?from&to&resident_id?)
POST   /api/v1/mar/rounds                          (start a round)
POST   /api/v1/mar/rounds/{id}/complete
POST   /api/v1/mar/administration-events           (record one event)
GET    /api/v1/mar/administration-events           (?resident_id&from&to)
POST   /api/v1/mar/administration-events/{id}/correct
```

### 6.3 Gas module — full surface

```
POST   /api/v1/gas/cylinders
GET    /api/v1/gas/cylinders
PATCH  /api/v1/gas/cylinders/{id}
POST   /api/v1/gas/operations
GET    /api/v1/gas/operations
```

Type names: `MarPrescriptionResponse`, `MarPrescriptionListResponse`,
`MarAdminEventResponse`, `GasCylinderResponse`, etc. Per CLAUDE.md
huma rule (globally unique type names; package-prefixed).

## 7. Permissions

New permissions added to the auth model:

```
perm_view_drug_register           (existing)
perm_log_drug_op                  (existing)
perm_witness_controlled_drugs     (existing)
perm_reconcile_drugs              (existing)
perm_view_mar
perm_record_mar_administration
perm_prescribe_mar
perm_amend_mar
perm_manage_gas_cylinders
perm_log_gas_op
perm_export_drug_regulator_report
perm_manage_dea_registrations
```

Default role mapping seeded for each (vertical × country) at clinic creation.

## 8. Test strategy

### 8.1 Unit — fake repos per CLAUDE.md template

- `internal/drugs/validators/uk_validator_test.go` — every UK rule with
  positive + negative cases.
- Same for NZ/US/AU. AU test parameterised across 8 states.
- `internal/drugs/chain_test.go` — chain integrity (single-row tampering
  detected; gap detected; chain re-computed correctly across corrections).
- `internal/drugs/retention_test.go` — retention_until computed correctly
  per (country, op-type), purge soft-deletes only past retention.
- `internal/mar/service_test.go` — outcome enum exhaustive; CD events
  generate parallel drug_op rows; non-CD events do not; witness rule
  enforced for CD only.
- `internal/mar/round_test.go` — round grouping is UI concept only;
  per-event witness still required.

### 8.2 Compliance fixture matrix

`internal/drugs/compliance_test.go` runs a generated year of synthetic
ops for each (vertical × country) = 16 fixtures. Each fixture asserts:

- All required fields populated for that country.
- Regulator export produces all required columns.
- Hash chain intact across the full year.
- Retention_until correct on every row.

### 8.3 Integration

`//go:build integration` tests for cross-module flows:

- MAR administration of a CD → drug_operations_log row written atomically
  → chain extends → balance decrements → mar_administration_events.drug_op_id
  populated.
- Reconciliation cadence overdue → new ops blocked at 14d.
- Retention purge → soft-delete only, audit log records purge action.

## 9. Migration order + rollback

| # | Migration | Breaking? | Backfill | Down |
|---|---|---|---|---|
| 00060 | drug_op compliance fields | No (NULLABLE) | entry_seq + chain_key + hash for legacy rows | ALTER DROP COLUMN |
| 00061 | partial-vial waste | No | None (applies to new discards) | ALTER DROP COLUMN |
| 00062 | retention policy table | No | Seed defaults per clinic.country | DROP TABLE |
| 00063 | drug op attachments | No | None | DROP TABLE |
| 00064 | dea_registrations | No | None | DROP TABLE |
| 00065 | MAR module schema | No (new) | None | DROP TABLE (5) + DROP TYPE |
| 00066 | drug procedures + vet snapshots | No | None | ALTER DROP + DROP TABLE |
| 00067 | gas cylinders + ops | No | None | DROP TABLE (2) |

Every migration ships with both Up and Down per CLAUDE.md rule.

Backfill script `scripts/backfill_chain.go` runs once, reads every legacy
op in `created_at` order, computes chain_key + entry_seq_in_chain +
prev_row_hash + row_hash. Idempotent (skips rows that already have
non-NULL row_hash). Run from `make backfill-drug-chain`.

## 10. Rollout

1. **Phase 1+2 land** behind `drug_register.compliance_v2`. Validators run
   in shadow mode (log mismatches, don't block) for ≥7 days against the
   NZ vet pilot clinic.
2. **Shadow → enforce** at the pilot clinic. Validators reject non-compliant
   ops. Pilot runs for ≥30 days.
3. **Backfill chain** for all clinics. One-shot script per clinic.
4. **Phase 3** (MAR) lands. UK + AU aged-care pilot clinics onboarded.
5. **Phase 4-5** (vet procedures + dental gas) land.
6. **Phase 6** (regulator export) lands. Verified against UK CD inspector
   sample export, NZ Medsafe-style report, US biennial inventory format.
7. **Cross-fleet enable** — flag flipped on for all clinics.

## 11. Estimated effort

| Phase | Backend dev days |
|---|---|
| 1 — Schema + validators scaffold | 4 |
| 2 — Validators · cadence · chain · retention · purge | 5 |
| 3 — MAR module (full 4-file + tests + migration) | 8 |
| 4 — Vet procedures + waste | 2 |
| 5 — Dental gas | 3 |
| 6 — Regulator export | 3 |
| Compliance fixture matrix + integration tests | 4 |
| Backfill script + rollout tooling | 1 |
| **Total** | **30 days** ≈ 6 weeks one-engineer · realistic with buffer **8 weeks** |

## 12. Open follow-ups (not blockers)

- US state-by-state retention legal memo before enabling US sales.
- AU per-state legal review of the state-rules table before enabling AU sales.
- Diversion-detection ML — needs ≥18 months of fleet ledger data first.
- Vial photo OCR — needs validated user demand (currently inferred only).
- Open API for incumbent migrations (PointClickCare, ezyVet) — defer
  until at least one customer asks.

## Appendix A — Source citations

- UK Misuse of Drugs Regulations 2001 — Reg 19, Reg 20, Schedule 6 — https://www.legislation.gov.uk/uksi/2001/3998
- UK Controlled Drugs (Supervision of Management and Use) Regs 2013 — https://www.legislation.gov.uk/uksi/2013/373
- UK Health Act 2006 — collector-ID amendments
- UK NICE NG67 — adult social care medicines management — https://www.nice.org.uk/guidance/ng67
- UK CQC controlled drugs in care homes — https://www.cqc.org.uk/guidance-providers/adult-social-care/controlled-drugs-care-homes
- UK RPS MEP §3.6.11 — https://www.rpharms.com/mep/3-underpinning-knowledge-legislation-and-professional-issues/36-controlled-drugs/3611-record-keeping-and-controlled-drugs-registers
- US 21 CFR 1304 — https://www.ecfr.gov/current/title-21/chapter-II/part-1304
- US 21 CFR 1305 — Form 222 / CSOS — https://www.ecfr.gov/current/title-21/chapter-II/part-1305
- US 42 CFR 483.45 + 483.70 — LTC pharmacy services + clinical records
- US CMS F-Tag 755 — pharmacy services + record-keeping in LTC
- NZ Misuse of Drugs Regulations 1977 — Regs 37-46 (register format + retention) — https://www.legislation.govt.nz/regulation/public/1977/0037/latest/whole.html
- NZ Health (Retention of Health Information) Regulations 1996 — https://www.legislation.govt.nz/regulation/public/1996/0343/latest/whole.html
- NZ HQSC National Medication Chart user guide 2021 — https://www.hqsc.govt.nz/assets/Our-work/System-safety/Reducing-harm/Medicines/Publications-resources/NMC_user_guide_update_2021.pdf
- NZ Medicines Care Guides for Residential Aged Care 2011 — https://www.health.govt.nz/system/files/2011-10/medicines-care-guides-for-residential-aged-care-may11.pdf
- AU NSW PD2022_032 — https://www1.health.nsw.gov.au/pds/ActivePDSDocuments/PD2022_032.pdf
- AU VIC Drugs, Poisons and Controlled Substances Regs 2017 — https://www.legislation.vic.gov.au/in-force/statutory-rules/drugs-poisons-and-controlled-substances-regulations-2017
- AU QLD Medicines and Poisons (Medicines) Regulation 2021 — https://www.legislation.qld.gov.au/view/html/inforce/current/sl-2021-0140
- Tamper-evident landscape — Toniq Guard NZ (Beehive press release 2025), eCDR-Pro UK G-Cloud, CDRx UK, Modeus AU/NZ vet, CUBEX US — none mandate hash-chain; all use append-only + audit log.
