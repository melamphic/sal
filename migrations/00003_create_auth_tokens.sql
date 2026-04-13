-- +goose Up
-- +goose StatementBegin

-- auth_tokens stores hashed magic link tokens and refresh tokens.
-- The raw token is never stored — only SHA-256 hashes.
-- Both token types are one-time-use and time-limited.
CREATE TABLE auth_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id    UUID        NOT NULL REFERENCES staff(id),
    -- SHA-256 hash of the raw token. The raw token is sent to the user and
    -- never stored here. On verify, hash the incoming token and compare.
    token_hash  TEXT        NOT NULL UNIQUE,
    token_type  VARCHAR     NOT NULL
                            CHECK (token_type IN ('magic_link', 'refresh')),
    expires_at  TIMESTAMPTZ NOT NULL,
    -- Set when the token is consumed. Used to detect replay attacks.
    used_at     TIMESTAMPTZ,
    -- Soft IP/agent logging for anomaly detection (SOC 2 requirement).
    -- PII: encrypted (IP address)
    created_from_ip TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_auth_tokens_token_hash ON auth_tokens(token_hash);
CREATE INDEX idx_auth_tokens_staff_id ON auth_tokens(staff_id);
-- Cleanup job uses this to purge expired tokens efficiently.
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens(expires_at);

-- invite_tokens stores pending staff invitations.
-- Separate from auth_tokens because invites have additional metadata
-- (role, permissions) and a different lifecycle.
CREATE TABLE invite_tokens (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id     UUID        NOT NULL REFERENCES clinics(id),
    -- PII: encrypted — the invited person's email address.
    email         TEXT        NOT NULL,
    -- HMAC hash for lookup (same pattern as staff.email_hash).
    email_hash    TEXT        NOT NULL,
    role          VARCHAR     NOT NULL,
    -- Permissions granted at invite time, stored as JSONB for flexibility.
    permissions   JSONB       NOT NULL DEFAULT '{}',
    note_tier     VARCHAR     NOT NULL DEFAULT 'none',
    token_hash    TEXT        NOT NULL UNIQUE,
    expires_at    TIMESTAMPTZ NOT NULL,
    accepted_at   TIMESTAMPTZ,
    -- Who sent this invite — for audit trail.
    invited_by_id UUID        NOT NULL REFERENCES staff(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_invite_tokens_token_hash ON invite_tokens(token_hash);
CREATE INDEX idx_invite_tokens_clinic_id ON invite_tokens(clinic_id);
CREATE INDEX idx_invite_tokens_email_hash ON invite_tokens(email_hash);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS invite_tokens;
DROP TABLE IF EXISTS auth_tokens;

-- +goose StatementEnd
