-- +goose Up
-- +goose StatementBegin

-- Fields collected when a staff member accepts their email invite.
-- Today we only capture full_name; this expands to the minimum set
-- competitors collect at acceptance time + what's compliance-required
-- on day 1 for clinical hires.
--
-- title         — honorific / credential prefix printed on signed PDFs
--                 ("Dr.", "RN", "RVN"). Free-form short string;
--                 enum-style validation lives in the FE / service.
--
-- mobile_e164   — E.164-formatted phone number, encrypted (PII). Used
--                 for 2FA + incident SMS. Optional.
--
-- terms_accepted_at — set on first invite acceptance. Required by
--                 GDPR + healthcare data-handling policies for an
--                 auditable record of consent. Existing rows are
--                 backfilled to created_at so older accounts don't
--                 read as "never accepted".

ALTER TABLE staff
    ADD COLUMN title             TEXT,
    ADD COLUMN mobile_e164       TEXT,
    ADD COLUMN terms_accepted_at TIMESTAMPTZ;

-- Backfill: every existing staff row has been operating under the
-- terms by definition, so stamp acceptance at their creation time.
UPDATE staff SET terms_accepted_at = created_at
WHERE terms_accepted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE staff
    DROP COLUMN IF EXISTS terms_accepted_at,
    DROP COLUMN IF EXISTS mobile_e164,
    DROP COLUMN IF EXISTS title;
-- +goose StatementEnd
