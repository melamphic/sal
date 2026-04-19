-- +goose Up
ALTER TABLE notes ADD COLUMN policy_check_result JSONB;

-- +goose Down
ALTER TABLE notes DROP COLUMN policy_check_result;
