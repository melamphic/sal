-- +goose Up
-- +goose StatementBegin

-- Clinic-specific drug entries that don't appear in the system-managed
-- master catalog (compounded drugs, custom formulations, locally
-- registered products). The master catalog ships as data files in
-- internal/drugs/catalog/; this table covers the long tail.
--
-- A clinic_drug_shelf row references either a catalog string id (for
-- system catalog entries) or a row from this table (for overrides).
CREATE TABLE clinic_drug_catalog_overrides (
    id                 UUID PRIMARY KEY,
    clinic_id          UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    active_ingredient  TEXT,
    schedule           TEXT,
    strength           TEXT,
    form               TEXT,
    brand_name         TEXT,
    notes              TEXT,
    created_by         UUID NOT NULL REFERENCES staff(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at        TIMESTAMPTZ
);

CREATE INDEX clinic_drug_catalog_overrides_clinic_idx
    ON clinic_drug_catalog_overrides (clinic_id)
    WHERE archived_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS clinic_drug_catalog_overrides;

-- +goose StatementEnd
