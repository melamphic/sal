-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════════════════
-- Marketplace Phase 3 + 4 schema:
--   * Multi-form packs: a listing can carry N forms (was 1:1)
--   * Policy-only listings: source content is a tenant policy, not a form
--
-- Both extensions happen via the bundle_type enum + a new join table for the
-- pack source forms. The `bundled` and `form_only` legacy values stay
-- backward-compatible; existing rows are unchanged.
-- ═══════════════════════════════════════════════════════════════════════════════

-- ── 1. Extend bundle_type to add 'pack' and 'policy_only' ───────────────────
ALTER TABLE marketplace_listings
    DROP CONSTRAINT IF EXISTS marketplace_listings_bundle_type_check;

ALTER TABLE marketplace_listings
    ADD CONSTRAINT marketplace_listings_bundle_type_check
    CHECK (bundle_type IN ('form_only', 'bundled', 'pack', 'policy_only'));

-- ── 2. marketplace_listing_forms ─────────────────────────────────────────────
-- The set of tenant forms (owned by the publisher's clinic) that compose a
-- pack-type listing. Empty for non-pack listings — those use the implicit
-- single source_form_id passed at PublishVersion time.
--
-- Position is 1-based; ordering matters because the import flow materialises
-- the forms in the same order so the buyer's tenant forms feel curated.
--
-- Deletion semantics: if a publisher deletes their tenant form before
-- publishing a pack version, the join row is removed via ON DELETE CASCADE
-- on the form FK. Once a version is published, the package_payload carries
-- the immutable snapshot — losing the source form doesn't break already-
-- shipped versions.
CREATE TABLE marketplace_listing_forms (
    listing_id      UUID         NOT NULL REFERENCES marketplace_listings(id) ON DELETE CASCADE,
    position        INT          NOT NULL,
    source_form_id  UUID         NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (listing_id, position),
    UNIQUE (listing_id, source_form_id)
);

CREATE INDEX idx_marketplace_listing_forms_form
    ON marketplace_listing_forms(source_form_id);

-- ── 3. Allow source_form_id on listings to track the policy source for
-- policy_only listings. We add a generic `source_policy_id` column so the
-- publish-version flow knows where to pull the snapshot from. Nullable —
-- only populated when bundle_type='policy_only'.
ALTER TABLE marketplace_listings
    ADD COLUMN source_policy_id UUID REFERENCES policies(id);

CREATE INDEX idx_marketplace_listings_source_policy
    ON marketplace_listings(source_policy_id)
    WHERE source_policy_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_marketplace_listings_source_policy;
ALTER TABLE marketplace_listings DROP COLUMN IF EXISTS source_policy_id;

DROP INDEX IF EXISTS idx_marketplace_listing_forms_form;
DROP TABLE IF EXISTS marketplace_listing_forms;

ALTER TABLE marketplace_listings
    DROP CONSTRAINT IF EXISTS marketplace_listings_bundle_type_check;

ALTER TABLE marketplace_listings
    ADD CONSTRAINT marketplace_listings_bundle_type_check
    CHECK (bundle_type IN ('form_only', 'bundled'));

-- +goose StatementEnd
