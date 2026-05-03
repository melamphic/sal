-- +goose Up
-- Submitted notes are read-only by default. The override-unlock flow lets
-- the original creator (or a staff manager) re-open a submitted note for
-- a justified correction. Each unlock records who unlocked it, why, and
-- when; once committed the count increments and the note returns to
-- submitted. The pre-existing override_reason* columns are submit-time
-- policy overrides — different concept, different lifecycle.
ALTER TABLE notes
    ADD COLUMN override_unlocked_at     TIMESTAMPTZ,
    ADD COLUMN override_unlocked_by     UUID REFERENCES staff(id),
    ADD COLUMN override_unlocked_reason TEXT,
    ADD COLUMN override_count           INT NOT NULL DEFAULT 0;

-- Allow status='overriding' so the gate code knows the note is in an
-- editable, post-submit state. Existing rows stay unaffected.
ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_status_check;
ALTER TABLE notes
    ADD CONSTRAINT notes_status_check
    CHECK (status IN ('extracting', 'draft', 'submitted', 'failed', 'overriding'));

-- +goose Down
ALTER TABLE notes DROP CONSTRAINT IF EXISTS notes_status_check;
ALTER TABLE notes
    ADD CONSTRAINT notes_status_check
    CHECK (status IN ('extracting', 'draft', 'submitted', 'failed'));

ALTER TABLE notes
    DROP COLUMN override_unlocked_at,
    DROP COLUMN override_unlocked_by,
    DROP COLUMN override_unlocked_reason,
    DROP COLUMN override_count;
