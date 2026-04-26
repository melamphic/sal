-- +goose Up
-- +goose StatementBegin

-- Structured per-publish change log for policies — mirrors form_versions.changes
-- (added in 00035). An array of typed ops the editor computed by diffing the
-- draft against the previous published version. Display-only: rollback
-- targets remain whole versions, not individual ops. JSONB so new op types
-- can be added without a migration.
ALTER TABLE policy_versions
    ADD COLUMN changes JSONB NOT NULL DEFAULT '[]'::JSONB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE policy_versions DROP COLUMN IF EXISTS changes;

-- +goose StatementEnd
