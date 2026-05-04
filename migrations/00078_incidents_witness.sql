-- +goose Up
-- +goose StatementBegin

-- Bring incident_events in line with drug_operations_log (00074) +
-- consent_records (00077): the 4-mode witness pattern. Incidents
-- often need a structured second-pair-of-eyes (medication error
-- witnessed by another nurse, fall observed by a colleague, etc) —
-- the existing free-text `witnesses_text` field is a narrative
-- complement and stays.
--
-- Modes mirror drugs/consent:
--   - staff:    sync internal witness (witness_id set)
--   - pending:  async via approvals (already supported via
--               submit_for_review)
--   - external: sync paper-trail (non-Salvia user)
--   - self:     emergency / solo, attestation only — flagged on
--               regulator export
ALTER TABLE incident_events
    ADD COLUMN witness_id            UUID REFERENCES staff(id),
    ADD COLUMN witness_kind          TEXT,
    ADD COLUMN external_witness_name TEXT,
    ADD COLUMN external_witness_role TEXT,
    ADD COLUMN witness_attestation   TEXT;

ALTER TABLE incident_events
    ADD CONSTRAINT incident_events_witness_kind_check
    CHECK (
        witness_kind IS NULL
        OR witness_kind IN ('staff', 'pending', 'external', 'self')
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE incident_events
    DROP CONSTRAINT IF EXISTS incident_events_witness_kind_check;
ALTER TABLE incident_events
    DROP COLUMN IF EXISTS witness_attestation,
    DROP COLUMN IF EXISTS external_witness_role,
    DROP COLUMN IF EXISTS external_witness_name,
    DROP COLUMN IF EXISTS witness_kind,
    DROP COLUMN IF EXISTS witness_id;
-- +goose StatementEnd
