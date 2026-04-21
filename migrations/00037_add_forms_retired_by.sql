-- +goose Up
-- +goose StatementBegin

-- Capture accountability for form retirement so the version-history trail can
-- show "who retired this, when, and why" alongside publish/rollback events.
-- retired_by is set in the same UPDATE as archived_at/retire_reason; NULL for
-- historical forms retired before this column existed.
ALTER TABLE forms
    ADD COLUMN retired_by UUID;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE forms
    DROP COLUMN retired_by;

-- +goose StatementEnd
