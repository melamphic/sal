-- +goose Up
-- +goose StatementBegin

-- Adds localization + legal fields to support country-aware onboarding.
-- Phase 1 target is NZ veterinary; UK / AU / IN / additional verticals
-- follow the same schema with different default values.
--
-- business_reg_no is a generic business-registration identifier — rendered
-- as NZBN for NZ, CRN for UK, ABN for AU, GSTIN/PAN for IN. Stored as plain
-- text because these identifiers are publicly searchable (not PHI).
-- terms_accepted_at records when the clinic super-admin accepted the
-- Salvia terms of service during onboarding. Required before onboarding
-- can complete — enforced at the service layer.

ALTER TABLE clinics
    ADD COLUMN legal_name        TEXT,
    ADD COLUMN country           CHAR(2) NOT NULL DEFAULT 'NZ',
    ADD COLUMN timezone          TEXT    NOT NULL DEFAULT 'Pacific/Auckland',
    ADD COLUMN business_reg_no   TEXT,
    ADD COLUMN terms_accepted_at TIMESTAMPTZ;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE clinics
    DROP COLUMN terms_accepted_at,
    DROP COLUMN business_reg_no,
    DROP COLUMN timezone,
    DROP COLUMN country,
    DROP COLUMN legal_name;

-- +goose StatementEnd
