-- +goose Up
-- +goose StatementBegin

-- subjects is the generic "thing being documented" entity.
-- For veterinary: an animal. For dental: a human patient. For aged care: a resident.
-- Vertical-specific fields live in extension tables (vet_subject_details, etc.)
-- and are joined at the service layer. This table never changes when a new vertical is added.
CREATE TABLE subjects (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id    UUID        NOT NULL REFERENCES clinics(id),
    -- contact_id is nullable — a subject can exist without a linked contact
    -- and one can be linked later via PATCH /patients/{id}/contact.
    contact_id   UUID        REFERENCES contacts(id),
    -- display_name is the human-readable label shown in the UI.
    -- Vet: "Buddy" or "Bella (Labrador)". Dental: "James Smith". Aged care: "Mary White".
    display_name TEXT        NOT NULL,
    status       VARCHAR     NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active', 'deceased', 'transferred', 'archived')),
    -- vertical mirrors the owning clinic's vertical — stored here for query efficiency
    -- and to support future cross-vertical reports without joining clinics.
    vertical     VARCHAR     NOT NULL
                             CHECK (vertical IN ('veterinary', 'dental', 'aged_care')),
    -- created_by is set from the authenticated staff JWT and never changes.
    created_by   UUID        NOT NULL REFERENCES staff(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Soft-delete: subject records are the audit anchor for all clinical records.
    archived_at  TIMESTAMPTZ
);

CREATE INDEX idx_subjects_clinic_id         ON subjects(clinic_id);
CREATE INDEX idx_subjects_contact_id        ON subjects(contact_id) WHERE contact_id IS NOT NULL;
CREATE INDEX idx_subjects_clinic_status     ON subjects(clinic_id, status);
CREATE INDEX idx_subjects_clinic_created_at ON subjects(clinic_id, created_at DESC);

CREATE TRIGGER subjects_updated_at
    BEFORE UPDATE ON subjects
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS subjects_updated_at ON subjects;
DROP TABLE IF EXISTS subjects;

-- +goose StatementEnd
