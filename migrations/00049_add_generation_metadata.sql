-- +goose Up
-- +goose StatementBegin

-- AI-generation metadata for forms and policies. JSONB so the shape
-- (provider/model/prompt_hash/staff_id/timestamps + repair counts) can
-- evolve without further migrations. NULL means "not AI-drafted".
--
-- Carried by the version row, not the entity row, because metadata is per
-- generation event — a form may have one AI-drafted version and many
-- subsequent human-authored versions.
ALTER TABLE form_versions
    ADD COLUMN generation_metadata JSONB;

ALTER TABLE policy_versions
    ADD COLUMN generation_metadata JSONB;

-- Hardening: form_fields.position must be positive and unique within a
-- version. The aigen repair pass ensures this on AI output, but defending
-- in depth at the DB catches any caller (manual editor, marketplace import,
-- AI gen) that lets a bad value slip past the service layer.
ALTER TABLE form_fields
    ADD CONSTRAINT form_fields_position_positive CHECK (position > 0);

CREATE UNIQUE INDEX IF NOT EXISTS form_fields_version_position_unique
    ON form_fields (form_version_id, position);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS form_fields_version_position_unique;

ALTER TABLE form_fields
    DROP CONSTRAINT IF EXISTS form_fields_position_positive;

ALTER TABLE policy_versions
    DROP COLUMN IF EXISTS generation_metadata;

ALTER TABLE form_versions
    DROP COLUMN IF EXISTS generation_metadata;

-- +goose StatementEnd
