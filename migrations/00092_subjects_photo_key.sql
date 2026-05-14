-- +goose Up
-- +goose StatementBegin

-- photo_key holds the durable object-storage path for a subject's
-- avatar (e.g. patient-photos/{clinic_id}/{uuid}.jpg). The service
-- signs a short-lived download URL from this key on every GET, so
-- subject avatars survive past the upload-time signature expiry.
-- photo_url stays on the row for legacy data only — new writes use
-- photo_key exclusively.
ALTER TABLE subjects
    ADD COLUMN photo_key TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE subjects
    DROP COLUMN photo_key;

-- +goose StatementEnd
