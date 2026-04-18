-- +goose Up
-- +goose StatementBegin

-- Idempotency + audit trail for Stripe webhooks. Every incoming event
-- is inserted keyed by Stripe's event id BEFORE any side effects. A
-- duplicate id returns 23505 — service treats it as a replay, the
-- handler returns 200. Stripe retries failed webhooks aggressively so
-- this table gates every mutation.
--
-- status distinguishes three outcomes:
--   processed — matched a clinic, state written.
--   ignored   — event type not in our dispatch table (e.g. charge.*).
--   unmapped  — recognised event type but no clinic matched the
--               stripe_customer_id. Happens for paid signups where /mel
--               forgot to pass stripe_customer_id in the handoff JWT.
--               Surface in ops dashboards.
CREATE TABLE stripe_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    clinic_id    UUID        REFERENCES clinics(id) ON DELETE SET NULL,
    status       TEXT        NOT NULL
                             CHECK (status IN ('processed', 'ignored', 'unmapped'))
);

CREATE INDEX idx_stripe_events_received_at ON stripe_events (received_at);
CREATE INDEX idx_stripe_events_clinic_id
    ON stripe_events (clinic_id)
    WHERE clinic_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE stripe_events;

-- +goose StatementEnd
