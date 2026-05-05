-- +goose Up
-- +goose StatementBegin

-- Bring consent_records in line with drug_operations_log (00074): the
-- four witness modes — staff (sync internal), pending (async via
-- approvals queue, already supported via submit_for_review),
-- external (sync paper-trail, non-Salvia user), self (emergency,
-- attestation only). Lets the consent capture flow drop "No eligible
-- staff" dead-ends when the only available witness is the signer.
--
-- All columns NULL by default — pre-existing rows ride along with no
-- migration backfill needed; the service layer maps witness_id-only
-- callers to witness_kind='staff' on read.
ALTER TABLE consent_records
    ADD COLUMN witness_kind          TEXT,
    ADD COLUMN external_witness_name TEXT,
    ADD COLUMN external_witness_role TEXT,
    ADD COLUMN witness_attestation   TEXT;

-- Sanity CHECK — values aligned with drugs: 'staff' | 'pending' |
-- 'external' | 'self'. NULL = legacy unset (treated as staff or no
-- witness depending on consent type).
ALTER TABLE consent_records
    ADD CONSTRAINT consent_records_witness_kind_check
    CHECK (
        witness_kind IS NULL
        OR witness_kind IN ('staff', 'pending', 'external', 'self')
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE consent_records
    DROP CONSTRAINT IF EXISTS consent_records_witness_kind_check;
ALTER TABLE consent_records
    DROP COLUMN IF EXISTS witness_attestation,
    DROP COLUMN IF EXISTS external_witness_role,
    DROP COLUMN IF EXISTS external_witness_name,
    DROP COLUMN IF EXISTS witness_kind;
-- +goose StatementEnd
