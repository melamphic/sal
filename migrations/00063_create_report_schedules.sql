-- +goose Up
-- +goose StatementBegin

-- Recurring compliance-report schedules.
--
-- A schedule fires at next_run_at, generates a report (via the existing
-- compliance pipeline in internal/reports), and emails the recipients
-- when generation completes. The fire-loop is a River periodic job that
-- runs hourly and picks up rows whose next_run_at is in the past.
--
-- Frequencies:
--   daily      → bumps next_run_at by 1 day
--   weekly     → bumps by 7 days
--   monthly    → bumps to the 1st of the following month
--   quarterly  → bumps to the 1st of the next calendar quarter
--
-- Period of the generated report is derived from frequency:
--   daily    → previous day  (yesterday 00:00 → 23:59:59)
--   weekly   → previous 7 days
--   monthly  → previous calendar month
--   quarterly → previous calendar quarter
--
-- Recipients are an immutable snapshot at fire-time: the row stores the
-- list, the report's delivered_to_emails column on `reports` captures
-- exactly what we sent. Editing recipients on the schedule changes
-- future fires only.
CREATE TABLE report_schedules (
    id              UUID PRIMARY KEY,
    clinic_id       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,

    report_type     TEXT NOT NULL,
    frequency       TEXT NOT NULL
        CHECK (frequency IN ('daily', 'weekly', 'monthly', 'quarterly')),

    recipients      TEXT[] NOT NULL,

    paused          BOOLEAN NOT NULL DEFAULT FALSE,

    next_run_at     TIMESTAMPTZ NOT NULL,
    last_run_at     TIMESTAMPTZ,
    last_report_id  UUID REFERENCES reports(id),

    created_by      UUID NOT NULL REFERENCES staff(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT report_schedules_recipients_nonempty
        CHECK (array_length(recipients, 1) >= 1)
);

CREATE INDEX report_schedules_clinic_idx
    ON report_schedules (clinic_id, created_at DESC);

-- The fire loop scans this index every hour. Filtering on paused + due
-- so the planner can skip the rest.
CREATE INDEX report_schedules_due_idx
    ON report_schedules (next_run_at)
    WHERE paused = FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS report_schedules;

-- +goose StatementEnd
