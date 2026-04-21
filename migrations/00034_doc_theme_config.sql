-- +goose Up
-- +goose StatementBegin

-- Extend clinic_form_style_versions with the rich doc-theme config used by the
-- designer (header/theme/body/footer/watermark/signature/page sections). The
-- existing flat columns are kept populated as a legacy mirror of the top-level
-- fields from the JSONB so older clients (step_pdf_brand.dart in onboarding)
-- keep working until cut over.
--
-- preset_id lets us remember which vertical-specific template the clinic
-- picked (e.g. "dental.clean_clinical") so exports can carry provenance and
-- the UI can highlight the active preset.

ALTER TABLE clinic_form_style_versions
    ADD COLUMN config    JSONB,
    ADD COLUMN preset_id TEXT,
    ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;

-- Backfill: only the highest-version row per clinic is active.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY clinic_id ORDER BY version DESC) AS rn
    FROM clinic_form_style_versions
)
UPDATE clinic_form_style_versions s
   SET is_active = (r.rn = 1)
  FROM ranked r
 WHERE s.id = r.id;

-- Partial unique index — at most one active row per clinic.
CREATE UNIQUE INDEX clinic_form_style_active_unique
    ON clinic_form_style_versions (clinic_id)
    WHERE is_active;

-- Fast lookup of active row.
CREATE INDEX idx_clinic_form_style_active
    ON clinic_form_style_versions (clinic_id)
    WHERE is_active;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_clinic_form_style_active;
DROP INDEX IF EXISTS clinic_form_style_active_unique;

ALTER TABLE clinic_form_style_versions
    DROP COLUMN IF EXISTS is_active,
    DROP COLUMN IF EXISTS preset_id,
    DROP COLUMN IF EXISTS config;

-- +goose StatementEnd
