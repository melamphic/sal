-- +goose Up
ALTER TABLE report_jobs ADD COLUMN content_hash TEXT;

-- +goose Down
ALTER TABLE report_jobs DROP COLUMN content_hash;
