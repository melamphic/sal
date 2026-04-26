-- +goose Up
-- +goose StatementBegin

-- system_header_config: per-form-version config for the "patient header" card
-- that renders at the top of every note (review screen + PDF). The card's
-- values come from the linked subject/patient row at note-render time, NEVER
-- from AI extraction — extracting identity data wastes tokens and produces
-- false confidence on values the system already knows with certainty.
--
-- Shape: {"enabled": bool, "fields": ["name","photo","id","dob","age","sex",...]}
--   `fields` is the ordered list of patient-row attributes the form author
--   wants surfaced. The set of valid identifiers is enforced in app code, not
--   the DB, so adding new ones (e.g. a new aged_care identifier) ships
--   without a migration.
--
-- Default mirrors the prescription-card mock: name + photo + ID + DOB + age
-- + sex + visit date. Existing draft + published rows backfill to this
-- default so historical notes continue to render.

ALTER TABLE form_versions
    ADD COLUMN system_header_config JSONB NOT NULL
        DEFAULT '{"enabled":true,"fields":["name","photo","id","dob","age","sex","visit_date"]}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE form_versions DROP COLUMN IF EXISTS system_header_config;

-- +goose StatementEnd
