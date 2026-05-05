-- +goose Up
-- +goose StatementBegin

-- Per-document-type style overrides for the doc-theme module.
--
-- Today the theme is global to the clinic — every PDF (signed clinical
-- note, CD register, incident report, MAR, audit pack) renders against
-- the single config blob in clinic_form_style_versions.config. That's
-- fine until a clinic wants the regulator-submission docs (CD register,
-- accreditation bundle) to drop the brand flourishes a clinical note
-- carries (slim header, no watermark logo, mono footer) — common
-- request from compliance officers.
--
-- This column stores partial overrides keyed by doc-type slug. Each
-- value is a partial DocThemeConfig that the renderer merges over the
-- base config for that doc-type. NULL or missing key → use base config.
--
-- Shape:
--
--   {
--     "cd_register":           { "header": { "shape": "flat" }, "watermark": { "kind": "none" } },
--     "incident_report":       { "watermark": { "kind": "text", "text": "REGULATOR COPY" } },
--     "accreditation_bundle":  { "page": { "size": "a4", "margin_mm": 25 } }
--   }
--
-- Doc-type slugs come from the renderer (signed_note, cd_register,
-- incident_report, cd_reconciliation, pain_trend, mar, audit_pack, ...).
--
-- Default '{}' so existing rows don't violate NOT NULL.
ALTER TABLE clinic_form_style_versions
    ADD COLUMN per_doc_overrides JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE clinic_form_style_versions
    DROP COLUMN IF EXISTS per_doc_overrides;
-- +goose StatementEnd
