-- +goose Up
-- +goose StatementBegin

-- Per-clinic inventory of stocked drugs ("shelf"). Each row is one
-- (drug × strength × batch × location) combination — same physical drug
-- in two batches is two rows.
--
-- One of catalog_id / override_drug_id is required (CHECK enforces
-- exactly-one). catalog_id references a string id from the system master
-- catalog (data files in internal/drugs/catalog/), no FK; override_drug_id
-- FKs into clinic_drug_catalog_overrides for clinic-defined drugs.
--
-- balance is the live on-hand count; recomputed on every operation log
-- insert via service-layer transaction. par_level is the reorder
-- threshold the dashboard uses to flag low stock.
CREATE TABLE clinic_drug_shelf (
    id                  UUID PRIMARY KEY,
    clinic_id           UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    catalog_id          TEXT,
    override_drug_id    UUID REFERENCES clinic_drug_catalog_overrides(id),
    strength            TEXT,
    form                TEXT,
    batch_number        TEXT,
    expiry_date         DATE,
    location            TEXT NOT NULL DEFAULT 'main',
    balance             NUMERIC(14,4) NOT NULL DEFAULT 0,
    unit                TEXT NOT NULL,
    par_level           NUMERIC(14,4),
    notes               TEXT,
    created_by          UUID NOT NULL REFERENCES staff(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at         TIMESTAMPTZ,

    -- Exactly-one of catalog_id / override_drug_id is required.
    CONSTRAINT shelf_drug_source_exclusive
        CHECK ((catalog_id IS NOT NULL) <> (override_drug_id IS NOT NULL))
);

CREATE INDEX clinic_drug_shelf_clinic_idx
    ON clinic_drug_shelf (clinic_id)
    WHERE archived_at IS NULL;

-- One active shelf entry per (clinic, drug, strength, batch, location).
-- Reordering a depleted batch creates a new row, not an update of the old.
CREATE UNIQUE INDEX clinic_drug_shelf_active_unique
    ON clinic_drug_shelf (
        clinic_id,
        COALESCE(catalog_id, ''),
        COALESCE(override_drug_id::text, ''),
        COALESCE(strength, ''),
        COALESCE(batch_number, ''),
        location
    )
    WHERE archived_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS clinic_drug_shelf;

-- +goose StatementEnd
