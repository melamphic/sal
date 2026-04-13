-- +goose Up
-- +goose StatementBegin

-- form_groups: logical folders for organising forms within a clinic.
-- Deleting a group sets forms' group_id to NULL (forms become ungrouped).
CREATE TABLE form_groups (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id   UUID        NOT NULL REFERENCES clinics(id),
    name        TEXT        NOT NULL,
    description TEXT,
    created_by  UUID        NOT NULL REFERENCES staff(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_form_groups_clinic_id ON form_groups(clinic_id);

CREATE TRIGGER form_groups_updated_at
    BEFORE UPDATE ON form_groups
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- forms: top-level form definition (metadata only).
-- All structural data lives on form_versions and form_fields.
-- tags is a plain text array — free-form labels defined by clinic staff.
-- overall_prompt is fed to the AI alongside field-level prompts to provide
-- context about the form's purpose during extraction.
CREATE TABLE forms (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id      UUID        NOT NULL REFERENCES clinics(id),
    -- group_id is nullable — forms can be ungrouped.
    -- ON DELETE SET NULL: deleting a group ungroups its forms.
    group_id       UUID        REFERENCES form_groups(id) ON DELETE SET NULL,
    name           TEXT        NOT NULL,
    description    TEXT,
    overall_prompt TEXT,
    -- tags: free-form strings, e.g. {"neurology","emergency","rabbit"}.
    tags           TEXT[]      NOT NULL DEFAULT '{}',
    created_by     UUID        NOT NULL REFERENCES staff(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at    TIMESTAMPTZ,
    -- retire_reason is set alongside archived_at when a form is retired/unpublished.
    retire_reason  TEXT
);

CREATE INDEX idx_forms_clinic_id   ON forms(clinic_id);
CREATE INDEX idx_forms_group_id    ON forms(group_id) WHERE group_id IS NOT NULL;
CREATE INDEX idx_forms_tags        ON forms USING GIN(tags);
CREATE INDEX idx_forms_archived_at ON forms(clinic_id) WHERE archived_at IS NULL;

CREATE TRIGGER forms_updated_at
    BEFORE UPDATE ON forms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- form_versions: immutable snapshots of a form after publish.
-- Exactly one draft per form is enforced by the partial unique index below.
-- version_major and version_minor are NULL on the draft; assigned at publish.
-- change_type: major = field structure changed; minor = metadata/config only.
-- rollback_of: non-NULL when this version was created by rolling back to a
-- previous published version (preserves audit trail).
-- policy_check_result and policy_check_by are populated at publish time if
-- linked policies were checked before publishing.
CREATE TABLE form_versions (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    form_id              UUID        NOT NULL REFERENCES forms(id),
    status               VARCHAR     NOT NULL DEFAULT 'draft'
                                     CHECK (status IN ('draft', 'published', 'archived')),
    version_major        INT,
    version_minor        INT,
    change_type          VARCHAR     CHECK (change_type IN ('minor', 'major')),
    change_summary       TEXT,
    rollback_of          UUID        REFERENCES form_versions(id),
    -- Stored at publish time — the raw AI response from the policy check.
    policy_check_result  TEXT,
    policy_check_by      UUID        REFERENCES staff(id),
    policy_check_at      TIMESTAMPTZ,
    published_at         TIMESTAMPTZ,
    published_by         UUID        REFERENCES staff(id),
    created_by           UUID        NOT NULL REFERENCES staff(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- No updated_at: versions are immutable once published.
    -- The draft row is updated via explicit columns only.
);

-- Enforce one draft per form at the database level.
CREATE UNIQUE INDEX idx_form_versions_one_draft
    ON form_versions(form_id) WHERE status = 'draft';

CREATE INDEX idx_form_versions_form_id ON form_versions(form_id);
CREATE INDEX idx_form_versions_form_published
    ON form_versions(form_id, published_at DESC) WHERE status = 'published';

-- form_fields: individual fields on a specific form version.
-- type is an open string — new field types never require a schema migration.
-- config is type-specific JSONB, e.g.:
--   slider:    {"min": 0, "max": 100, "step": 5, "unit": "kg"}
--   select:    {"options": ["yes","no","uncertain"]}
--   blocks:    {"count": 10, "labels": ["none","mild","severe"]}
-- ai_prompt: what the AI extraction model looks for in the audio transcript.
-- required: if true, the extraction job must find a value or flag it.
-- skippable: if true, this field is excluded from AI extraction entirely;
--   the reviewer fills it manually. Required fields take precedence.
-- position: 1-based display order; reordering updates all positions atomically.
CREATE TABLE form_fields (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    form_version_id UUID        NOT NULL REFERENCES form_versions(id),
    position        INT         NOT NULL,
    title           TEXT        NOT NULL,
    type            TEXT        NOT NULL,
    config          JSONB       NOT NULL DEFAULT '{}',
    ai_prompt       TEXT,
    required        BOOLEAN     NOT NULL DEFAULT false,
    skippable       BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_form_fields_version_id ON form_fields(form_version_id, position);

CREATE TRIGGER form_fields_updated_at
    BEFORE UPDATE ON form_fields
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- form_policies: many-to-many link between forms and policies.
-- policy_id is stored as UUID without FK — the policies module is not yet
-- built. The FK will be added in a future migration.
CREATE TABLE form_policies (
    form_id    UUID        NOT NULL REFERENCES forms(id),
    policy_id  UUID        NOT NULL,
    linked_by  UUID        NOT NULL REFERENCES staff(id),
    linked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (form_id, policy_id)
);

CREATE INDEX idx_form_policies_form_id ON form_policies(form_id);

-- clinic_form_style_versions: immutable version history of a clinic's PDF style settings.
-- Each UPDATE creates a new row (version++). The active style is the row with the
-- highest version for the clinic. This table is separate from form versions so that
-- a single style change does not create a new version on every form.
-- logo_key: object-storage key for the clinic logo image.
-- primary_color: hex CSS colour e.g. "#3B82F6".
-- font_family: font name recognised by the Flutter PDF renderer.
-- header_extra: any extra text below the clinic name/logo in the PDF header.
-- footer_text: custom footer text; the form version and approver are appended automatically.
CREATE TABLE clinic_form_style_versions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id     UUID        NOT NULL REFERENCES clinics(id),
    version       INT         NOT NULL,
    logo_key      TEXT,
    primary_color VARCHAR(7),
    font_family   TEXT,
    header_extra  TEXT,
    footer_text   TEXT,
    created_by    UUID        NOT NULL REFERENCES staff(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (clinic_id, version)
);

CREATE INDEX idx_clinic_form_style_clinic_id ON clinic_form_style_versions(clinic_id, version DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS clinic_form_style_versions;
DROP TABLE IF EXISTS form_policies;
DROP TABLE IF EXISTS form_fields;
DROP TABLE IF EXISTS form_versions;
DROP TABLE IF EXISTS forms;
DROP TABLE IF EXISTS form_groups;

-- +goose StatementEnd
