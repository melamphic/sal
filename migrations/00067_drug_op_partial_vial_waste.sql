-- +goose Up
-- +goose StatementBegin

-- Compliance v2: partial-vial waste tracking. Required by RCVS, NSW S8,
-- VIC D&P. Today the existing 'discard' op records the full quantity
-- discarded but not the residual remaining in the syringe/vial — vet
-- waste documentation needs both the dose drawn (administer) and the
-- residual destroyed (discard with residual_qty), often on the same
-- vial in one anaesthetic episode.
--
-- Design: docs/drug-register-compliance-v2.md §4.2
ALTER TABLE drug_operations_log
    ADD COLUMN waste_residual_qty   NUMERIC(14,4),
    ADD COLUMN waste_reason         TEXT,
    ADD COLUMN waste_witnessed_by   UUID REFERENCES staff(id);

-- Discard ops MUST record the residual quantity (may be 0 for whole-vial
-- destruction). Service layer is the friendly enforcer (returns a clear
-- 400); the constraint is the safety net against ledger rows that bypass
-- the service.
ALTER TABLE drug_operations_log
    ADD CONSTRAINT drug_op_discard_requires_residual
    CHECK (
        operation <> 'discard'
        OR waste_residual_qty IS NOT NULL
        OR created_at < '2026-05-01'  -- legacy rows pre-v2 are exempt
    );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE drug_operations_log
    DROP CONSTRAINT IF EXISTS drug_op_discard_requires_residual;

ALTER TABLE drug_operations_log
    DROP COLUMN IF EXISTS waste_witnessed_by,
    DROP COLUMN IF EXISTS waste_reason,
    DROP COLUMN IF EXISTS waste_residual_qty;

-- +goose StatementEnd
