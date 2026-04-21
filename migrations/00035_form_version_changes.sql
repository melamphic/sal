-- +goose Up
-- +goose StatementBegin

-- Structured per-publish change log — an array of typed ops the editor
-- computed by diffing the draft against the previous published version.
-- Display-only: rollback targets remain whole versions, not individual ops.
-- Stored as JSONB so new op types can be added without a migration.
ALTER TABLE form_versions
    ADD COLUMN changes JSONB NOT NULL DEFAULT '[]'::JSONB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE form_versions DROP COLUMN IF EXISTS changes;

-- +goose StatementEnd
