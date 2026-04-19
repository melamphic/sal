-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════════════════
-- Marketplace Phase 1.5: policy bundling + simplified authority model + Stripe
-- ═══════════════════════════════════════════════════════════════════════════════

-- ── Relax authority_type to the simplified two-tier model ────────────────────
-- Before: ('salvia', 'national_body', 'specialist_college')
-- After:  ('salvia', 'authority')
ALTER TABLE publisher_accounts
    DROP CONSTRAINT IF EXISTS publisher_accounts_authority_type_check;

ALTER TABLE publisher_accounts
    ADD CONSTRAINT publisher_accounts_authority_type_check
    CHECK (authority_type IN ('salvia', 'authority'));

-- ── Listing changes: bundle_type replaces policy_dependency_flag/note ────────
ALTER TABLE marketplace_listings
    ADD COLUMN bundle_type VARCHAR NOT NULL DEFAULT 'bundled'
        CHECK (bundle_type IN ('form_only', 'bundled'));

-- Backfill: any listing with policy_dependency_flag=true → bundle_type='form_only'
-- (Phase 1 semantics: "references a policy outside the package").
UPDATE marketplace_listings
SET bundle_type = 'form_only'
WHERE policy_dependency_flag = true;

ALTER TABLE marketplace_listings
    DROP COLUMN policy_dependency_flag,
    DROP COLUMN policy_dependency_note;

DROP INDEX IF EXISTS idx_marketplace_listings_policy_flag;

-- ── Acquisition changes: policy import choice + legal acknowledgement ───────
ALTER TABLE marketplace_acquisitions
    ADD COLUMN policy_import_choice VARCHAR
        CHECK (policy_import_choice IN ('imported', 'skipped', 'relinked')),
    ADD COLUMN policy_attribution_accepted_at TIMESTAMPTZ;

-- ── Tenant-side audit: which marketplace version did this policy come from? ─
ALTER TABLE policies
    ADD COLUMN source_marketplace_version_id UUID;

CREATE INDEX idx_policies_source_marketplace
    ON policies(source_marketplace_version_id) WHERE source_marketplace_version_id IS NOT NULL;

-- ── Stripe event dedupe ──────────────────────────────────────────────────────
-- Phase 3 uses this to reject duplicate webhook deliveries from Stripe.
-- event_id = Stripe-assigned unique identifier per webhook event.
CREATE TABLE stripe_events_processed (
    event_id      TEXT         PRIMARY KEY,
    event_type    TEXT         NOT NULL,
    processed_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ── Seed the Salvia platform clinic + publisher ──────────────────────────────
-- This is the internal clinic whose staff are Salvia employees.
-- Deterministic UUIDs so migrations are idempotent and tests can reference them.
-- The first super-admin staff is NOT seeded here — bootstrap code in app.go
-- provisions it from an env var (SALVIA_PLATFORM_ADMIN_EMAIL) at startup.
--
-- The trial_ends_at date is far in the future — the platform clinic is never
-- on trial.
INSERT INTO clinics (
    id, name, slug, email, email_hash, vertical, status, trial_ends_at, data_region
) VALUES (
    '00000000-0000-0000-0000-000000000001',
    'Salvia Platform',
    'salvia-platform',
    'encrypted_placeholder',
    'salvia_platform_marker',
    'veterinary',
    'active',
    '9999-12-31 00:00:00+00',
    'ap-southeast-2'
) ON CONFLICT (id) DO NOTHING;

INSERT INTO publisher_accounts (
    id, clinic_id, display_name, authority_type, status
) VALUES (
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000001',
    'Salvia',
    'salvia',
    'active'
) ON CONFLICT (id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM publisher_accounts WHERE id = '00000000-0000-0000-0000-000000000002';
DELETE FROM clinics WHERE id = '00000000-0000-0000-0000-000000000001';

DROP TABLE IF EXISTS stripe_events_processed;

DROP INDEX IF EXISTS idx_policies_source_marketplace;
ALTER TABLE policies
    DROP COLUMN IF EXISTS source_marketplace_version_id;

ALTER TABLE marketplace_acquisitions
    DROP COLUMN IF EXISTS policy_attribution_accepted_at,
    DROP COLUMN IF EXISTS policy_import_choice;

ALTER TABLE marketplace_listings
    ADD COLUMN policy_dependency_flag BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN policy_dependency_note TEXT;

UPDATE marketplace_listings
SET policy_dependency_flag = true
WHERE bundle_type = 'form_only';

ALTER TABLE marketplace_listings
    DROP COLUMN bundle_type;

CREATE INDEX idx_marketplace_listings_policy_flag
    ON marketplace_listings(policy_dependency_flag) WHERE policy_dependency_flag = true;

ALTER TABLE publisher_accounts
    DROP CONSTRAINT IF EXISTS publisher_accounts_authority_type_check;

ALTER TABLE publisher_accounts
    ADD CONSTRAINT publisher_accounts_authority_type_check
    CHECK (authority_type IN ('salvia', 'national_body', 'specialist_college'));

-- +goose StatementEnd
