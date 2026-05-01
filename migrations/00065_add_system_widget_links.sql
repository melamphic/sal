-- +goose Up
-- +goose StatementBegin

-- Field-precise traceability for compliance entities created via system
-- widgets on a form. Each compliance row already carries note_id, but a
-- single note can mount multiple system widgets (e.g. "audio recording
-- consent" + "treatment plan consent" on the same encounter), so we add
-- an optional link to the form_field that captured each entity.
--
-- Backfill is unnecessary — every compliance row created before this
-- migration was captured via the standalone modules, not via a form
-- field, so NULL is the correct legacy value.
ALTER TABLE consent_records
    ADD COLUMN note_field_id UUID REFERENCES form_fields(id);

ALTER TABLE drug_operations_log
    ADD COLUMN note_field_id UUID REFERENCES form_fields(id);

ALTER TABLE incident_events
    ADD COLUMN note_field_id UUID REFERENCES form_fields(id);

ALTER TABLE pain_scores
    ADD COLUMN note_field_id UUID REFERENCES form_fields(id);

CREATE INDEX consent_records_note_field_idx
    ON consent_records (note_field_id) WHERE note_field_id IS NOT NULL;
CREATE INDEX drug_operations_log_note_field_idx
    ON drug_operations_log (note_field_id) WHERE note_field_id IS NOT NULL;
CREATE INDEX incident_events_note_field_idx
    ON incident_events (note_field_id) WHERE note_field_id IS NOT NULL;
CREATE INDEX pain_scores_note_field_idx
    ON pain_scores (note_field_id) WHERE note_field_id IS NOT NULL;

-- ── Drug op confirm gate ────────────────────────────────────────────────
-- system.drug_op widgets pre-fill via AI but stay in pending_confirm until
-- the clinician taps Confirm. Backend rejects note submission while any
-- drug op for that note is still pending. Existing drug ops (created via
-- the manual modal) are 'confirmed' by default — they were always
-- explicit user actions.
ALTER TABLE drug_operations_log
    ADD COLUMN status TEXT NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('pending_confirm', 'confirmed')),
    ADD COLUMN confirmed_by UUID REFERENCES staff(id),
    ADD COLUMN confirmed_at TIMESTAMPTZ;

CREATE INDEX drug_operations_log_pending_confirm_idx
    ON drug_operations_log (clinic_id, status, created_at)
    WHERE status = 'pending_confirm';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS drug_operations_log_pending_confirm_idx;

ALTER TABLE drug_operations_log
    DROP COLUMN IF EXISTS confirmed_at,
    DROP COLUMN IF EXISTS confirmed_by,
    DROP COLUMN IF EXISTS status;

DROP INDEX IF EXISTS pain_scores_note_field_idx;
DROP INDEX IF EXISTS incident_events_note_field_idx;
DROP INDEX IF EXISTS drug_operations_log_note_field_idx;
DROP INDEX IF EXISTS consent_records_note_field_idx;

ALTER TABLE pain_scores         DROP COLUMN IF EXISTS note_field_id;
ALTER TABLE incident_events     DROP COLUMN IF EXISTS note_field_id;
ALTER TABLE drug_operations_log DROP COLUMN IF EXISTS note_field_id;
ALTER TABLE consent_records     DROP COLUMN IF EXISTS note_field_id;

-- +goose StatementEnd
