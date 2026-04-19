-- +goose Up
-- +goose StatementBegin

-- Records the jti of every consumed /mel → Salvia handoff JWT so a single
-- token cannot be replayed. Rows are written transactionally during
-- handoff and pruned by a background job once expires_at is past.
--
-- NOTE: this table is global (not per-clinic). The handoff happens before
-- the Salvia clinic row exists, so there is no clinic_id to scope by.

CREATE TABLE mel_handoff_tokens (
    jti          TEXT        PRIMARY KEY,
    consumed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL
);

-- Used by the prune job.
CREATE INDEX idx_mel_handoff_tokens_expires_at
    ON mel_handoff_tokens (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE mel_handoff_tokens;

-- +goose StatementEnd
