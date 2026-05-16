-- +goose Up
CREATE TABLE note_policy_checks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id     UUID        NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    clinic_id   UUID        NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    result      JSONB       NOT NULL,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX note_policy_checks_note_id_idx ON note_policy_checks (note_id, clinic_id, checked_at DESC);

-- +goose Down
DROP TABLE note_policy_checks;
