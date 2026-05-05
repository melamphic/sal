-- +goose Up
-- +goose StatementBegin

-- The original 00056 CHECK `consent_verbal_requires_witness` predates
-- the 4-mode witness model added in 00077 (staff / pending / external /
-- self). It requires witness_id NOT NULL whenever captured_via =
-- 'verbal_clinic', which trips a CHECK violation for every non-staff
-- mode — the service correctly skips witness_id for pending/external/
-- self, but the constraint rejects the row at DB level → 500.
--
-- Replaces it with a shape check mirroring drug_op_witness_kind_shape_check
-- (00074): witness_id is required only for staff (and the legacy
-- NULL-witness_kind path that callers still take when witness_kind isn't
-- provided AND captured_via='verbal_clinic'). The other modes carry
-- their own evidence (approvals row / external name / attestation).
ALTER TABLE consent_records
    DROP CONSTRAINT IF EXISTS consent_verbal_requires_witness;

ALTER TABLE consent_records
    ADD CONSTRAINT consent_records_witness_shape_check
    CHECK (
        captured_via <> 'verbal_clinic'
        OR witness_kind IS NOT NULL
        OR witness_id IS NOT NULL
    );

ALTER TABLE consent_records
    ADD CONSTRAINT consent_records_witness_kind_shape_check
    CHECK (
        witness_kind IS NULL
        OR (witness_kind = 'staff'    AND witness_id IS NOT NULL)
        OR (witness_kind = 'pending')
        OR (witness_kind = 'external' AND external_witness_name IS NOT NULL AND external_witness_name <> '')
        OR (witness_kind = 'self'     AND witness_attestation   IS NOT NULL AND witness_attestation   <> '')
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE consent_records DROP CONSTRAINT IF EXISTS consent_records_witness_kind_shape_check;
ALTER TABLE consent_records DROP CONSTRAINT IF EXISTS consent_records_witness_shape_check;

ALTER TABLE consent_records
    ADD CONSTRAINT consent_verbal_requires_witness
    CHECK (captured_via <> 'verbal_clinic' OR witness_id IS NOT NULL);
-- +goose StatementEnd
