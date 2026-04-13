-- +goose Up
-- +goose StatementBegin

-- contacts holds the owner / client for a subject (animal, patient, resident).
-- One contact can have many subjects (e.g. one owner with multiple pets).
-- All PII fields are encrypted at the application layer before INSERT.
CREATE TABLE contacts (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id  UUID        NOT NULL REFERENCES clinics(id),
    -- PII: encrypted
    full_name  TEXT        NOT NULL,
    -- PII: encrypted
    phone      TEXT,
    -- PII: encrypted
    email      TEXT,
    -- Deterministic HMAC hash of lowercased email for lookup and deduplication.
    email_hash TEXT,
    -- PII: encrypted
    address    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Soft-delete: contact records are preserved for audit trail integrity.
    archived_at TIMESTAMPTZ
);

CREATE INDEX idx_contacts_clinic_id  ON contacts(clinic_id);
CREATE INDEX idx_contacts_email_hash ON contacts(email_hash) WHERE email_hash IS NOT NULL;

CREATE TRIGGER contacts_updated_at
    BEFORE UPDATE ON contacts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS contacts_updated_at ON contacts;
DROP TABLE IF EXISTS contacts;

-- +goose StatementEnd
