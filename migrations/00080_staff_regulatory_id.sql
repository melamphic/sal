-- +goose Up
-- +goose StatementBegin

-- Capture each clinician's regulatory registration so the body of every
-- generated PDF (signed clinical notes, controlled-drug register
-- entries, incident reports, MAR sign-offs) can cite it the way a
-- regulator-defensible record demands. NZ vets need the VCNZ number;
-- UK GPs the GMC number; UK nurses the NMC PIN; US vets the AVMA
-- license; AU clinicians the AHPRA registration. Schema-wise the shape
-- is the same — authority + identifier — and we don't want to bake a
-- per-jurisdiction column set when one tuple covers all of them.
--
-- Both NULLable. Existing staff rows keep working until the user fills
-- the value in via staff settings (P3-P backfills the UI).
ALTER TABLE staff
    ADD COLUMN regulatory_authority TEXT,
    ADD COLUMN regulatory_reg_no    TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE staff
    DROP COLUMN IF EXISTS regulatory_reg_no,
    DROP COLUMN IF EXISTS regulatory_authority;
-- +goose StatementEnd
