-- +goose Up
-- +goose StatementBegin

-- Compliance-module permission flags on staff. Each is independent
-- because regulator review of "who has X right" must answer per-perm.
--
-- Defaults are conservative: FALSE for everyone. Onboarding wizard
-- (or admin panel) flips them per role. Existing staff carry no new
-- perms automatically — they're explicitly granted by clinic owner.
--
-- Naming convention matches existing perm_* style on staff table.
ALTER TABLE staff
    -- Drug register
    ADD COLUMN perm_manage_drug_shelf            BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_dispense_controlled_drugs    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_witness_controlled_drugs     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_reconcile_drugs              BOOLEAN NOT NULL DEFAULT FALSE,

    -- Incidents
    ADD COLUMN perm_manage_incidents             BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_escalate_incidents           BOOLEAN NOT NULL DEFAULT FALSE,

    -- Consent
    ADD COLUMN perm_capture_consent              BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_withdraw_consent             BOOLEAN NOT NULL DEFAULT FALSE,

    -- Compliance reports (distinct from existing perm_generate_audit_export
    -- which gates the audit-pack mega-report specifically; these gate
    -- the per-report-type endpoints)
    ADD COLUMN perm_generate_compliance_report   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_download_compliance_report   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perm_share_report_externally      BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE staff
    DROP COLUMN IF EXISTS perm_manage_drug_shelf,
    DROP COLUMN IF EXISTS perm_dispense_controlled_drugs,
    DROP COLUMN IF EXISTS perm_witness_controlled_drugs,
    DROP COLUMN IF EXISTS perm_reconcile_drugs,
    DROP COLUMN IF EXISTS perm_manage_incidents,
    DROP COLUMN IF EXISTS perm_escalate_incidents,
    DROP COLUMN IF EXISTS perm_capture_consent,
    DROP COLUMN IF EXISTS perm_withdraw_consent,
    DROP COLUMN IF EXISTS perm_generate_compliance_report,
    DROP COLUMN IF EXISTS perm_download_compliance_report,
    DROP COLUMN IF EXISTS perm_share_report_externally;

-- +goose StatementEnd
