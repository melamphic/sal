-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════════════════
-- Marketplace (Phase 1 MVP)
-- ═══════════════════════════════════════════════════════════════════════════════
-- Scope: Salvia-curated free forms only. Tables for ratings, tags, upgrade
-- notifications are created but not yet wired to routes. Third-party publishing
-- and Stripe Connect are Phase 2 / Phase 3.
-- See docs/marketplace.md for full design rationale.
-- ═══════════════════════════════════════════════════════════════════════════════

-- publisher_accounts: one row per clinic allowed to publish to the marketplace.
-- Phase 1 creates exactly one row with authority_type='salvia' for the reserved
-- salvia-platform clinic. Third-party publishers register in Phase 2.
CREATE TABLE publisher_accounts (
    id                          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id                   UUID         NOT NULL UNIQUE REFERENCES clinics(id),
    display_name                TEXT         NOT NULL,
    bio                         TEXT,
    website_url                 TEXT,
    verified_badge              BOOLEAN      NOT NULL DEFAULT false,
    authority_type              VARCHAR      CHECK (authority_type IN ('salvia', 'national_body', 'specialist_college')),
    authority_granted_by        UUID         REFERENCES publisher_accounts(id),
    authority_granted_at        TIMESTAMPTZ,
    stripe_connect_account_id   TEXT,
    stripe_onboarding_complete  BOOLEAN      NOT NULL DEFAULT false,
    status                      VARCHAR      NOT NULL DEFAULT 'pending'
                                             CHECK (status IN ('pending', 'active', 'suspended')),
    created_at                  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_publisher_accounts_verified
    ON publisher_accounts(verified_badge) WHERE verified_badge = true;
CREATE INDEX idx_publisher_accounts_status ON publisher_accounts(status);

CREATE TRIGGER publisher_accounts_updated_at
    BEFORE UPDATE ON publisher_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- marketplace_listings: one listing per published form. Independent of the
-- tenant forms table — a listing is a marketplace artifact, not a clinic form.
-- search_vector is maintained by the trigger below from name/tags/descriptions.
-- download_count, rating_count, rating_sum are denormalised from acquisitions
-- and reviews for fast sort/filter without subqueries.
CREATE TABLE marketplace_listings (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    publisher_account_id    UUID          NOT NULL REFERENCES publisher_accounts(id),
    vertical                VARCHAR       NOT NULL DEFAULT 'veterinary'
                                          CHECK (vertical IN ('veterinary', 'dental', 'aged_care')),
    name                    TEXT          NOT NULL,
    slug                    VARCHAR(200)  NOT NULL UNIQUE,
    short_description       TEXT          NOT NULL,
    long_description        TEXT,
    tags                    TEXT[]        NOT NULL DEFAULT '{}',
    policy_dependency_flag  BOOLEAN       NOT NULL DEFAULT false,
    policy_dependency_note  TEXT,
    pricing_type            VARCHAR       NOT NULL CHECK (pricing_type IN ('free', 'paid')),
    price_cents             INTEGER       CHECK (price_cents IS NULL OR price_cents > 0),
    currency                VARCHAR(3)    NOT NULL DEFAULT 'NZD',
    status                  VARCHAR       NOT NULL DEFAULT 'draft'
                                          CHECK (status IN ('draft', 'published', 'under_review', 'suspended', 'archived')),
    search_vector           TSVECTOR,
    preview_field_count     INTEGER       NOT NULL DEFAULT 3 CHECK (preview_field_count >= 0),
    download_count          INTEGER       NOT NULL DEFAULT 0,
    rating_count            INTEGER       NOT NULL DEFAULT 0,
    rating_sum              INTEGER       NOT NULL DEFAULT 0,
    published_at            TIMESTAMPTZ,
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    archived_at             TIMESTAMPTZ,
    -- price_cents required for paid listings, forbidden for free listings
    CHECK ((pricing_type = 'free'  AND price_cents IS NULL)
        OR (pricing_type = 'paid' AND price_cents IS NOT NULL))
);

CREATE INDEX idx_marketplace_listings_publisher     ON marketplace_listings(publisher_account_id);
CREATE INDEX idx_marketplace_listings_vertical_pub  ON marketplace_listings(vertical) WHERE status = 'published';
CREATE INDEX idx_marketplace_listings_pricing_pub   ON marketplace_listings(pricing_type) WHERE status = 'published';
CREATE INDEX idx_marketplace_listings_tags          ON marketplace_listings USING GIN(tags);
CREATE INDEX idx_marketplace_listings_search        ON marketplace_listings USING GIN(search_vector);
CREATE INDEX idx_marketplace_listings_policy_flag   ON marketplace_listings(policy_dependency_flag) WHERE policy_dependency_flag = true;

CREATE TRIGGER marketplace_listings_updated_at
    BEFORE UPDATE ON marketplace_listings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- search_vector maintenance trigger.
CREATE OR REPLACE FUNCTION marketplace_listings_search_vector()
RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector :=
        setweight(to_tsvector('english', coalesce(NEW.name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(array_to_string(NEW.tags, ' '), '')), 'A') ||
        setweight(to_tsvector('english', coalesce(NEW.short_description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(NEW.long_description, '')), 'C');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER marketplace_listings_search_vector_trigger
    BEFORE INSERT OR UPDATE OF name, tags, short_description, long_description
    ON marketplace_listings
    FOR EACH ROW EXECUTE FUNCTION marketplace_listings_search_vector();

-- marketplace_versions: immutable version snapshots of a listing. Buyers pin
-- to a specific version at acquisition time. package_payload is the full
-- portable form package (see docs/marketplace.md §2).
CREATE TABLE marketplace_versions (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id              UUID          NOT NULL REFERENCES marketplace_listings(id),
    version_major           INT           NOT NULL,
    version_minor           INT           NOT NULL,
    change_type             VARCHAR       NOT NULL CHECK (change_type IN ('minor', 'major')),
    change_summary          TEXT,
    package_payload         JSONB         NOT NULL,
    payload_checksum        TEXT          NOT NULL,
    field_count             INT           NOT NULL,
    source_form_version_id  UUID          REFERENCES form_versions(id),
    status                  VARCHAR       NOT NULL DEFAULT 'active'
                                          CHECK (status IN ('active', 'deprecated')),
    published_at            TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    published_by            UUID          NOT NULL REFERENCES staff(id),
    created_at              TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_marketplace_versions_listing_version
    ON marketplace_versions(listing_id, version_major, version_minor);
CREATE INDEX idx_marketplace_versions_listing_latest
    ON marketplace_versions(listing_id, published_at DESC);
CREATE INDEX idx_marketplace_versions_source_form
    ON marketplace_versions(source_form_version_id) WHERE source_form_version_id IS NOT NULL;

-- marketplace_version_fields: relational mirror of package_payload.fields.
-- Enables SQL-level preview and per-field indexing without JSONB parsing.
CREATE TABLE marketplace_version_fields (
    id                      UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    marketplace_version_id  UUID          NOT NULL REFERENCES marketplace_versions(id) ON DELETE CASCADE,
    position                INT           NOT NULL,
    title                   TEXT          NOT NULL,
    type                    TEXT          NOT NULL,
    config                  JSONB         NOT NULL DEFAULT '{}',
    ai_prompt               TEXT,
    required                BOOLEAN       NOT NULL DEFAULT false,
    skippable               BOOLEAN       NOT NULL DEFAULT false,
    allow_inference         BOOLEAN       NOT NULL DEFAULT true,
    min_confidence          DECIMAL(4,2)  CHECK (min_confidence IS NULL OR (min_confidence >= 0 AND min_confidence <= 1))
);

CREATE INDEX idx_marketplace_version_fields_version
    ON marketplace_version_fields(marketplace_version_id, position);

-- marketplace_acquisitions: entitlement rows. Free and paid acquisitions both
-- land here. Partial unique index ensures one active entitlement per
-- (listing, clinic); refunded rows are retained for audit.
CREATE TABLE marketplace_acquisitions (
    id                        UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id                UUID         NOT NULL REFERENCES marketplace_listings(id),
    marketplace_version_id    UUID         NOT NULL REFERENCES marketplace_versions(id),
    clinic_id                 UUID         NOT NULL REFERENCES clinics(id),
    acquired_by               UUID         NOT NULL REFERENCES staff(id),
    acquisition_type          VARCHAR      NOT NULL CHECK (acquisition_type IN ('free', 'purchase')),
    stripe_payment_intent_id  TEXT,
    amount_paid_cents         INTEGER,
    platform_fee_cents        INTEGER,
    currency                  VARCHAR(3),
    status                    VARCHAR      NOT NULL DEFAULT 'pending'
                                           CHECK (status IN ('pending', 'active', 'refunded')),
    imported_form_id          UUID         REFERENCES forms(id),
    fulfilled_at              TIMESTAMPTZ,
    created_at                TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_marketplace_acquisitions_active_unique
    ON marketplace_acquisitions(listing_id, clinic_id) WHERE status = 'active';
CREATE INDEX idx_marketplace_acquisitions_clinic
    ON marketplace_acquisitions(clinic_id, status);
CREATE INDEX idx_marketplace_acquisitions_stripe_pi
    ON marketplace_acquisitions(stripe_payment_intent_id) WHERE stripe_payment_intent_id IS NOT NULL;
CREATE INDEX idx_marketplace_acquisitions_version
    ON marketplace_acquisitions(marketplace_version_id);

-- marketplace_reviews: verified-acquisition model. Must hold active entitlement
-- to leave a review. Tables exist for Phase 2 but routes come later.
CREATE TABLE marketplace_reviews (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id      UUID         NOT NULL REFERENCES marketplace_listings(id),
    acquisition_id  UUID         NOT NULL REFERENCES marketplace_acquisitions(id),
    clinic_id       UUID         NOT NULL REFERENCES clinics(id),
    staff_id        UUID         NOT NULL REFERENCES staff(id),
    rating          SMALLINT     NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body            TEXT,
    status          VARCHAR      NOT NULL DEFAULT 'published'
                                 CHECK (status IN ('published', 'hidden', 'removed')),
    moderated_by    UUID         REFERENCES staff(id),
    moderated_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_marketplace_reviews_listing_clinic_unique
    ON marketplace_reviews(listing_id, clinic_id);
CREATE INDEX idx_marketplace_reviews_listing_feed
    ON marketplace_reviews(listing_id, status, created_at DESC);

CREATE TRIGGER marketplace_reviews_updated_at
    BEFORE UPDATE ON marketplace_reviews
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- marketplace_tags: normalised taxonomy (separate from forms.tags array).
CREATE TABLE marketplace_tags (
    id             UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    slug           VARCHAR(100)  NOT NULL UNIQUE,
    display_name   TEXT          NOT NULL,
    vertical       VARCHAR,
    listing_count  INTEGER       NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- marketplace_listing_tags: join table between listings and taxonomy tags.
CREATE TABLE marketplace_listing_tags (
    listing_id  UUID NOT NULL REFERENCES marketplace_listings(id) ON DELETE CASCADE,
    tag_id      UUID NOT NULL REFERENCES marketplace_tags(id) ON DELETE CASCADE,
    PRIMARY KEY (listing_id, tag_id)
);

CREATE INDEX idx_marketplace_listing_tags_tag ON marketplace_listing_tags(tag_id);

-- marketplace_update_notifications: tracks upgrade availability per pinned
-- acquisition. Populated by a River worker when a new version is published.
-- Phase 1 creates the table; Phase 2 wires the worker + routes.
CREATE TABLE marketplace_update_notifications (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    acquisition_id      UUID         NOT NULL REFERENCES marketplace_acquisitions(id),
    clinic_id           UUID         NOT NULL REFERENCES clinics(id),
    new_version_id      UUID         NOT NULL REFERENCES marketplace_versions(id),
    notification_type   VARCHAR      NOT NULL CHECK (notification_type IN ('major_upgrade', 'minor_update')),
    seen_at             TIMESTAMPTZ,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_marketplace_update_notifications_unread
    ON marketplace_update_notifications(clinic_id, seen_at NULLS FIRST);

-- ═══════════════════════════════════════════════════════════════════════════════
-- Permissions
-- ═══════════════════════════════════════════════════════════════════════════════

ALTER TABLE staff
    ADD COLUMN perm_marketplace_manage   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN perm_marketplace_download BOOLEAN NOT NULL DEFAULT false;

-- Backfill download permission for clinical roles who use forms day-to-day.
-- vet_nurse is included because DefaultPermissions grants SubmitForms=true.
-- receptionist excluded; super_admin can grant per-staff.
UPDATE staff SET perm_marketplace_download = true
WHERE role IN ('super_admin', 'admin', 'vet', 'vet_nurse');

-- Manage permission: super_admin only by default.
UPDATE staff SET perm_marketplace_manage = true
WHERE role = 'super_admin';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE staff
    DROP COLUMN IF EXISTS perm_marketplace_download,
    DROP COLUMN IF EXISTS perm_marketplace_manage;

DROP TABLE IF EXISTS marketplace_update_notifications;
DROP TABLE IF EXISTS marketplace_listing_tags;
DROP TABLE IF EXISTS marketplace_tags;
DROP TABLE IF EXISTS marketplace_reviews;
DROP TABLE IF EXISTS marketplace_acquisitions;
DROP TABLE IF EXISTS marketplace_version_fields;
DROP TABLE IF EXISTS marketplace_versions;

DROP TRIGGER IF EXISTS marketplace_listings_search_vector_trigger ON marketplace_listings;
DROP FUNCTION IF EXISTS marketplace_listings_search_vector();
DROP TABLE IF EXISTS marketplace_listings;

DROP TABLE IF EXISTS publisher_accounts;

-- +goose StatementEnd
