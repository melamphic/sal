-- +goose Up
-- +goose StatementBegin

-- Soft-unlink form_policies on policy retirement so the form's compliance
-- trail can surface "Policy X retired — unlinked" as a synthetic entry
-- (mirrors how retired forms appear in their own version history).
-- Hard-deleting on retire would erase the audit of *which* policy was active
-- at the time of prior publishes, which is the whole point of the trail.

ALTER TABLE form_policies
    ADD COLUMN unlinked_at           TIMESTAMPTZ NULL,
    ADD COLUMN unlinked_reason       TEXT        NULL,
    -- Capture the policy name at unlink time. The policies row still exists
    -- (archived), but capturing here keeps the trail stable against future
    -- rename edits on the archived policy.
    ADD COLUMN policy_name_snapshot  TEXT        NULL;

-- Existing PK (form_id, policy_id) still applies. Partial unique index so
-- a form can re-link the same policy after an unlink without violating PK.
-- We drop the old PK and replace with a partial unique, keeping one active
-- link per (form, policy) pair while allowing the unlinked history rows.
ALTER TABLE form_policies DROP CONSTRAINT IF EXISTS form_policies_pkey;

CREATE UNIQUE INDEX form_policies_active_uniq
    ON form_policies (form_id, policy_id)
    WHERE unlinked_at IS NULL;

-- Index for the retire-cascade sweep: WHERE policy_id = $1 AND unlinked_at IS NULL.
CREATE INDEX form_policies_policy_active_idx
    ON form_policies (policy_id)
    WHERE unlinked_at IS NULL;

-- Index for per-form trail reads: ORDER BY unlinked_at DESC WHERE form_id = $1.
CREATE INDEX form_policies_form_unlinked_idx
    ON form_policies (form_id, unlinked_at DESC)
    WHERE unlinked_at IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS form_policies_form_unlinked_idx;
DROP INDEX IF EXISTS form_policies_policy_active_idx;
DROP INDEX IF EXISTS form_policies_active_uniq;

ALTER TABLE form_policies
    DROP COLUMN policy_name_snapshot,
    DROP COLUMN unlinked_reason,
    DROP COLUMN unlinked_at;

-- Restore original PK.
ALTER TABLE form_policies ADD PRIMARY KEY (form_id, policy_id);

-- +goose StatementEnd
