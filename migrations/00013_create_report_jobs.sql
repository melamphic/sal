-- +goose Up

-- report_jobs tracks async compliance export requests.
-- The River worker writes the generated file to S3 and stores the key here.
-- The GET endpoint generates a fresh presigned URL from the key on demand.
CREATE TABLE report_jobs (
    id           UUID        PRIMARY KEY,
    clinic_id    UUID        NOT NULL,
    report_type  TEXT        NOT NULL, -- 'clinical_audit' | 'staff_actions' | 'note_history' | 'consent_log'
    format       TEXT        NOT NULL DEFAULT 'csv', -- 'csv' | 'pdf'
    status       TEXT        NOT NULL DEFAULT 'pending', -- 'pending' | 'complete' | 'failed'
    filters      JSONB,
    storage_key  TEXT,        -- S3 key of the generated file; set when status = 'complete'
    error_msg    TEXT,
    created_by   UUID        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX ON report_jobs (clinic_id, created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS report_jobs;
