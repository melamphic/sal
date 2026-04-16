-- +goose Up
-- +goose StatementBegin

-- allow_inference: when false, the extraction worker rejects any field value
-- the AI derived via inference (transformation_type='inference'). Only verbatim
-- quotes (transformation_type='direct') are accepted for that field.
-- Defaults true so existing fields are unaffected.
--
-- min_confidence: ASR-grounded confidence floor for this field. If the computed
-- asr_confidence is below this value, the extracted result is flagged requires_review.
-- NULL means no threshold is enforced (accept any score).
ALTER TABLE form_fields
    ADD COLUMN allow_inference BOOLEAN     NOT NULL DEFAULT true,
    ADD COLUMN min_confidence  DECIMAL(4,2) CHECK (min_confidence >= 0 AND min_confidence <= 1);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE form_fields
    DROP COLUMN IF EXISTS min_confidence,
    DROP COLUMN IF EXISTS allow_inference;

-- +goose StatementEnd
