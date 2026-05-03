-- +goose Up
-- Solo / small-practice clinicians frequently can't satisfy the original
-- "witness must be a staff record with Dispense permission" rule (a vet
-- alone in a rural NZ clinic at 2am, an aged-care home with one RN on
-- shift). MAJOR_ISSUES.md item 2.1 flagged this as customer-blocking.
--
-- The fix lets a witness fall into one of three kinds:
--   * staff    — the existing path (witnessed_by FK to staff(id))
--   * external — a real witness who isn't a Salvia user (vet nurse,
--                receptionist, family member, another vet visiting). We
--                record their name + role + a written attestation.
--   * self     — emergency / no-witness-available path. Mandatory written
--                attestation explaining why; surfaced on the regulator
--                report as a flagged row so an auditor can review every
--                self-witnessed CD operation.
--
-- All four columns are NULLable; the service layer enforces the per-kind
-- shape (e.g. external requires both name and attestation). Existing
-- rows backfill as kind='staff' when witnessed_by IS NOT NULL.
ALTER TABLE drug_operations_log
    ADD COLUMN witness_kind            TEXT,
    ADD COLUMN external_witness_name   TEXT,
    ADD COLUMN external_witness_role   TEXT,
    ADD COLUMN witness_attestation     TEXT;

UPDATE drug_operations_log
   SET witness_kind = 'staff'
 WHERE witnessed_by IS NOT NULL
   AND witness_kind IS NULL;

-- Witness-kind enum constraint enforced after backfill.
ALTER TABLE drug_operations_log
    ADD CONSTRAINT drug_op_witness_kind_check
    CHECK (
        witness_kind IS NULL
        OR witness_kind IN ('staff', 'external', 'self')
    );

-- Coherence: external requires the name; staff requires witnessed_by.
-- self has no FK or external name, only the attestation.
ALTER TABLE drug_operations_log
    ADD CONSTRAINT drug_op_witness_kind_shape_check
    CHECK (
        witness_kind IS NULL
        OR (witness_kind = 'staff' AND witnessed_by IS NOT NULL)
        OR (witness_kind = 'external' AND external_witness_name IS NOT NULL AND external_witness_name <> '')
        OR (witness_kind = 'self' AND witness_attestation IS NOT NULL AND witness_attestation <> '')
    );

-- +goose Down
ALTER TABLE drug_operations_log DROP CONSTRAINT IF EXISTS drug_op_witness_kind_shape_check;
ALTER TABLE drug_operations_log DROP CONSTRAINT IF EXISTS drug_op_witness_kind_check;
ALTER TABLE drug_operations_log
    DROP COLUMN witness_kind,
    DROP COLUMN external_witness_name,
    DROP COLUMN external_witness_role,
    DROP COLUMN witness_attestation;
