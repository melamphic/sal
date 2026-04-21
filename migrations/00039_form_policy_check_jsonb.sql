-- +goose Up
-- Convert form_versions.policy_check_result from free-form TEXT to structured
-- JSONB. Going forward the column stores an array of per-policy entries:
--   [{policy_id, policy_version_id, result_pct, narrative, clauses: [...]}]
-- Existing narrative rows are discarded — pre-prod, no audit value in the old
-- unstructured text (and it doesn't fit the new shape anyway).
ALTER TABLE form_versions
    ALTER COLUMN policy_check_result DROP DEFAULT,
    ALTER COLUMN policy_check_result TYPE JSONB USING NULL;

-- +goose Down
ALTER TABLE form_versions
    ALTER COLUMN policy_check_result TYPE TEXT USING policy_check_result::text;
