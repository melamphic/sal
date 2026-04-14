-- +goose Up
-- +goose StatementBegin

-- notes: one note = one form filled from one recording.
-- A recording may produce up to 3 notes (one per linked form).
-- status lifecycle: extracting → draft → submitted.
-- Extraction happens asynchronously via River; staff reviews draft then submits.
CREATE TABLE notes (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id       UUID        NOT NULL REFERENCES clinics(id),
    recording_id    UUID        NOT NULL REFERENCES recordings(id),
    form_version_id UUID        NOT NULL REFERENCES form_versions(id),
    subject_id      UUID        REFERENCES subjects(id),
    created_by      UUID        NOT NULL REFERENCES staff(id),
    status          VARCHAR     NOT NULL DEFAULT 'extracting'
                                CHECK (status IN ('extracting', 'draft', 'submitted', 'failed')),
    error_message   TEXT,
    submitted_at    TIMESTAMPTZ,
    submitted_by    UUID        REFERENCES staff(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One note per (recording, form_version) — no duplicate fills.
CREATE UNIQUE INDEX idx_notes_recording_form ON notes(recording_id, form_version_id);

-- Max 3 notes per recording enforced at service layer (not DB-level for simplicity).
CREATE INDEX idx_notes_clinic     ON notes(clinic_id, created_at DESC);
CREATE INDEX idx_notes_recording  ON notes(recording_id);
CREATE INDEX idx_notes_subject    ON notes(subject_id) WHERE subject_id IS NOT NULL;
CREATE INDEX idx_notes_created_by ON notes(created_by);
CREATE INDEX idx_notes_status     ON notes(clinic_id, status);

SELECT set_updated_at('notes');

-- note_fields: one row per field in the form version.
-- value is JSON-encoded (string, number, array — whatever the field type needs).
-- confidence: 0.00–1.00 from AI; NULL when field is skippable or manually set.
-- source_quote: snippet from transcript that the AI used for this value.
-- overridden: set when staff edits the AI-extracted value.
CREATE TABLE note_fields (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id         UUID        NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    field_id        UUID        NOT NULL REFERENCES form_fields(id),
    value           TEXT,       -- JSON-encoded; NULL = not yet extracted / skipped
    confidence      DECIMAL(5,2) CHECK (confidence >= 0 AND confidence <= 1),
    source_quote    TEXT,       -- excerpt from transcript supporting this value
    overridden_by   UUID        REFERENCES staff(id),
    overridden_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(note_id, field_id)
);

CREATE INDEX idx_note_fields_note ON note_fields(note_id);

SELECT set_updated_at('note_fields');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS note_fields;
DROP TABLE IF EXISTS notes;

-- +goose StatementEnd
