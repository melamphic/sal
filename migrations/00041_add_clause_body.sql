-- +goose Up
-- Policy clauses gain a `body` field so admins can attach richer guidance
-- (bullet lists, bold/italic emphasis) to each clause. The Flutter editor
-- produces a small markdown subset that both the digital preview and the
-- PDF renderer understand. Existing rows default to empty.
ALTER TABLE policy_clauses
    ADD COLUMN body TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE policy_clauses
    DROP COLUMN body;
