-- +goose Up
ALTER TABLE clinics ADD COLUMN logo_key            TEXT;
ALTER TABLE clinics ADD COLUMN accent_color        TEXT;
ALTER TABLE clinics ADD COLUMN pdf_header_text     TEXT;
ALTER TABLE clinics ADD COLUMN pdf_footer_text     TEXT;
ALTER TABLE clinics ADD COLUMN pdf_primary_color   TEXT;
ALTER TABLE clinics ADD COLUMN pdf_font            TEXT;
ALTER TABLE clinics ADD COLUMN onboarding_step     SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE clinics ADD COLUMN onboarding_complete BOOLEAN  NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE clinics DROP COLUMN onboarding_complete;
ALTER TABLE clinics DROP COLUMN onboarding_step;
ALTER TABLE clinics DROP COLUMN pdf_font;
ALTER TABLE clinics DROP COLUMN pdf_primary_color;
ALTER TABLE clinics DROP COLUMN pdf_footer_text;
ALTER TABLE clinics DROP COLUMN pdf_header_text;
ALTER TABLE clinics DROP COLUMN accent_color;
ALTER TABLE clinics DROP COLUMN logo_key;
