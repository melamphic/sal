-- +goose Up
-- +goose StatementBegin

-- Adds billing plan code to clinics and extends the status enum with
-- 'past_due' so the Stripe webhook can reflect failed-invoice state
-- without immediately suspending access (grace period handled by
-- application logic + invoice.payment_failed retry schedule).

ALTER TABLE clinics ADD COLUMN plan_code TEXT;

ALTER TABLE clinics DROP CONSTRAINT clinics_status_check;
ALTER TABLE clinics ADD CONSTRAINT clinics_status_check
    CHECK (status IN ('trial', 'active', 'past_due', 'grace_period', 'cancelled', 'suspended'));

-- Stripe webhook lookup path: customer id → clinic.
CREATE INDEX IF NOT EXISTS idx_clinics_stripe_customer_id
    ON clinics (stripe_customer_id)
    WHERE stripe_customer_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_clinics_stripe_customer_id;

-- Any rows with status='past_due' will block this down migration —
-- operators must transition them to a legacy status first.
ALTER TABLE clinics DROP CONSTRAINT clinics_status_check;
ALTER TABLE clinics ADD CONSTRAINT clinics_status_check
    CHECK (status IN ('trial', 'active', 'grace_period', 'cancelled', 'suspended'));

ALTER TABLE clinics DROP COLUMN plan_code;

-- +goose StatementEnd
