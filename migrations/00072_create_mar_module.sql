-- +goose Up
-- +goose StatementBegin

-- Compliance v2: aged-care Medication Administration Record (MAR).
--
-- Why a new module + new tables (rather than extending the existing
-- drugs module): MAR is structurally different.
--   * Most MAR rows are NON-controlled drugs (paracetamol, vitamins,
--     laxatives) — they don't belong in drug_operations_log.
--   * The unit of work is "scheduled dose per resident per time-slot"
--     rather than "operation against a shelf row".
--   * The outcome is a 14-option enum (administered/refused/vomited/
--     asleep/hospitalised/...) rather than a 6-option op type.
--
-- When a MAR administration event involves a CONTROLLED drug, the MAR
-- service writes a parallel row to drug_operations_log in the same
-- transaction (cross-domain via service interface). The MAR event row
-- carries drug_op_id pointing back to that ledger row.
--
-- Sources (legal floor):
--   * UK NICE NG67 §1.5 — adult social care medicines management
--   * UK CMS F-Tag 755 — pharmacy services + record-keeping
--   * NZ HQSC National Medication Chart user guide 2021
--   * NZ Medicines Care Guides for Residential Aged Care 2011
--   * AU NSW PD2022_032 + Vic D&P + Queensland HSU Medication Mgmt
--
-- Witness rule: per-event for CDs (CQC + RPS + NSW S8). NEVER per-round
-- — the cd_witness_required CHECK constraint enforces this at DB level.
--
-- Design: docs/drug-register-compliance-v2.md §4.6

CREATE TYPE mar_outcome_code AS ENUM (
    'administered',
    'partial',
    'refused',
    'omitted_clinical_hold',
    'omitted_npo_fasting',
    'vomited',
    'asleep',
    'unavailable_off_site',
    'hospitalised',
    'out_of_stock',
    'not_required_prn',
    'discontinued',
    'destroyed',
    'error_not_given'
);

CREATE TABLE mar_prescriptions (
    id                          UUID PRIMARY KEY,
    clinic_id                   UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    resident_id                 UUID NOT NULL REFERENCES subjects(id),

    -- Either catalog_entry_id (system catalog) or override_id
    -- (clinic-defined custom drug) — at least one must be set.
    catalog_entry_id            TEXT,
    override_id                 UUID REFERENCES clinic_drug_catalog_overrides(id),

    -- Drug identity snapshot at prescription time. Same denorm rationale
    -- as drug_operations_log — the catalog row may evolve (renames,
    -- archived) but the prescription must remain immutable.
    drug_name                   TEXT NOT NULL,
    formulation                 TEXT NOT NULL,
    strength                    TEXT NOT NULL,
    dose                        TEXT NOT NULL,
    route                       TEXT NOT NULL,
    frequency                   TEXT NOT NULL,             -- 'BD','TDS','QID','PRN','Q4H'
    schedule_times              TEXT[],                    -- e.g. ['08:00','13:00','20:00']

    is_prn                      BOOLEAN NOT NULL DEFAULT FALSE,
    prn_indication              TEXT,
    prn_max_24h                 NUMERIC(14,4),

    indication                  TEXT,                      -- diagnosis / clinical reason
    prescriber_id               UUID REFERENCES staff(id),
    prescriber_external_name    TEXT,                      -- when prescriber is not a staff member (visiting GP)
    prescriber_external_address TEXT,

    start_at                    TIMESTAMPTZ NOT NULL,
    stop_at                     TIMESTAMPTZ,
    review_at                   TIMESTAMPTZ,
    instructions                TEXT,
    allergies_checked           BOOLEAN NOT NULL DEFAULT FALSE,

    -- Drives the cross-domain CD link. When TRUE, every administered/
    -- partial/destroyed event also writes drug_operations_log + chain.
    is_controlled               BOOLEAN NOT NULL DEFAULT FALSE,
    schedule_class              TEXT,                      -- 'CD2','S8','CII','S1', etc.

    archived_at                 TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT mar_prescription_drug_source_required
        CHECK (catalog_entry_id IS NOT NULL OR override_id IS NOT NULL)
);

CREATE INDEX mar_prescriptions_resident_idx
    ON mar_prescriptions (clinic_id, resident_id, start_at DESC)
    WHERE archived_at IS NULL;

CREATE INDEX mar_prescriptions_active_window_idx
    ON mar_prescriptions (clinic_id, start_at, stop_at)
    WHERE archived_at IS NULL;

CREATE INDEX mar_prescriptions_review_due_idx
    ON mar_prescriptions (clinic_id, review_at)
    WHERE review_at IS NOT NULL AND archived_at IS NULL;

