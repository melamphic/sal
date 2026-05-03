-- +goose Up
-- +goose StatementBegin

-- Compliance v2: archived_at on drug_operations_log so the retention
-- purge worker can soft-delete rows past their retention window.
--
-- We never physically DELETE from this table (append-only is the
-- regulator-acceptance bar). Soft-delete is reversible if a regulator
-- subpoena later requires recovery — physical purge requires a
-- separate admin endpoint with double-confirmation + 1-year grace
-- (design doc §5.5).
--
-- Service-layer queries that already filter "active rows only" must
-- add `AND archived_at IS NULL` going forward; for now the chain
-- verifier + ledger listing accept archived rows transparently
-- because the chain-of-truth must include them (regulator may still
-- request the full audit trail).
ALTER TABLE drug_operations_log
    ADD COLUMN archived_at TIMESTAMPTZ;

CREATE INDEX drug_operations_log_archived_idx
    ON drug_operations_log (archived_at)
    WHERE archived_at IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS drug_operations_log_archived_idx;

ALTER TABLE drug_operations_log
    DROP COLUMN IF EXISTS archived_at;

-- +goose StatementEnd
