-- +goose Up
-- +goose StatementBegin

CREATE TABLE clinics (
    id                        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                      TEXT        NOT NULL,
    -- URL-safe identifier used in magic links and audit exports.
    slug                      VARCHAR(100) NOT NULL UNIQUE,
    -- PII: encrypted — use crypto.Cipher.Encrypt before INSERT.
    email                     TEXT        NOT NULL,
    -- Deterministic HMAC hash of lowercased email for uniqueness and lookup.
    email_hash                TEXT        NOT NULL UNIQUE,
    -- PII: encrypted
    phone                     TEXT,
    -- PII: encrypted
    address                   TEXT,
    vertical                  VARCHAR     NOT NULL DEFAULT 'veterinary'
                                          CHECK (vertical IN ('veterinary', 'dental', 'aged_care')),
    status                    VARCHAR     NOT NULL DEFAULT 'trial'
                                          CHECK (status IN ('trial', 'active', 'grace_period', 'cancelled', 'suspended')),
    trial_ends_at             TIMESTAMPTZ NOT NULL,
    -- Stripe identifiers — NULL until billing is set up (Phase 5).
    stripe_customer_id        TEXT,
    stripe_subscription_id    TEXT,
    -- Note cap tracking. NULL during trial (no cap applies).
    note_cap                  INTEGER,
    note_count                INTEGER     NOT NULL DEFAULT 0,
    note_count_reset_at       TIMESTAMPTZ,
    -- Where this clinic's data must be stored (for data residency).
    data_region               VARCHAR     NOT NULL DEFAULT 'ap-southeast-2',
    -- Set when the clinic is scheduled for deletion (7 years post-cancellation).
    scheduled_for_deletion_at TIMESTAMPTZ,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Soft-delete: cancelled clinics are archived, not removed.
    archived_at               TIMESTAMPTZ
);

-- ── Triggers ───────────────────────────────────────────────────────────────────

-- Keep updated_at current on every row change.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER clinics_updated_at
    BEFORE UPDATE ON clinics
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS clinics_updated_at ON clinics;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TABLE IF EXISTS clinics;

-- +goose StatementEnd
