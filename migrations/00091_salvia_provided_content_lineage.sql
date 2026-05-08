-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════════════════
-- Salvia-provided content lineage: tracks Salvia-authored forms/policies that
-- were materialised into a clinic's tenant tables at signup. Mirrors the
-- marketplace lineage pattern (00088) so the same upgrade UX (siblings,
-- "v1.1 available" banner) can be reused for Salvia-track content.
--
-- Lifecycle states:
--   default — clinic has the unmodified Salvia template; receives upgrade prompts
--   forked  — clinic edited; lineage retained but clinic owns the future
--   deleted — clinic explicitly removed; not re-created on subsequent re-syncs
--
-- The id is the YAML id (e.g. "salvia.shared.consultation_note") — text, not
-- UUID, because the catalogue is shipped in code (not a DB row) and must be
-- stable across clinic tenants.
-- ═══════════════════════════════════════════════════════════════════════════════

-- ── forms ─────────────────────────────────────────────────────────────────────
ALTER TABLE forms
    ADD COLUMN salvia_template_id       TEXT,
    ADD COLUMN salvia_template_version  INT,
    ADD COLUMN salvia_template_state    VARCHAR(16)
        CHECK (salvia_template_state IS NULL
               OR salvia_template_state IN ('default', 'forked', 'deleted')),
    ADD COLUMN framework_currency_date  DATE;

-- "Does this clinic already have template X (live or deleted)?" — covers the
-- materialiser idempotency check on re-sync.
CREATE UNIQUE INDEX idx_forms_salvia_template_per_clinic
    ON forms(clinic_id, salvia_template_id)
    WHERE salvia_template_id IS NOT NULL;

-- "What clinics are still on v1 of template X?" — for upgrade rollout.
CREATE INDEX idx_forms_salvia_template_version
    ON forms(salvia_template_id, salvia_template_version)
    WHERE salvia_template_id IS NOT NULL;

-- ── policies ──────────────────────────────────────────────────────────────────
ALTER TABLE policies
    ADD COLUMN salvia_template_id       TEXT,
    ADD COLUMN salvia_template_version  INT,
    ADD COLUMN salvia_template_state    VARCHAR(16)
        CHECK (salvia_template_state IS NULL
               OR salvia_template_state IN ('default', 'forked', 'deleted')),
    ADD COLUMN framework_currency_date  DATE;

CREATE UNIQUE INDEX idx_policies_salvia_template_per_clinic
    ON policies(clinic_id, salvia_template_id)
    WHERE salvia_template_id IS NOT NULL;

CREATE INDEX idx_policies_salvia_template_version
    ON policies(salvia_template_id, salvia_template_version)
    WHERE salvia_template_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_policies_salvia_template_version;
DROP INDEX IF EXISTS idx_policies_salvia_template_per_clinic;
ALTER TABLE policies
    DROP COLUMN IF EXISTS framework_currency_date,
    DROP COLUMN IF EXISTS salvia_template_state,
    DROP COLUMN IF EXISTS salvia_template_version,
    DROP COLUMN IF EXISTS salvia_template_id;

DROP INDEX IF EXISTS idx_forms_salvia_template_version;
DROP INDEX IF EXISTS idx_forms_salvia_template_per_clinic;
ALTER TABLE forms
    DROP COLUMN IF EXISTS framework_currency_date,
    DROP COLUMN IF EXISTS salvia_template_state,
    DROP COLUMN IF EXISTS salvia_template_version,
    DROP COLUMN IF EXISTS salvia_template_id;

-- +goose StatementEnd
