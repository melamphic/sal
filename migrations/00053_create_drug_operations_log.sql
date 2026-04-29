-- +goose Up
-- +goose StatementBegin

-- Append-only ledger of every drug operation against the clinic shelf.
-- Operations: administer, dispense, discard, receive, transfer, adjust.
--
-- Compliance properties:
--   * Witness staff_id required for controlled drugs (S1/S2/S3 NZ;
--     S8 AU; CII US; CD2/CD3 UK) — enforced in service layer based on
--     the schedule of the underlying catalog entry.
--   * UPDATEs are forbidden; corrections happen via the addends_to
--     self-reference (an addendum row pointing to the original).
--   * Once a row's reconciliation_id is set, the row is locked — even
--     addendums are disallowed (a correction would need to escalate
--     through the discrepancy workflow on the reconciliation row).
--   * balance_before and balance_after are denormalised from the
--     transaction so reports don't have to replay the entire log.
CREATE TABLE drug_operations_log (
    id                  UUID PRIMARY KEY,
    clinic_id           UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    shelf_id            UUID NOT NULL REFERENCES clinic_drug_shelf(id),
    subject_id          UUID REFERENCES subjects(id),         -- NULL for receive/discard/transfer
    note_id             UUID REFERENCES notes(id),            -- NULL when not tied to an encounter

    operation           TEXT NOT NULL
        CHECK (operation IN ('administer','dispense','discard','receive','transfer','adjust')),

    quantity            NUMERIC(14,4) NOT NULL,
    unit                TEXT NOT NULL,
    dose                TEXT,
    route               TEXT,
    reason_indication   TEXT,

    administered_by     UUID NOT NULL REFERENCES staff(id),
    witnessed_by        UUID REFERENCES staff(id),
    prescribed_by       UUID REFERENCES staff(id),

    balance_before      NUMERIC(14,4) NOT NULL,
    balance_after       NUMERIC(14,4) NOT NULL,

    reconciliation_id   UUID,                                  -- FK added in 00054
    addends_to          UUID REFERENCES drug_operations_log(id),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Subject required when administering / dispensing.
    CONSTRAINT drug_op_subject_required_for_patient_actions
        CHECK (
            operation NOT IN ('administer','dispense')
            OR subject_id IS NOT NULL
        )
);

CREATE INDEX drug_operations_log_clinic_idx
    ON drug_operations_log (clinic_id, created_at DESC);

CREATE INDEX drug_operations_log_shelf_idx
    ON drug_operations_log (shelf_id, created_at DESC);

CREATE INDEX drug_operations_log_subject_idx
    ON drug_operations_log (subject_id, created_at DESC)
    WHERE subject_id IS NOT NULL;

CREATE INDEX drug_operations_log_note_idx
    ON drug_operations_log (note_id)
    WHERE note_id IS NOT NULL;

CREATE INDEX drug_operations_log_pending_recon_idx
    ON drug_operations_log (clinic_id, shelf_id, created_at)
    WHERE reconciliation_id IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS drug_operations_log;

-- +goose StatementEnd
