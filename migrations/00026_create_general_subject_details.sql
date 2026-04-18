-- +goose Up
-- +goose StatementBegin

-- Per-vertical extension table for general_clinic subjects (primary care, GP).
-- Subjects are human patients. Clinical PHI and insurance PII are encrypted
-- at the application layer via crypto.Cipher (AES-256-GCM).
-- Plain text: admission_warnings, insurance_provider_name,
-- referring_provider_name, primary_provider_name.

CREATE TABLE general_subject_details (
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
    -- Insurance/plan provider display name. Not PII.
    insurance_provider_name TEXT,
    -- PII: encrypted — member/policy number identifies the subscriber.
    insurance_policy_number TEXT,
    -- External referring provider name. Plain text.
    referring_provider_name TEXT,
    -- Primary care provider on record. Plain text.
    primary_provider_name   TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE general_subject_details;

-- +goose StatementEnd
