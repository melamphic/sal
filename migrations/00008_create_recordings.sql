-- +goose Up
-- +goose StatementBegin

-- recordings holds metadata for each audio recording submitted by a staff member.
-- The audio file itself is stored in S3-compatible object storage (MinIO in dev,
-- R2/S3 in prod). This table never holds binary data — only the storage key.
--
-- Lifecycle:
--   pending_upload → (client uploads to pre-signed URL) → uploaded
--   uploaded       → (River job picks up)               → transcribing
--   transcribing   → (Deepgram responds)                → transcribed | failed
CREATE TABLE recordings (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id   UUID        NOT NULL REFERENCES clinics(id),
    -- staff_id is who initiated the recording.
    staff_id    UUID        NOT NULL REFERENCES staff(id),
    -- subject_id is nullable — a recording can be made before a patient is selected,
    -- and linked later via PATCH /recordings/{id}.
    subject_id  UUID        REFERENCES subjects(id),
    -- status tracks the processing pipeline state.
    status      VARCHAR     NOT NULL DEFAULT 'pending_upload'
                            CHECK (status IN ('pending_upload','uploaded','transcribing','transcribed','failed')),
    -- file_key is the object storage path, e.g. "clinics/{clinic_id}/recordings/{id}.m4a".
    -- Always a UUID-based path — never expose this to clients directly.
    file_key    TEXT        NOT NULL,
    -- content_type is the MIME type the client declared on upload (e.g. "audio/mp4").
    -- Stored so the pre-signed download URL can set the correct Content-Type.
    content_type TEXT       NOT NULL DEFAULT 'audio/mp4',
    -- duration_seconds is populated from the Deepgram response after transcription.
    duration_seconds INTEGER,
    -- transcript holds the full plain-text transcript returned by Deepgram.
    -- Stored as TEXT (not JSONB) — the raw Deepgram JSON is not needed after extraction.
    transcript  TEXT,
    -- error_message holds the last failure reason for debugging, cleared on retry.
    error_message TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_recordings_clinic_id         ON recordings(clinic_id);
CREATE INDEX idx_recordings_staff_id          ON recordings(staff_id);
CREATE INDEX idx_recordings_subject_id        ON recordings(subject_id) WHERE subject_id IS NOT NULL;
CREATE INDEX idx_recordings_clinic_created_at ON recordings(clinic_id, created_at DESC);
CREATE INDEX idx_recordings_status            ON recordings(status) WHERE status IN ('uploaded','transcribing');

CREATE TRIGGER recordings_updated_at
    BEFORE UPDATE ON recordings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS recordings_updated_at ON recordings;
DROP TABLE IF EXISTS recordings;

-- +goose StatementEnd
