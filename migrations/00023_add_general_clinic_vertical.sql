-- +goose Up
-- +goose StatementBegin

-- Add 'general_clinic' as a supported vertical. Aged Care stays in the enum —
-- it is hidden from onboarding UI but architecture remains ready.
-- Per-vertical extension tables (general_subject_details) land in later migrations.

ALTER TABLE clinics DROP CONSTRAINT clinics_vertical_check;
ALTER TABLE clinics ADD CONSTRAINT clinics_vertical_check
    CHECK (vertical IN ('veterinary', 'dental', 'general_clinic', 'aged_care'));

ALTER TABLE subjects DROP CONSTRAINT subjects_vertical_check;
ALTER TABLE subjects ADD CONSTRAINT subjects_vertical_check
    CHECK (vertical IN ('veterinary', 'dental', 'general_clinic', 'aged_care'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Revert CHECK constraints. Any rows with vertical='general_clinic' will block
-- the down migration — operators must migrate those clinics off first.

ALTER TABLE subjects DROP CONSTRAINT subjects_vertical_check;
ALTER TABLE subjects ADD CONSTRAINT subjects_vertical_check
    CHECK (vertical IN ('veterinary', 'dental', 'aged_care'));

ALTER TABLE clinics DROP CONSTRAINT clinics_vertical_check;
ALTER TABLE clinics ADD CONSTRAINT clinics_vertical_check
    CHECK (vertical IN ('veterinary', 'dental', 'aged_care'));

-- +goose StatementEnd
