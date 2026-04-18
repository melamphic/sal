-- +goose Up
-- +goose StatementBegin

-- Extend vet_subject_details with clinical safety and admin fields.
-- PHI fields (allergies, chronic_conditions) and PII (insurance_policy_number)
-- are encrypted at the application layer via crypto.Cipher (AES-256-GCM).
-- admission_warnings, insurance_provider_name, referring_vet_name are plain text.

ALTER TABLE vet_subject_details
    -- PHI: encrypted — free-text list of allergies with reactions.
    ADD COLUMN allergies               TEXT,
    -- PHI: encrypted — free-text list of chronic conditions.
    ADD COLUMN chronic_conditions      TEXT,
    -- Safety/behavioural flags (aggressive, bite history, etc). Operational, not PHI.
    ADD COLUMN admission_warnings      TEXT,
    -- Insurance provider display name. Not PII on its own.
    ADD COLUMN insurance_provider_name TEXT,
    -- PII: encrypted — policy number can identify an owner.
    ADD COLUMN insurance_policy_number TEXT,
    -- External person name — low risk, plain text for UI display.
    ADD COLUMN referring_vet_name      TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE vet_subject_details
    DROP COLUMN referring_vet_name,
    DROP COLUMN insurance_policy_number,
    DROP COLUMN insurance_provider_name,
    DROP COLUMN admission_warnings,
    DROP COLUMN chronic_conditions,
    DROP COLUMN allergies;

-- +goose StatementEnd
