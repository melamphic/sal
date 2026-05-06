-- +goose Up
-- +goose StatementBegin

-- ═══════════════════════════════════════════════════════════════════════════════
-- Form lineage from marketplace: enables per-form tracking of which marketplace
-- listing/version/acquisition the form was imported from. Required for the
-- buyer-side upgrade flow: when a publisher ships a new version, the buyer can
-- import it as a NEW tenant form (sibling to their existing one), and the UI
-- needs to surface that lineage to show banners and link siblings.
--
-- Pre-this-migration, only `policies.source_marketplace_version_id` was tracked
-- (00033). Forms had no lineage, so there was no way to find "every form this
-- clinic imported from listing X" for the upgrade UX.
-- ═══════════════════════════════════════════════════════════════════════════════

ALTER TABLE forms
    ADD COLUMN source_marketplace_listing_id     UUID,
    ADD COLUMN source_marketplace_version_id     UUID,
    ADD COLUMN source_marketplace_acquisition_id UUID;

-- Sibling lookup: "what other forms in this clinic descended from listing X?"
-- Partial index keeps it small — most forms are clinic-authored, not marketplace.
CREATE INDEX idx_forms_source_marketplace_listing
    ON forms(clinic_id, source_marketplace_listing_id)
    WHERE source_marketplace_listing_id IS NOT NULL;

-- Audit lookup by version (e.g., "which clinics imported v1 of this listing?").
CREATE INDEX idx_forms_source_marketplace_version
    ON forms(source_marketplace_version_id)
    WHERE source_marketplace_version_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_forms_source_marketplace_version;
DROP INDEX IF EXISTS idx_forms_source_marketplace_listing;

ALTER TABLE forms
    DROP COLUMN IF EXISTS source_marketplace_acquisition_id,
    DROP COLUMN IF EXISTS source_marketplace_version_id,
    DROP COLUMN IF EXISTS source_marketplace_listing_id;

-- +goose StatementEnd
