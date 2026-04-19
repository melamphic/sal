-- +goose Up
-- +goose StatementBegin

-- Subject access log. Records every read and write against a subject so we
-- have a durable audit trail for compliance (HIPAA §164.312(b), AU MHR,
-- GDPR Art. 30). Writes are append-only. Reads of sensitive PII fields
-- ("tap to reveal") are logged as action='unmask_pii' with an optional
-- purpose string captured at the UI.
--
-- NOTE: this table is intentionally per-clinic and NOT encrypted — it
-- stores only foreign keys and an action verb, never the content that
-- was viewed.

CREATE TABLE subject_access_log (
    id         UUID        PRIMARY KEY,
    subject_id UUID        NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    staff_id   UUID        NOT NULL REFERENCES staff(id) ON DELETE RESTRICT,
    clinic_id  UUID        NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    action     VARCHAR(20) NOT NULL CHECK (action IN ('view','create','update','archive','unmask_pii')),
    purpose    TEXT,
    at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_subject_access_log_subject_at
    ON subject_access_log (subject_id, at DESC);

CREATE INDEX idx_subject_access_log_clinic_at
    ON subject_access_log (clinic_id, at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE subject_access_log;

-- +goose StatementEnd
