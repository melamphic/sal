-- +goose Up
-- +goose StatementBegin

-- subject_contacts is the many-to-many join between subjects and contacts,
-- with a role so a subject can have several people tied to them in
-- different capacities (e.g. primary owner + emergency contact + referring
-- vet). subjects.contact_id still exists as the cached "primary" pointer
-- used on list screens so we don't have to join for every row.
CREATE TABLE subject_contacts (
    subject_id UUID NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    role       VARCHAR(40) NOT NULL CHECK (role IN (
        'primary_owner',
        'co_owner',
        'emergency_contact',
        'guardian',
        'next_of_kin',
        'power_of_attorney',
        'referring_provider',
        'other'
    )),
    -- note is a free-text clarifier on the relationship (e.g. "daughter",
    -- "work phone only"). Not encrypted — treat as safe to display beside
    -- the role.
    note       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subject_id, contact_id, role)
);

CREATE INDEX idx_subject_contacts_subject_id ON subject_contacts(subject_id);
CREATE INDEX idx_subject_contacts_contact_id ON subject_contacts(contact_id);

CREATE TRIGGER subject_contacts_updated_at
    BEFORE UPDATE ON subject_contacts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS subject_contacts_updated_at ON subject_contacts;
DROP TABLE IF EXISTS subject_contacts;

-- +goose StatementEnd
