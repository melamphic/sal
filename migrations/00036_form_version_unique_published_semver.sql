-- +goose Up
-- +goose StatementBegin

-- Prevent two concurrent publishes (or a publish racing a rollback) from
-- landing on the same semver. Without this index, both writers compute the
-- same next (major, minor) from the previous latest and insert in parallel
-- — creating two "v2.1" rows with no way to tell which is canonical.
--
-- Scope is `status = 'published'` only: drafts have no version numbers
-- assigned yet, and historical rows older than 00009's partial-unique-on-
-- drafts rule could in principle collide if backfilled, but no such rows
-- exist in any environment.
CREATE UNIQUE INDEX form_versions_published_semver_uniq
    ON form_versions (form_id, version_major, version_minor)
    WHERE status = 'published';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS form_versions_published_semver_uniq;

-- +goose StatementEnd
