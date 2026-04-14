-- +goose Up
-- +goose StatementBegin

-- Add review, archival, and form-version-context tracking to notes.
-- reviewed_by/reviewed_at: human-in-the-loop acknowledgement before submission.
-- archived_at: soft delete — archived notes hidden from timeline unless filter applied.
-- form_version_context: label set at submit time if the linked form version was
--   retired or rolled back ("before decommission").

ALTER TABLE notes
    ADD COLUMN reviewed_by          UUID        REFERENCES staff(id),
    ADD COLUMN reviewed_at          TIMESTAMPTZ,
    ADD COLUMN archived_at          TIMESTAMPTZ,
    ADD COLUMN form_version_context TEXT;

-- Make recording_id nullable to support manual notes (no audio, no AI).
ALTER TABLE notes
    ALTER COLUMN recording_id DROP NOT NULL;

-- Replace unique index — only enforce uniqueness when a recording is linked.
-- NULL recording_ids (manual notes) do not conflict with each other.
DROP INDEX IF EXISTS idx_notes_recording_form;
CREATE UNIQUE INDEX idx_notes_recording_form
    ON notes(recording_id, form_version_id)
    WHERE recording_id IS NOT NULL;

-- Add index to efficiently filter non-archived notes (the common case).
CREATE INDEX idx_notes_archived ON notes(clinic_id, archived_at)
    WHERE archived_at IS NULL;

-- transformation_type: how the AI derived the value.
-- "direct" = verbatim or near-verbatim from transcript.
-- "inference" = derived/computed from context.
-- NULL for manual staff overrides and skippable fields.
ALTER TABLE note_fields
    ADD COLUMN transformation_type VARCHAR(20)
        CHECK (transformation_type IN ('direct', 'inference'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE note_fields DROP COLUMN transformation_type;

DROP INDEX IF EXISTS idx_notes_archived;
DROP INDEX IF EXISTS idx_notes_recording_form;
CREATE UNIQUE INDEX idx_notes_recording_form ON notes(recording_id, form_version_id);

ALTER TABLE notes ALTER COLUMN recording_id SET NOT NULL;

ALTER TABLE notes
    DROP COLUMN reviewed_by,
    DROP COLUMN reviewed_at,
    DROP COLUMN archived_at,
    DROP COLUMN form_version_context;

-- +goose StatementEnd
