-- +goose Up

CREATE TABLE note_events (
    id          UUID        PRIMARY KEY,
    note_id     UUID        NOT NULL REFERENCES notes(id),
    subject_id  UUID        REFERENCES subjects(id),
    clinic_id   UUID        NOT NULL,
    event_type  TEXT        NOT NULL,
    field_id    UUID        REFERENCES form_fields(id),
    old_value   JSONB,
    new_value   JSONB,
    actor_id    UUID        NOT NULL,
    actor_role  TEXT        NOT NULL DEFAULT '',
    reason      TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON note_events (clinic_id,  occurred_at DESC);
CREATE INDEX ON note_events (note_id,    occurred_at ASC);
CREATE INDEX ON note_events (subject_id, occurred_at ASC) WHERE subject_id IS NOT NULL;
CREATE INDEX ON note_events (actor_id,   occurred_at DESC);

-- Notify the SSE broker whenever a new event is inserted.
-- Payload format: <clinic_id>:<event_id>:<note_id>:<event_type>
-- +goose StatementBegin
CREATE FUNCTION notify_note_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'salvia_note_events',
        NEW.clinic_id::text || ':' || NEW.id::text || ':' || NEW.note_id::text || ':' || NEW.event_type
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER note_events_after_insert
AFTER INSERT ON note_events
FOR EACH ROW EXECUTE FUNCTION notify_note_event();

-- +goose Down

DROP TRIGGER IF EXISTS note_events_after_insert ON note_events;
DROP FUNCTION IF EXISTS notify_note_event();
DROP TABLE IF EXISTS note_events;
