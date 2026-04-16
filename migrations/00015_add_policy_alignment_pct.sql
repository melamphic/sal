-- +goose Up

ALTER TABLE notes ADD COLUMN policy_alignment_pct DECIMAL(5,2);

-- +goose Down

ALTER TABLE notes DROP COLUMN IF EXISTS policy_alignment_pct;
