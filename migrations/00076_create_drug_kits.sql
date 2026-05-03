-- +goose Up
-- +goose StatementBegin

-- Drug kits = clinic-defined bundles of drugs that get used together
-- repeatedly. Vet: spay pack (induction + analgesic + antibiotic +
-- reversal), dental pack, euthanasia pack. Aged care: PRN packs by
-- indication, end-of-life comfort pack. Dental: sedation tray,
-- post-op script bundle. GP: vaccine panel, minor procedure pack.
--
-- Cross-vertical research note (see project_drug_kits memory): every
-- vertical has a "treatment template" / "kit" / "favourites" concept
-- with the same shape — name + N line items + per-item default qty.
-- The "Use kit" action expands the template into N draft cart lines
-- the user can edit before checkout. Historical dispenses snapshot
-- the qty at the cart line level, not via versioning here — kits
-- evolve, but past dispenses are recorded at the operation_log row.
CREATE TABLE drug_kits (
    id            UUID PRIMARY KEY,
    clinic_id     UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    description   TEXT,
    use_context   TEXT, -- e.g. 'spay', 'dental_prophy', 'discharge', 'comfort_pack', 'vaccine_panel'
    created_by    UUID NOT NULL REFERENCES staff(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at   TIMESTAMPTZ
);

CREATE INDEX drug_kits_clinic_idx
    ON drug_kits (clinic_id)
    WHERE archived_at IS NULL;

CREATE TABLE drug_kit_items (
    id                 UUID PRIMARY KEY,
    kit_id             UUID NOT NULL REFERENCES drug_kits(id) ON DELETE CASCADE,
    position           INT  NOT NULL,
    -- Exactly one of catalog_id (system entry, string id) or
    -- override_drug_id (clinic-specific) is set per row.
    catalog_id         TEXT,
    override_drug_id   UUID REFERENCES clinic_drug_catalog_overrides(id) ON DELETE RESTRICT,
    default_quantity   NUMERIC(12,4),
    unit               TEXT NOT NULL,
    default_dose       TEXT,
    default_route      TEXT,
    -- 'administer' | 'dispense' | 'discard' | 'receive' | 'transfer'
    -- | 'adjust' — empty = let the user pick at use-time.
    default_operation  TEXT,
    notes              TEXT,
    -- When TRUE, the line is included in the cart at use-time but
    -- ticked off; the user can opt in. Useful for variable kits
    -- ("euthanasia pack: include sedative if anxious cat").
    is_optional        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((catalog_id IS NOT NULL) OR (override_drug_id IS NOT NULL)),
    CHECK (NOT (catalog_id IS NOT NULL AND override_drug_id IS NOT NULL))
);

CREATE INDEX drug_kit_items_kit_idx ON drug_kit_items (kit_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS drug_kit_items;
DROP TABLE IF EXISTS drug_kits;
-- +goose StatementEnd
