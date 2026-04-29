-- +goose Up
-- +goose StatementBegin

-- Periodic (typically monthly) reconciliation of physical drug counts
-- against the ledger. Required by VCNZ Code of Professional Conduct
-- (NZ vet) and analogous regulators elsewhere.
--
-- Two-staff signoff (reconciled_by_primary + reconciled_by_secondary)
-- is required for controlled drugs — enforced in service layer based on
-- the schedule of the shelf entry. Once committed, all
-- drug_operations_log rows in the period get reconciliation_id set,
-- locking them.
CREATE TABLE drug_reconciliation (
    id                       UUID PRIMARY KEY,
    clinic_id                UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    shelf_id                 UUID NOT NULL REFERENCES clinic_drug_shelf(id),

    period_start             TIMESTAMPTZ NOT NULL,
    period_end               TIMESTAMPTZ NOT NULL,

    physical_count           NUMERIC(14,4) NOT NULL,
    ledger_count             NUMERIC(14,4) NOT NULL,
    discrepancy              NUMERIC(14,4) GENERATED ALWAYS AS (physical_count - ledger_count) STORED,

    reconciled_by_primary    UUID NOT NULL REFERENCES staff(id),
    reconciled_by_secondary  UUID REFERENCES staff(id),

    status                   TEXT NOT NULL DEFAULT 'clean'
        CHECK (status IN ('clean','discrepancy_logged','reported_to_regulator')),
    discrepancy_explanation  TEXT,
    reported_at              TIMESTAMPTZ,
    reported_by              UUID REFERENCES staff(id),

    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT drug_reconciliation_period_valid
        CHECK (period_end > period_start),
    CONSTRAINT drug_reconciliation_discrepancy_explained
        CHECK (
            status = 'clean'
            OR discrepancy_explanation IS NOT NULL
        )
);

CREATE INDEX drug_reconciliation_clinic_idx
    ON drug_reconciliation (clinic_id, period_end DESC);

CREATE INDEX drug_reconciliation_shelf_idx
    ON drug_reconciliation (shelf_id, period_end DESC);

-- One reconciliation per (shelf, period_end). Re-running is via a fresh
-- period; correcting a closed reconciliation requires a discrepancy
-- escalation, not a re-insert.
CREATE UNIQUE INDEX drug_reconciliation_shelf_period_unique
    ON drug_reconciliation (shelf_id, period_end);

-- Now wire drug_operations_log.reconciliation_id ↔ drug_reconciliation.id.
ALTER TABLE drug_operations_log
    ADD CONSTRAINT drug_operations_log_reconciliation_fk
    FOREIGN KEY (reconciliation_id) REFERENCES drug_reconciliation(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE drug_operations_log
    DROP CONSTRAINT IF EXISTS drug_operations_log_reconciliation_fk;

DROP TABLE IF EXISTS drug_reconciliation;

-- +goose StatementEnd
