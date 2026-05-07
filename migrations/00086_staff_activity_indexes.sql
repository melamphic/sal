-- +goose Up
-- +goose StatementBegin

-- Per-staff activity feed scans each domain table by actor_id ordered
-- by event time DESC. Without these indexes the aggregator fans out
-- N seq-scans on every team-page open. Each is a partial index where
-- it makes sense (login events filter to consumed magic-link rows).

CREATE INDEX IF NOT EXISTS drug_operations_log_administered_by_idx
    ON drug_operations_log (administered_by, created_at DESC);

CREATE INDEX IF NOT EXISTS incident_events_reported_by_idx
    ON incident_events (reported_by, occurred_at DESC);

CREATE INDEX IF NOT EXISTS consent_records_captured_by_idx
    ON consent_records (captured_by, captured_at DESC);

CREATE INDEX IF NOT EXISTS pain_scores_assessed_by_idx
    ON pain_scores (assessed_by, assessed_at DESC);

-- Login activity is the subset of auth_tokens that's a consumed
-- magic_link. Partial index keeps this small + fast.
CREATE INDEX IF NOT EXISTS auth_tokens_staff_login_idx
    ON auth_tokens (staff_id, used_at DESC)
    WHERE token_type = 'magic_link' AND used_at IS NOT NULL;

-- note_events.actor_id was already used by timeline.ListByActor; an
-- index makes sure that scan is fast too.
CREATE INDEX IF NOT EXISTS note_events_actor_idx
    ON note_events (actor_id, occurred_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS note_events_actor_idx;
DROP INDEX IF EXISTS auth_tokens_staff_login_idx;
DROP INDEX IF EXISTS pain_scores_assessed_by_idx;
DROP INDEX IF EXISTS consent_records_captured_by_idx;
DROP INDEX IF EXISTS incident_events_reported_by_idx;
DROP INDEX IF EXISTS drug_operations_log_administered_by_idx;
-- +goose StatementEnd
