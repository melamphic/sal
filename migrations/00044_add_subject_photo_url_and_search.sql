-- +goose Up
-- +goose StatementBegin

-- photo_url is an optional externally-hosted image URL for the subject's
-- avatar. Kept nullable — frontends fall back to initials when missing.
-- Not treated as PII; the URL is public or signed by the caller.
ALTER TABLE subjects ADD COLUMN photo_url TEXT;

-- Trigram index on display_name to make patient search `?q=` cheap.
-- display_name is stored plaintext, so ILIKE '%foo%' can hit this index.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_subjects_display_name_trgm
    ON subjects USING gin (display_name gin_trgm_ops);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_subjects_display_name_trgm;
ALTER TABLE subjects DROP COLUMN IF EXISTS photo_url;

-- +goose StatementEnd
