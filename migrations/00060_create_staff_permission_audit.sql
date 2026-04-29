-- +goose Up
-- +goose StatementBegin

-- Append-only log of every staff permission change. Triggered by the
-- repository layer on UPDATE of staff.perm_* columns; one row per flag
-- that changed.
--
-- Compliance answer to "who granted Mary CD-dispensing rights and
-- when?" — a routine question in regulator reviews that most clinical
-- software cannot answer.
CREATE TABLE staff_permission_audit (
    id                UUID PRIMARY KEY,
    clinic_id         UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    staff_id          UUID NOT NULL REFERENCES staff(id),
    permission_name   TEXT NOT NULL,
    old_value         BOOLEAN NOT NULL,
    new_value         BOOLEAN NOT NULL,
    changed_by        UUID NOT NULL REFERENCES staff(id),
    changed_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reason            TEXT,
    audit_context     TEXT NOT NULL DEFAULT 'admin_panel'
        CHECK (audit_context IN (
            'admin_panel','onboarding','auto_provisioned','migration','api'
        )),

    -- A no-op audit row would be wasted storage; require a real change.
    CONSTRAINT staff_permission_audit_change_required
        CHECK (old_value <> new_value)
);

CREATE INDEX staff_permission_audit_staff_idx
    ON staff_permission_audit (staff_id, changed_at DESC);

CREATE INDEX staff_permission_audit_clinic_idx
    ON staff_permission_audit (clinic_id, changed_at DESC);

CREATE INDEX staff_permission_audit_changed_by_idx
    ON staff_permission_audit (changed_by, changed_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS staff_permission_audit;

-- +goose StatementEnd
