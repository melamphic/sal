-- +goose Up
-- +goose StatementBegin

CREATE TABLE staff (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id     UUID        NOT NULL REFERENCES clinics(id),
    -- PII: encrypted
    email         TEXT        NOT NULL,
    -- Deterministic HMAC hash of lowercased email for uniqueness and lookup.
    -- Index is on (clinic_id, email_hash) because email uniqueness is per-clinic.
    email_hash    TEXT        NOT NULL,
    -- PII: encrypted
    full_name     TEXT        NOT NULL,
    role          VARCHAR     NOT NULL
                              CHECK (role IN ('super_admin', 'admin', 'vet', 'vet_nurse', 'receptionist')),
    -- note_tier determines billing (standard = counts toward tier) and quota (nurse = 50%).
    note_tier     VARCHAR     NOT NULL DEFAULT 'none'
                              CHECK (note_tier IN ('standard', 'nurse', 'none')),
    -- ── Permissions ──────────────────────────────────────────────────────────
    -- Stored as individual columns for query efficiency and clarity.
    -- Granted at invite time; super_admin can update any time.
    perm_manage_staff          BOOLEAN NOT NULL DEFAULT false,
    perm_manage_forms          BOOLEAN NOT NULL DEFAULT false,
    perm_manage_policies       BOOLEAN NOT NULL DEFAULT false,
    perm_manage_billing        BOOLEAN NOT NULL DEFAULT false,
    perm_rollback_policies     BOOLEAN NOT NULL DEFAULT false,
    perm_record_audio          BOOLEAN NOT NULL DEFAULT false,
    perm_submit_forms          BOOLEAN NOT NULL DEFAULT false,
    perm_view_all_patients     BOOLEAN NOT NULL DEFAULT false,
    perm_view_own_patients     BOOLEAN NOT NULL DEFAULT false,
    perm_dispense              BOOLEAN NOT NULL DEFAULT false,
    perm_generate_audit_export BOOLEAN NOT NULL DEFAULT false,
    -- ── Lifecycle ────────────────────────────────────────────────────────────
    status         VARCHAR     NOT NULL DEFAULT 'invited'
                               CHECK (status IN ('invited', 'active', 'deactivated')),
    last_active_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Soft-delete only — staff records are preserved for audit trail integrity.
    archived_at    TIMESTAMPTZ,
    -- Email uniqueness is enforced per-clinic via the hash (encrypted email is
    -- non-deterministic so a plain UNIQUE on email would not work).
    UNIQUE (clinic_id, email_hash)
);

CREATE INDEX idx_staff_clinic_id ON staff(clinic_id);
-- Used for email lookup during magic link flow and invite deduplication.
CREATE INDEX idx_staff_email_hash ON staff(email_hash);

CREATE TRIGGER staff_updated_at
    BEFORE UPDATE ON staff
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS staff_updated_at ON staff;
DROP TABLE IF EXISTS staff;

-- +goose StatementEnd
