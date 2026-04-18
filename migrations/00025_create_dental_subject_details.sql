-- +goose Up
-- +goose StatementBegin

-- Per-vertical extension table for dental subjects.
-- Subjects in dental clinics are humans; demographics + clinical PHI live here.
-- PHI fields (medical_alerts, medications, allergies, chronic_conditions)
-- and PII (insurance_policy_number) are encrypted at the application layer
-- via crypto.Cipher (AES-256-GCM). Plain text: admission_warnings,
-- insurance_provider_name, referring_dentist_name, primary_dentist_name.

CREATE TABLE dental_subject_details (
    subject_id              UUID PRIMARY KEY REFERENCES subjects(id) ON DELETE CASCADE,
    date_of_birth           DATE,
    sex                     VARCHAR(10) CHECK (sex IN ('male','female','other','unknown')),
    -- PHI: encrypted — medical conditions flagged for safety.
    medical_alerts          TEXT,
    -- PHI: encrypted — current medications list.
    medications             TEXT,
    -- PHI: encrypted — allergy list with reactions.
    allergies               TEXT,
    -- PHI: encrypted — chronic conditions list.
    chronic_conditions      TEXT,
    -- Operational safety warnings. Not PHI.
    admission_warnings      TEXT,
    -- Insurance provider display name. Not PII.
    insurance_provider_name TEXT,
    -- PII: encrypted — policy number identifies the subscriber.
    insurance_policy_number TEXT,
    -- External referring dentist name. Low risk, plain text.
    referring_dentist_name  TEXT,
    -- Primary dentist on record (internal staff name display).
    primary_dentist_name    TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE dental_subject_details;

-- +goose StatementEnd
