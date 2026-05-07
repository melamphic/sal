-- +goose Up
-- +goose StatementBegin

-- Adds revocation tracking to invite_tokens so the team-management UI
-- can show "revoked" as a distinct status (vs. silently deleting the
-- row, which loses audit context). The accept-invite path treats any
-- non-NULL revoked_at as a hard reject.
ALTER TABLE invite_tokens
    ADD COLUMN revoked_at TIMESTAMPTZ;

-- Listing pending invites for a clinic is the new hot path — covers
-- the team page query: WHERE clinic_id = $1 AND accepted_at IS NULL
-- AND revoked_at IS NULL ORDER BY created_at DESC.
CREATE INDEX idx_invite_tokens_clinic_pending
    ON invite_tokens (clinic_id, created_at DESC)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_invite_tokens_clinic_pending;
ALTER TABLE invite_tokens DROP COLUMN IF EXISTS revoked_at;
-- +goose StatementEnd
