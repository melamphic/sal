-- +goose Up
-- +goose StatementBegin

-- Compliance v2: add fields needed for UK / US / NZ / AU regulatory compliance
-- + tamper-evident chain. All columns NULLABLE so existing rows are not
-- broken; per-country NOT-NULL is enforced at service layer (see
-- internal/drugs/validators/).
--
-- Design: docs/drug-register-compliance-v2.md §4.1
--
-- Why drug_name / drug_strength / drug_form snapshots: the chain key
-- (per UK MDR 2001 Reg 20(1)(b) + NZ MDR 1977 Reg 37(2)(a)) is
-- "one page per drug × strength × form". Shelf rows are per-batch, so
-- they are too granular. We snapshot the page-level identity onto the
-- ledger row at insert time so the chain stays correct even if the
-- shelf row is archived or re-batched.
ALTER TABLE drug_operations_log
    -- Tamper-evident chain
    ADD COLUMN entry_seq                       BIGINT,
    ADD COLUMN entry_seq_in_chain              BIGINT,
    ADD COLUMN chain_key                       BYTEA,
    ADD COLUMN prev_row_hash                   BYTEA,
    ADD COLUMN row_hash                        BYTEA,

    -- Page-identity snapshot (drives chain_key, immutable per-row)
    ADD COLUMN drug_name                       TEXT,
    ADD COLUMN drug_strength                   TEXT,
    ADD COLUMN drug_form                       TEXT,

    -- Counterparty (UK Sch 6, US 1304, NZ Reg 40, AU state regs)
    ADD COLUMN counterparty_name               TEXT,
    ADD COLUMN counterparty_address            TEXT,
    ADD COLUMN counterparty_dea_number         TEXT,

    -- Prescriber identity snapshot (UK + NZ + AU)
    ADD COLUMN prescriber_name                 TEXT,
    ADD COLUMN prescriber_address              TEXT,
    ADD COLUMN prescriber_dea_number           TEXT,
    ADD COLUMN dea_registration_id             UUID,   -- FK added in 00070

    -- Patient address snapshot (UK + NZ + AU dispense to public)
    ADD COLUMN patient_address                 TEXT,

    -- UK collector (Reg 16 + Health Act 2006 amendments)
    ADD COLUMN collector_name                  TEXT,
    ADD COLUMN collector_id_evidence_requested BOOLEAN,
    ADD COLUMN collector_id_evidence_provided  BOOLEAN,

    -- Prescription / order references
    ADD COLUMN prescription_ref                TEXT,
    ADD COLUMN order_form_serial               TEXT,   -- US Form 222 / CSOS

    -- US container model (1304.11(e)(1)(iv)(A))
    ADD COLUMN commercial_container_count      INTEGER,
    ADD COLUMN units_per_container             NUMERIC(14,4),

    -- Batch + expiry snapshot at entry (AU explicit; others recommended)
    ADD COLUMN batch_number                    TEXT,
    ADD COLUMN expiry_date                     DATE,

    -- E-signature hash of the entered_by user (canonical-row SHA256)
    ADD COLUMN signature_hash                  BYTEA,

    -- Retention floor — derived from clinic country at insert
    ADD COLUMN retention_until                 DATE;

-- Per-chain sequential numbering: the regulator-facing "consecutively
-- numbered pages" model (NZ Reg 37(2)(a)). UNIQUE so insert-time
-- contention surfaces as a constraint violation rather than a silent
-- gap.
CREATE UNIQUE INDEX drug_operations_log_chain_seq_unique
    ON drug_operations_log (clinic_id, chain_key, entry_seq_in_chain)
    WHERE chain_key IS NOT NULL;

-- Retention purge worker scans this index daily.
CREATE INDEX drug_operations_log_retention_idx
    ON drug_operations_log (retention_until)
    WHERE retention_until IS NOT NULL;

-- Page-level lookups (regulator export filters by drug × strength × form).
CREATE INDEX drug_operations_log_page_idx
    ON drug_operations_log (clinic_id, drug_name, drug_strength, drug_form, created_at DESC)
    WHERE drug_name IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS drug_operations_log_page_idx;
DROP INDEX IF EXISTS drug_operations_log_retention_idx;
DROP INDEX IF EXISTS drug_operations_log_chain_seq_unique;

ALTER TABLE drug_operations_log
    DROP COLUMN IF EXISTS retention_until,
    DROP COLUMN IF EXISTS signature_hash,
    DROP COLUMN IF EXISTS expiry_date,
    DROP COLUMN IF EXISTS batch_number,
    DROP COLUMN IF EXISTS units_per_container,
    DROP COLUMN IF EXISTS commercial_container_count,
    DROP COLUMN IF EXISTS order_form_serial,
    DROP COLUMN IF EXISTS prescription_ref,
    DROP COLUMN IF EXISTS collector_id_evidence_provided,
    DROP COLUMN IF EXISTS collector_id_evidence_requested,
    DROP COLUMN IF EXISTS collector_name,
    DROP COLUMN IF EXISTS patient_address,
    DROP COLUMN IF EXISTS dea_registration_id,
    DROP COLUMN IF EXISTS prescriber_dea_number,
    DROP COLUMN IF EXISTS prescriber_address,
    DROP COLUMN IF EXISTS prescriber_name,
    DROP COLUMN IF EXISTS counterparty_dea_number,
    DROP COLUMN IF EXISTS counterparty_address,
    DROP COLUMN IF EXISTS counterparty_name,
    DROP COLUMN IF EXISTS drug_form,
    DROP COLUMN IF EXISTS drug_strength,
    DROP COLUMN IF EXISTS drug_name,
    DROP COLUMN IF EXISTS row_hash,
    DROP COLUMN IF EXISTS prev_row_hash,
    DROP COLUMN IF EXISTS chain_key,
    DROP COLUMN IF EXISTS entry_seq_in_chain,
    DROP COLUMN IF EXISTS entry_seq;

-- +goose StatementEnd
