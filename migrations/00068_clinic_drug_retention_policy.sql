-- +goose Up
-- +goose StatementBegin

-- Compliance v2: per-clinic retention policy. Each country has different
-- minimum retention periods for the CD register, reconciliation records,
-- and aged-care MAR. We seed sensible defaults from clinic.country at
-- creation; per-clinic override is allowed but only upward (a clinic
-- may keep records longer than the legal floor, never shorter — enforced
-- in service).
--
-- Verified retention floors (May 2026 legislation text):
--   NZ: CD register 4y (MDR 1977 Reg 42) · general health 10y
--       (Health (Retention of Health Info) Regs 1996 Reg 2)
--   UK: CD register 2y (MDR 2001) · MAR 8y (NICE NG67)
--   US: 2y federal floor (21 CFR 1304.04(a)) — we default to 7y to cover
--       strictest state (MA / WA)
--   AU: state-by-state, default 7y to cover state Health Records Acts
--
-- Design: docs/drug-register-compliance-v2.md §4.3
CREATE TABLE clinic_drug_retention_policy (
    clinic_id     UUID PRIMARY KEY REFERENCES clinics(id) ON DELETE CASCADE,
    ledger_years  INTEGER NOT NULL CHECK (ledger_years > 0),
    recon_years   INTEGER NOT NULL CHECK (recon_years > 0),
    mar_years     INTEGER NOT NULL CHECK (mar_years > 0),
    set_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    set_by        UUID REFERENCES staff(id)
);

-- Seed defaults from clinic.country for every existing clinic.
-- Country values are the canonical 2-letter codes ('NZ','UK','US','AU').
INSERT INTO clinic_drug_retention_policy (clinic_id, ledger_years, recon_years, mar_years)
SELECT
    c.id,
    CASE c.country
        WHEN 'NZ' THEN 4
        WHEN 'UK' THEN 2
        WHEN 'US' THEN 7
        WHEN 'AU' THEN 7
        ELSE 7
    END,
    CASE c.country
        WHEN 'NZ' THEN 4
        WHEN 'UK' THEN 2
        WHEN 'US' THEN 7
        WHEN 'AU' THEN 7
        ELSE 7
    END,
    CASE c.country
        WHEN 'NZ' THEN 10
        WHEN 'UK' THEN 8
        WHEN 'US' THEN 7
        WHEN 'AU' THEN 7
        ELSE 8
    END
FROM clinics c
ON CONFLICT (clinic_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS clinic_drug_retention_policy;

-- +goose StatementEnd