-- mar_scheduled_doses: nightly-generated rows from
-- mar_prescriptions.schedule_times for the upcoming 30-day window.
-- Routine prescriptions produce one row per (prescription, scheduled_at)
-- combo; PRN prescriptions produce zero (events are open-ended).
CREATE TABLE mar_scheduled_doses (
    id              UUID PRIMARY KEY,
    clinic_id       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    prescription_id UUID NOT NULL REFERENCES mar_prescriptions(id) ON DELETE CASCADE,
    scheduled_at    TIMESTAMPTZ NOT NULL,
    dose_qty        NUMERIC(14,4) NOT NULL,
    route           TEXT NOT NULL,
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX mar_scheduled_doses_due_idx
    ON mar_scheduled_doses (clinic_id, scheduled_at);

CREATE INDEX mar_scheduled_doses_prescription_idx
    ON mar_scheduled_doses (prescription_id, scheduled_at);

-- One scheduled dose per (prescription, slot) — generator is idempotent.
CREATE UNIQUE INDEX mar_scheduled_doses_unique_idx
    ON mar_scheduled_doses (prescription_id, scheduled_at);

-- mar_rounds: a UI/workflow grouping for "one nurse walking the cart
-- through N residents in a 30-min window". Persists for reporting + UI
-- replay; NOT a witness-shortcut. Per-event witness is still required
-- for CDs.
CREATE TABLE mar_rounds (
    id            UUID PRIMARY KEY,
    clinic_id     UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    started_by    UUID NOT NULL REFERENCES staff(id),
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    shift_label   TEXT,                                    -- 'morning','afternoon','evening','night'
    location      TEXT,                                    -- ward / floor identifier
    notes         TEXT
);

CREATE INDEX mar_rounds_active_idx
    ON mar_rounds (clinic_id, started_at DESC)
    WHERE completed_at IS NULL;

-- mar_administration_events: the legal record. Append-only
-- (corrections via corrects_id). One row per administration attempt
-- (administered, refused, vomited, ...). Witness required for CDs at
-- DB level — never per-round.
CREATE TABLE mar_administration_events (
    id                              UUID PRIMARY KEY,
    clinic_id                       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    resident_id                     UUID NOT NULL REFERENCES subjects(id),
    prescription_id                 UUID NOT NULL REFERENCES mar_prescriptions(id),
    scheduled_dose_id               UUID REFERENCES mar_scheduled_doses(id),    -- NULL for PRN
    round_id                        UUID REFERENCES mar_rounds(id),

    actual_at                       TIMESTAMPTZ NOT NULL,
    actual_dose_qty                 NUMERIC(14,4),
    route                           TEXT,

    outcome_code                    mar_outcome_code NOT NULL,
    outcome_reason                  TEXT,

    administered_by                 UUID NOT NULL REFERENCES staff(id),
    witness_id                      UUID REFERENCES staff(id),
    notes                           TEXT,

    -- PRN-specific
    prn_indication_trigger          TEXT,
    prn_effectiveness               TEXT,
    prn_effectiveness_reviewed_at   TIMESTAMPTZ,

    -- Cross-domain link to drug_operations_log when the prescription
    -- is controlled and the outcome moves stock. Service writes both
    -- rows in one transaction.
    drug_op_id                      UUID REFERENCES drug_operations_log(id),

    -- Append-only: corrections happen via a NEW row with corrects_id
    -- pointing back to the original.
    corrects_id                     UUID REFERENCES mar_administration_events(id),

    -- Tamper-evident chain (per-resident-per-prescription chain — not
    -- per-clinic — so a resident's history is one logical chain).
    chain_key                       BYTEA,
    entry_seq_in_chain              BIGINT,
    prev_row_hash                   BYTEA,
    row_hash                        BYTEA,

    created_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT mar_event_outcome_reason_required
        CHECK (
            outcome_code = 'administered'
            OR outcome_code = 'partial'
            OR outcome_reason IS NOT NULL
        ),

    -- Per-event witness: when a CD is involved (drug_op_id set) we MUST
    -- have a witness on this exact row. Never a round-level signature.
    CONSTRAINT mar_event_cd_witness_required
        CHECK (drug_op_id IS NULL OR witness_id IS NOT NULL),

    -- An event row that REPRESENTS administration must have an actual
    -- dose qty. Other outcomes (refused, asleep) may have NULL.
    CONSTRAINT mar_event_administered_dose_present
        CHECK (
            (outcome_code <> 'administered' AND outcome_code <> 'partial')
            OR actual_dose_qty IS NOT NULL
        )
);

CREATE INDEX mar_admin_event_resident_idx
    ON mar_administration_events (clinic_id, resident_id, actual_at DESC);

CREATE INDEX mar_admin_event_prescription_idx
    ON mar_administration_events (prescription_id, actual_at DESC);

CREATE INDEX mar_admin_event_round_idx
    ON mar_administration_events (round_id, actual_at)
    WHERE round_id IS NOT NULL;

CREATE INDEX mar_admin_event_drug_op_idx
    ON mar_administration_events (drug_op_id)
    WHERE drug_op_id IS NOT NULL;

CREATE UNIQUE INDEX mar_admin_event_chain_seq_unique
    ON mar_administration_events (clinic_id, chain_key, entry_seq_in_chain)
    WHERE chain_key IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS mar_administration_events;
DROP TABLE IF EXISTS mar_rounds;
DROP TABLE IF EXISTS mar_scheduled_doses;
DROP TABLE IF EXISTS mar_prescriptions;
DROP TYPE IF EXISTS mar_outcome_code;

-- +goose StatementEnd
