-- +goose Up
-- +goose StatementBegin

-- Capture per-clinic regulatory identifiers as a flexible JSONB blob
-- rather than 6+ nullable columns. Different (vertical, country) cells
-- need different identifiers and the set will only grow:
--
--   {
--     "nzbn": "9429048372910",                -- NZ vet, NZ GP, NZ aged care
--     "cqc_location_id": "1-247118331",       -- UK aged care, UK GP
--     "ods_code": "G83001",                   -- UK NHS practice code
--     "dea_id": "BC1234567",                  -- US (also lives in dea_registrations
--                                                  table for the structured DEA flow)
--     "ahpra_practice_id": "ABC1234567",      -- AU practice
--     "vmd_premises_id": "PREM-12345",        -- UK vet
--     "rcvs_practice_id": "P-04412",          -- UK vet RCVS PSS
--     "acqsc_provider_id": "PROV-2025-009",   -- AU aged care
--     "abn": "12345678901"                    -- AU clinics
--   }
--
-- The PDF renderer pulls whichever identifier the (vertical, country)
-- of the clinic implies. Keys without a value are simply absent.
--
-- Default '{}' so inserts that don't set the column don't violate
-- NOT NULL.
ALTER TABLE clinics
    ADD COLUMN regulatory_ids JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE clinics
    DROP COLUMN IF EXISTS regulatory_ids;
-- +goose StatementEnd
