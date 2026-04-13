-- +goose Up
-- +goose StatementBegin

-- vet_subject_details is the veterinary-specific extension to subjects.
-- It has a 1-to-1 relationship with subjects (subject_id is the PK).
-- Dental and aged care verticals will have their own extension tables added in Phase 2.
-- This table is NEVER modified when a new vertical is added — only new tables are created.
CREATE TABLE vet_subject_details (
    subject_id    UUID           PRIMARY KEY REFERENCES subjects(id),
    species       VARCHAR        NOT NULL
                                 CHECK (species IN ('dog', 'cat', 'bird', 'rabbit', 'reptile', 'other')),
    breed         TEXT,
    sex           VARCHAR        CHECK (sex IN ('male', 'female', 'unknown')),
    desexed       BOOLEAN,
    date_of_birth DATE,
    color         TEXT,
    -- Microchip is a device identifier — not PII, stored unencrypted for searchability.
    microchip     TEXT,
    weight_kg     NUMERIC(6, 2),
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- Microchip lookup is a common clinical operation.
CREATE INDEX idx_vet_subject_details_microchip ON vet_subject_details(microchip) WHERE microchip IS NOT NULL;

CREATE TRIGGER vet_subject_details_updated_at
    BEFORE UPDATE ON vet_subject_details
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS vet_subject_details_updated_at ON vet_subject_details;
DROP TABLE IF EXISTS vet_subject_details;

-- +goose StatementEnd
