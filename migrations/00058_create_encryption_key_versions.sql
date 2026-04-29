-- +goose Up
-- +goose StatementBegin

-- Registry of AES-256-GCM key versions used by the column-level
-- encryption wrapper (internal/platform/crypto). Each ciphertext is
-- prefixed with the key version byte so the decrypt path picks the
-- right key — meaning we DON'T need a per-row cipher_version column
-- on every encrypted table.
--
-- Compliance value: HIPAA §164.312(a)(2)(iv) and UK GDPR Article 32
-- both require documented key management. This table is the
-- authoritative log; the Privacy Officer report aggregates it.
CREATE TABLE encryption_key_versions (
    version          SMALLINT PRIMARY KEY,
    cipher_suite     TEXT NOT NULL DEFAULT 'AES-256-GCM',
    activated_at     TIMESTAMPTZ NOT NULL,
    retired_at       TIMESTAMPTZ,
    rotated_by       UUID REFERENCES staff(id),
    rotation_reason  TEXT,
    notes            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT encryption_key_versions_active_period_valid
        CHECK (retired_at IS NULL OR retired_at >= activated_at)
);

-- Seed v1 retroactively so existing encrypted columns have a key version
-- they can claim. The rotated_by is NULL because the system installed v1
-- pre-this-migration; a real rotation always has a rotator.
INSERT INTO encryption_key_versions (version, cipher_suite, activated_at, notes)
VALUES (1, 'AES-256-GCM', '2026-01-01 00:00:00+00', 'Initial production cipher version (pre-rotation log)')
ON CONFLICT (version) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS encryption_key_versions;

-- +goose StatementEnd
