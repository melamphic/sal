-- +goose Up
ALTER TABLE notes ADD COLUMN pdf_storage_key TEXT;

-- +goose Down
ALTER TABLE notes DROP COLUMN pdf_storage_key;
