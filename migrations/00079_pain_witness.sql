-- +goose Up
-- +goose StatementBegin

-- Bring pain_scores in line with the 4-mode witness pattern shipped
-- on drug_operations_log (00074), consent_records (00077), and
-- incident_events (00078). Most pain scores are routine observations
-- and don't need a witness — but PRN-driven pain assessments that
-- gate controlled-drug administration (cancer pain, palliative care)
-- carry a regulator-binding signal, and the FE needs the same widget
-- everywhere a witness might apply.
--
-- All columns NULLable; service treats absent witness fields as
-- "not required" for routine scores.
ALTER TABLE pain_scores
    ADD COLUMN witness_id            UUID REFERENCES staff(id),
    ADD COLUMN witness_kind          TEXT,
    ADD COLUMN external_witness_name TEXT,
    ADD COLUMN external_witness_role TEXT,
    ADD COLUMN witness_attestation   TEXT;

ALTER TABLE pain_scores
    ADD CONSTRAINT pain_scores_witness_kind_check
    CHECK (
        witness_kind IS NULL
        OR witness_kind IN ('staff', 'pending', 'external', 'self')
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pain_scores
    DROP CONSTRAINT IF EXISTS pain_scores_witness_kind_check;
ALTER TABLE pain_scores
    DROP COLUMN IF EXISTS witness_attestation,
    DROP COLUMN IF EXISTS external_witness_role,
    DROP COLUMN IF EXISTS external_witness_name,
    DROP COLUMN IF EXISTS witness_kind,
    DROP COLUMN IF EXISTS witness_id;
-- +goose StatementEnd
