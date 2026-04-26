-- +goose Up
-- +goose StatementBegin

-- Note-cap metering. Adds the columns needed to enforce pricing-model-v3
-- §7 (80% warn email / 110% CS notification / 150% hard block) and the
-- trial 100-note cap.
--
-- billing_period_start / billing_period_end
--   Authoritative copy of the current Stripe subscription period boundary
--   (sub.current_period_start / current_period_end). Written by the
--   billing webhook on customer.subscription.{created,updated} and on
--   invoice.payment_succeeded. The cap counter is COUNT(*) on notes
--   created since billing_period_start — so resetting the period
--   automatically resets the count without a separate UPDATE.
--
--   Trial clinics have no Stripe period yet — for them billing_period_start
--   is left NULL and the metering code falls back to clinics.created_at,
--   counting all notes created during the trial against the 100-note cap.
--
-- note_cap_warned_at / note_cap_cs_alerted_at / note_cap_blocked_at
--   Per-period sticky flags. Set the first time a clinic crosses 80% /
--   110% / 150% during the current period. Cleared back to NULL when the
--   period rolls over (UPDATE in the billing webhook, see clinic.repo).
--   Without these, the cascade would re-fire the same email every time a
--   note is created above the threshold.
--
-- Existing clinics get billing_period_start backfilled to created_at so
-- the cap counter has a sensible window for active customers without
-- requiring a webhook replay.

ALTER TABLE clinics
    ADD COLUMN billing_period_start    TIMESTAMPTZ,
    ADD COLUMN billing_period_end      TIMESTAMPTZ,
    ADD COLUMN note_cap_warned_at      TIMESTAMPTZ,
    ADD COLUMN note_cap_cs_alerted_at  TIMESTAMPTZ,
    ADD COLUMN note_cap_blocked_at     TIMESTAMPTZ;

UPDATE clinics
   SET billing_period_start = created_at
 WHERE billing_period_start IS NULL
   AND status IN ('active', 'past_due', 'grace_period');

-- Index supports the per-period COUNT(*) used on every note create. Keyed
-- by clinic_id (multi-tenancy filter) + created_at (period bound). The
-- partial WHERE archived_at IS NULL keeps the index narrow — archived
-- notes never count toward the cap.
CREATE INDEX IF NOT EXISTS idx_notes_clinic_created_active
    ON notes (clinic_id, created_at)
    WHERE archived_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_notes_clinic_created_active;

ALTER TABLE clinics
    DROP COLUMN IF EXISTS note_cap_blocked_at,
    DROP COLUMN IF EXISTS note_cap_cs_alerted_at,
    DROP COLUMN IF EXISTS note_cap_warned_at,
    DROP COLUMN IF EXISTS billing_period_end,
    DROP COLUMN IF EXISTS billing_period_start;

-- +goose StatementEnd
