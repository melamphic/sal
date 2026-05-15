-- +goose Up
-- +goose StatementBegin

-- Photo (and future document) attachments on a note. Vets/nurses
-- attach exam photos, wound progression shots, lab strip captures,
-- consent screenshots, etc. to the note that documents the encounter.
--
-- Mirrors drug_op_attachments (migration 00069) — same shape, same
-- soft-delete + extracted-payload columns — so the FE attachment
-- widgets stay reusable across both surfaces.
--
-- note_id is NOT NULL: attachments always belong to an existing note.
-- (Drug ops differ because vial photos can be uploaded pre-confirm;
-- note flow has no equivalent pending state.)
--
-- archived_at is the soft-delete sentinel. Submitted-note attachments
-- can only be archived by a user with manageNotes; pre-submit, the
-- uploader can also delete. Archived rows stay for audit retention.
--
-- extracted_payload is reserved for future OCR / vision-AI work
-- (clinical findings, drug-label readouts). v1 leaves it NULL.
CREATE TABLE note_attachments (
    id            UUID PRIMARY KEY,
    clinic_id     UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    note_id       UUID NOT NULL REFERENCES notes(id) ON DELETE RESTRICT,

    kind          TEXT NOT NULL
        CHECK (kind IN ('photo','document','other')),

    s3_key        TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    bytes         BIGINT NOT NULL CHECK (bytes >= 0),

    extracted_payload JSONB,

    uploaded_by   UUID NOT NULL REFERENCES staff(id),
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at   TIMESTAMPTZ
);

CREATE INDEX note_attachments_note_idx
    ON note_attachments (note_id, uploaded_at DESC)
    WHERE archived_at IS NULL;

CREATE INDEX note_attachments_clinic_idx
    ON note_attachments (clinic_id, uploaded_at DESC)
    WHERE archived_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS note_attachments;

-- +goose StatementEnd
