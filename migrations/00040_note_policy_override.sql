-- +goose Up
-- When a note fails a high-parity policy clause, submitters with the
-- appropriate permission may still submit by providing a written
-- justification. The reason is stored on the note alongside who overrode
-- and when, so compliance audits can surface the decision.
ALTER TABLE notes
    ADD COLUMN override_reason TEXT,
    ADD COLUMN override_by     UUID REFERENCES staff(id),
    ADD COLUMN override_at     TIMESTAMPTZ,
    ADD CONSTRAINT notes_override_all_or_nothing CHECK (
        (override_reason IS NULL AND override_by IS NULL AND override_at IS NULL)
        OR (override_reason IS NOT NULL AND override_by IS NOT NULL AND override_at IS NOT NULL)
    );

-- +goose Down
ALTER TABLE notes
    DROP CONSTRAINT notes_override_all_or_nothing,
    DROP COLUMN override_reason,
    DROP COLUMN override_by,
    DROP COLUMN override_at;
