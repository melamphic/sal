-- +goose Up
-- +goose StatementBegin

-- policy_folders: single-level folders for organising policies within a clinic.
-- Deleting a folder sets policies' folder_id to NULL (policies become ungrouped).
CREATE TABLE policy_folders (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id  UUID        NOT NULL REFERENCES clinics(id),
    name       TEXT        NOT NULL,
    created_by UUID        NOT NULL REFERENCES staff(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_policy_folders_clinic_id ON policy_folders(clinic_id);

CREATE TRIGGER policy_folders_updated_at
    BEFORE UPDATE ON policy_folders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- policies: top-level policy definition (metadata only).
-- All content lives on policy_versions.
CREATE TABLE policies (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id     UUID        NOT NULL REFERENCES clinics(id),
    -- folder_id is nullable — policies can be ungrouped.
    -- ON DELETE SET NULL: deleting a folder ungroups its policies.
    folder_id     UUID        REFERENCES policy_folders(id) ON DELETE SET NULL,
    name          TEXT        NOT NULL,
    description   TEXT,
    created_by    UUID        NOT NULL REFERENCES staff(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at   TIMESTAMPTZ,
    retire_reason TEXT
);

CREATE INDEX idx_policies_clinic_id   ON policies(clinic_id);
CREATE INDEX idx_policies_folder_id   ON policies(folder_id) WHERE folder_id IS NOT NULL;
CREATE INDEX idx_policies_archived_at ON policies(clinic_id) WHERE archived_at IS NULL;

CREATE TRIGGER policies_updated_at
    BEFORE UPDATE ON policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- policy_versions: immutable snapshots of a policy after publish.
-- Exactly one draft per policy is enforced by the partial unique index below.
-- version_major and version_minor are NULL on the draft; assigned at publish.
-- content is a JSONB array of editor blocks (AppFlowy-compatible structure).
-- The backend treats content as opaque — rendering is handled by the Flutter client.
-- rollback_of: non-NULL when this version was created by rolling back to a
-- previous published version (preserves audit trail).
CREATE TABLE policy_versions (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_id      UUID        NOT NULL REFERENCES policies(id),
    status         VARCHAR     NOT NULL DEFAULT 'draft'
                               CHECK (status IN ('draft', 'published', 'archived')),
    version_major  INT,
    version_minor  INT,
    change_type    VARCHAR     CHECK (change_type IN ('minor', 'major')),
    change_summary TEXT,
    -- content: opaque JSONB block array from AppFlowy/Flutter editor.
    content        JSONB       NOT NULL DEFAULT '[]',
    rollback_of    UUID        REFERENCES policy_versions(id),
    published_at   TIMESTAMPTZ,
    published_by   UUID        REFERENCES staff(id),
    created_by     UUID        NOT NULL REFERENCES staff(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Enforce one draft per policy at the database level.
CREATE UNIQUE INDEX idx_policy_versions_one_draft
    ON policy_versions(policy_id) WHERE status = 'draft';

CREATE INDEX idx_policy_versions_policy_id ON policy_versions(policy_id);
CREATE INDEX idx_policy_versions_policy_published
    ON policy_versions(policy_id, published_at DESC) WHERE status = 'published';

-- policy_clauses: individual enforceable clauses within a policy version.
-- A clause references a block_id from the content JSONB and carries a parity
-- level indicating how strictly it must be followed:
--   high   = must follow  (non-negotiable compliance requirement)
--   medium = should follow (strong recommendation)
--   low    = try to follow (best-practice guidance)
-- Clauses are replaced atomically via DELETE + INSERT when updated.
CREATE TABLE policy_clauses (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    policy_version_id UUID        NOT NULL REFERENCES policy_versions(id) ON DELETE CASCADE,
    -- block_id: client-assigned identifier for the block within the content JSONB.
    block_id          TEXT        NOT NULL,
    title             TEXT        NOT NULL,
    parity            TEXT        NOT NULL DEFAULT 'medium'
                                  CHECK (parity IN ('high', 'medium', 'low')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (policy_version_id, block_id)
);

CREATE INDEX idx_policy_clauses_version_id ON policy_clauses(policy_version_id);

-- Add FK from form_policies.policy_id → policies.id now that the policies
-- table exists. The column was deliberately left without a FK in migration 9
-- to keep forms and policy modules decoupled at migration time.
ALTER TABLE form_policies
    ADD CONSTRAINT form_policies_policy_id_fk
    FOREIGN KEY (policy_id) REFERENCES policies(id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE form_policies DROP CONSTRAINT IF EXISTS form_policies_policy_id_fk;

DROP TABLE IF EXISTS policy_clauses;
DROP TABLE IF EXISTS policy_versions;
DROP TABLE IF EXISTS policies;
DROP TABLE IF EXISTS policy_folders;

-- +goose StatementEnd
