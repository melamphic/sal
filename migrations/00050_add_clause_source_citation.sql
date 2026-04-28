-- +goose Up
-- +goose StatementBegin

-- AI-suggested verbatim regulator quote backing a clause. Optional — manual
-- clauses leave this NULL. The Flutter editor renders citations with an
-- explicit "verify against [regulator]" marker so reviewers know the quote
-- is unverified.
ALTER TABLE policy_clauses
    ADD COLUMN source_citation TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE policy_clauses
    DROP COLUMN IF EXISTS source_citation;

-- +goose StatementEnd
