-- +goose Up
-- Generic second-pair-of-eyes ledger. Until now every system widget
-- (drug operation, consent, incident, pain score) needed a witness or
-- supervisor review present at the time of capture — a hard gate that
-- broke for solo / small / out-of-hours practice. The async-approval
-- model lets the original signer log the action immediately and a
-- second qualified person sign it off later from a queue. Each approval
-- row is append-only; status transitions emit timeline events so the
-- patient + clinic audit trail always reflects who saw what when.
--
-- The table is generic on `entity_kind` so the same plumbing serves
-- every system widget. Cross-domain FK isn't enforced (per CLAUDE.md
-- "never query another domain's tables"); the consuming domain is
-- responsible for keeping its `*_status` snapshot column in sync via
-- the ApprovalsService callback.
CREATE TABLE compliance_approvals (
    id              UUID PRIMARY KEY,
    clinic_id       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,

    entity_kind     TEXT NOT NULL
                    CHECK (entity_kind IN ('drug_op', 'consent', 'incident', 'pain_score')),
    entity_id       UUID NOT NULL,
    -- Optional sub-classifier: 'administer'/'discard' for drug ops, etc.
    -- Lets the queue page render the right verb without joining the
    -- entity table.
    entity_op       TEXT,

    status          TEXT NOT NULL
                    CHECK (status IN ('pending', 'approved', 'challenged'))
                    DEFAULT 'pending',

    -- Who submitted the entity (the original signer). Cannot be the
    -- decider — service layer enforces.
    submitted_by    UUID NOT NULL REFERENCES staff(id),
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    submitted_note  TEXT,

    -- Hard deadline. Default per-clinic 48h; configurable later.
    deadline_at     TIMESTAMPTZ NOT NULL,

    decided_by      UUID REFERENCES staff(id),
    decided_at      TIMESTAMPTZ,
    decided_comment TEXT,

    -- Subject linkage (when the entity is patient-bound) so the
    -- queue page + subject timeline can scope correctly without a
    -- cross-domain JOIN.
    subject_id      UUID REFERENCES subjects(id),

    -- Originating note (when materialised from a system widget) so
    -- the queue can deep-link back into the note review surface.
    note_id         UUID REFERENCES notes(id),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A decided row must have a decider + decided_at; pending row
    -- must have neither. Enforces the state-machine at the schema.
    CONSTRAINT compliance_approval_decision_shape_check
        CHECK (
            (status = 'pending' AND decided_by IS NULL AND decided_at IS NULL)
            OR
            (status IN ('approved', 'challenged')
                AND decided_by IS NOT NULL
                AND decided_at IS NOT NULL)
        )
);

-- One pending approval per entity at a time. Approve/Challenge transitions
-- away from pending so a fresh approval row can be created later (e.g.
-- after a challenge → addendum → re-submit).
CREATE UNIQUE INDEX compliance_approvals_entity_pending_idx
    ON compliance_approvals (entity_kind, entity_id)
    WHERE status = 'pending';

-- Queue lookup: pending rows for a clinic, ordered by deadline.
CREATE INDEX compliance_approvals_queue_idx
    ON compliance_approvals (clinic_id, status, deadline_at)
    WHERE status = 'pending';

-- Per-entity history (decided rows for audit trail rendering).
CREATE INDEX compliance_approvals_entity_idx
    ON compliance_approvals (entity_kind, entity_id, created_at DESC);

CREATE INDEX compliance_approvals_subject_idx
    ON compliance_approvals (subject_id, created_at DESC)
    WHERE subject_id IS NOT NULL;

CREATE TRIGGER compliance_approvals_updated_at
    BEFORE UPDATE ON compliance_approvals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- Per-entity snapshot columns. Each consuming domain stores the latest
-- approval state denormalised onto its main row so:
--   * the entity can render its current witness state without a JOIN
--   * the entity's domain owns the status (per CLAUDE.md cross-domain rule)
--   * regulator reports + queue queries hit a single table
--
-- Defaults: 'approved' for non-controlled drug ops (no witness needed)
-- and for non-witnessed entity types; backend service decides per-row.
ALTER TABLE drug_operations_log
    ADD COLUMN witness_status TEXT
    CHECK (witness_status IN ('not_required', 'pending', 'approved', 'challenged'));

ALTER TABLE consent_records
    ADD COLUMN review_status TEXT
    CHECK (review_status IN ('not_required', 'pending', 'approved', 'challenged'));

ALTER TABLE incident_events
    ADD COLUMN review_status TEXT
    CHECK (review_status IN ('not_required', 'pending', 'approved', 'challenged'));

ALTER TABLE pain_scores
    ADD COLUMN review_status TEXT
    CHECK (review_status IN ('not_required', 'pending', 'approved', 'challenged'));

-- Backfill: existing rows get 'not_required' so they don't suddenly
-- pile into the queue. New rows go through the service layer which
-- decides per case.
UPDATE drug_operations_log SET witness_status = 'not_required' WHERE witness_status IS NULL;
UPDATE consent_records     SET review_status  = 'not_required' WHERE review_status  IS NULL;
UPDATE incident_events     SET review_status  = 'not_required' WHERE review_status  IS NULL;
UPDATE pain_scores         SET review_status  = 'not_required' WHERE review_status  IS NULL;

-- +goose Down
ALTER TABLE pain_scores         DROP COLUMN review_status;
ALTER TABLE incident_events     DROP COLUMN review_status;
ALTER TABLE consent_records     DROP COLUMN review_status;
ALTER TABLE drug_operations_log DROP COLUMN witness_status;
DROP TABLE IF EXISTS compliance_approvals;
