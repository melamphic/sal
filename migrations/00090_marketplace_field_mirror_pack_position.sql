-- +goose Up
-- +goose StatementBegin

-- Pack listings carry N forms in one version. The field mirror needs to
-- preserve which pack-form each field came from so detail / preview pages
-- can reconstruct per-form field lists without re-parsing the JSONB
-- package_payload. NULL for non-pack versions (single-form / policy_only).

ALTER TABLE marketplace_version_fields
    ADD COLUMN source_form_position INT;

CREATE INDEX idx_marketplace_version_fields_position
    ON marketplace_version_fields(marketplace_version_id, source_form_position)
    WHERE source_form_position IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_marketplace_version_fields_position;
ALTER TABLE marketplace_version_fields
    DROP COLUMN IF EXISTS source_form_position;

-- +goose StatementEnd
