-- +goose Up
-- +goose StatementBegin

-- Compliance v2: DEA registration records for US clinics + practitioners.
-- 21 CFR 1304 requires DEA registration numbers on every Sch II receive
-- (Form 222 / CSOS) and dispense. We model this as a separate table
-- because real US clinics + practitioners commonly hold multiple
-- registrations (different sites, different schedule authorities).
--
-- owner_type discriminates clinic-level vs staff-level registrations.
-- schedules_authorized is the list of DEA schedules ('II','III','IV','V')
-- the registration covers.
--
-- This table is US-only data; non-US clinics will have zero rows here.
--
-- Design: docs/drug-register-compliance-v2.md §4.5
CREATE TABLE dea_registrations (
    id                    UUID PRIMARY KEY,
    clinic_id             UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,

    owner_type            TEXT NOT NULL CHECK (owner_type IN ('clinic','staff')),
    -- owner_id references either clinics(id) or staff(id) depending on
    -- owner_type. Polymorphic FKs are not enforced at DB level — service
    -- layer validates the reference matches owner_type.
    owner_id              UUID NOT NULL,

    registration_number   TEXT NOT NULL,
    schedules_authorized  TEXT[] NOT NULL CHECK (cardinality(schedules_authorized) > 0),

    expires_at            DATE,
    archived_at           TIMESTAMPTZ
);

-- A registration is unique per (clinic, owner, number).
CREATE UNIQUE INDEX dea_registrations_unique_active_idx
    ON dea_registrations (clinic_id, owner_type, owner_id, registration_number)
    WHERE archived_at IS NULL;

CREATE INDEX dea_registrations_owner_idx
    ON dea_registrations (clinic_id, owner_type, owner_id)
    WHERE archived_at IS NULL;

CREATE INDEX dea_registrations_expiry_idx
    ON dea_registrations (clinic_id, expires_at)
    WHERE archived_at IS NULL AND expires_at IS NOT NULL;

-- Wire drug_operations_log.dea_registration_id ↔ dea_registrations.id.
-- The column was added in 00066 without an FK so this migration owns
-- the constraint. NULL is fine for non-US clinics.
ALTER TABLE drug_operations_log
    ADD CONSTRAINT drug_operations_log_dea_registration_fk
    FOREIGN KEY (dea_registration_id) REFERENCES dea_registrations(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE drug_operations_log
    DROP CONSTRAINT IF EXISTS drug_operations_log_dea_registration_fk;

DROP TABLE IF EXISTS dea_registrations;

-- +goose StatementEnd
