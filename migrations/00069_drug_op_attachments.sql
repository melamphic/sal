-- +goose Up
-- +goose StatementBegin

-- Compliance v2: evidence attachments for ledger rows. Many regulators
-- expect retainable evidence linked to specific ledger entries:
--   * receive: supplier invoice / packing slip
--   * destruction: regulator destruction certificate (UK CD destroyed
--     register, US Form 41, NZ regulator notice)
--   * administer / discard: vial photo + post-admin syringe photo
--     (witness artefact)
--
-- drug_op_id is NULLABLE so a photo can be uploaded BEFORE the op is
-- confirmed (vial photo OCR returns extracted fields; the user reviews
-- + confirms; the op is then created and the attachment linked).
-- Pending attachments (drug_op_id NULL) older than 24h are swept by a
-- background job.
--
-- Design: docs/drug-register-compliance-v2.md §4.4
CREATE TABLE drug_op_attachments (
    id            UUID PRIMARY KEY,
    clinic_id     UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    drug_op_id    UUID REFERENCES drug_operations_log(id) ON DELETE RESTRICT,

    kind          TEXT NOT NULL
        CHECK (kind IN ('invoice','destruction_cert','vial_photo','witness_photo','other')),

    s3_key        TEXT NOT NULL,
    content_type  TEXT NOT NULL,
    bytes         BIGINT NOT NULL CHECK (bytes >= 0),

    -- AI-extracted payload from vial-photo OCR or invoice scan.
    -- NULL when not extracted (manual upload, witness photo, etc).
    extracted_payload JSONB,

    uploaded_by   UUID NOT NULL REFERENCES staff(id),
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at   TIMESTAMPTZ
);

CREATE INDEX drug_op_attachments_op_idx
    ON drug_op_attachments (drug_op_id)
    WHERE drug_op_id IS NOT NULL AND archived_at IS NULL;

-- Pending-cleanup index for the 24h sweep worker.
CREATE INDEX drug_op_attachments_pending_idx
    ON drug_op_attachments (uploaded_at)
    WHERE drug_op_id IS NULL AND archived_at IS NULL;

CREATE INDEX drug_op_attachments_clinic_idx
    ON drug_op_attachments (clinic_id, uploaded_at DESC)
    WHERE archived_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS drug_op_attachments;

-- +goose StatementEnd
