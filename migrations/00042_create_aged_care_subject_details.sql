-- +goose Up
-- +goose StatementBegin

-- Per-vertical extension table for aged-care subjects (residents).
-- Mirrors dental/general_clinic shape but carries fields specific to
-- residential and home-care aged care: cognitive/mobility/continence
-- status, funding level, advance directive flag, and NHI/Medicare
-- identifiers (PII, encrypted).
-- PHI fields (medical_alerts, medications, allergies, chronic_conditions,
-- diet_notes) and PII fields (nhi_number, medicare_number) are encrypted
-- at the application layer via crypto.Cipher (AES-256-GCM). Plain text:
-- room, ethnicity, preferred_language, admission_date, funding_level,
-- advance_directive_flag, cognitive_status, mobility_status,
-- continence_status, primary_gp_name.

CREATE TABLE aged_care_subject_details (
    subject_id              UUID PRIMARY KEY REFERENCES subjects(id) ON DELETE CASCADE,
    date_of_birth           DATE,
    sex                     VARCHAR(10) CHECK (sex IN ('male','female','other','unknown')),
    -- Room / bed identifier within the facility. Not PHI.
    room                    TEXT,
    -- PII: encrypted — NZ National Health Index number.
    nhi_number              TEXT,
    -- PII: encrypted — AU Medicare number.
    medicare_number         TEXT,
    ethnicity               TEXT,
    preferred_language      TEXT,
    -- PHI: encrypted — conditions flagged for safety.
    medical_alerts          TEXT,
    -- PHI: encrypted — current medications list.
    medications             TEXT,
    -- PHI: encrypted — allergy list with reactions.
    allergies               TEXT,
    -- PHI: encrypted — chronic conditions list.
    chronic_conditions      TEXT,
    -- Care-status enums. Plain text — surfaced in lists and care plans.
    cognitive_status        VARCHAR(30) CHECK (cognitive_status IN
        ('independent','mild_impairment','moderate_impairment','severe_impairment','unknown')),
    mobility_status         VARCHAR(30) CHECK (mobility_status IN
        ('independent','supervised','assisted','immobile','unknown')),
    continence_status       VARCHAR(30) CHECK (continence_status IN
        ('continent','urinary_incontinence','faecal_incontinence','double_incontinence','catheterised','unknown')),
    -- PHI: encrypted — dietary restrictions and texture modifications.
    diet_notes              TEXT,
    -- Whether an advance-care directive is on file. Plain text.
    advance_directive_flag  BOOLEAN NOT NULL DEFAULT FALSE,
    -- Funding / assessment level (NZ InterRAI, AU Home Care Packages).
    funding_level           VARCHAR(30) CHECK (funding_level IN
        ('home_care_1','home_care_2','home_care_3','home_care_4',
         'residential_low','residential_high','unfunded','unknown')),
    admission_date          DATE,
    primary_gp_name         TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE aged_care_subject_details;

-- +goose StatementEnd
