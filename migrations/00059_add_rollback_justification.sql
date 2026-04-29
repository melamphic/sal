-- +goose Up
-- +goose StatementBegin

-- Audit-grade rollback justification on form_versions and policy_versions.
-- Existing rollback flow set rollback_of (target version) but not WHY.
-- Regulator review of "you rolled back this policy on date X" expects
-- a documented reason and an approver.
--
-- The fields are nullable for backwards compatibility — existing
-- rollback rows pre-this-migration have no reason. Going forward, the
-- service layer requires both on every rollback.
ALTER TABLE form_versions
    ADD COLUMN rollback_reason       TEXT,
    ADD COLUMN rollback_approved_by  UUID REFERENCES staff(id);

ALTER TABLE policy_versions
    ADD COLUMN rollback_reason       TEXT,
    ADD COLUMN rollback_approved_by  UUID REFERENCES staff(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE form_versions
    DROP COLUMN IF EXISTS rollback_reason,
    DROP COLUMN IF EXISTS rollback_approved_by;

ALTER TABLE policy_versions
    DROP COLUMN IF EXISTS rollback_reason,
    DROP COLUMN IF EXISTS rollback_approved_by;

-- +goose StatementEnd
