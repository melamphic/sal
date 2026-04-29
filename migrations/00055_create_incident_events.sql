-- +goose Up
-- +goose StatementBegin

-- Standalone incident log — separate from clinical encounter notes
-- because incidents (falls, medication errors, restraint) often happen
-- outside an encounter and are logged by different staff (night nurses,
-- non-clinical witnesses).
--
-- Fields cover SIRS (AU ACQSC Serious Incident Response Scheme — 24h
-- Priority 1 / 30d Priority 2 deadlines) and CQC notifiable events (UK
-- aged care). The notification_deadline is calculated at insert time
-- from incident_type + severity by the service layer.
--
-- Append-only with addendum chain. Status flow:
--   open → investigating → closed
--   open → escalated → reported_to_regulator
CREATE TABLE incident_events (
    id                       UUID PRIMARY KEY,
    clinic_id                UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    subject_id               UUID NOT NULL REFERENCES subjects(id),
    note_id                  UUID REFERENCES notes(id),

    incident_type            TEXT NOT NULL
        CHECK (incident_type IN (
            'fall', 'medication_error', 'restraint', 'behaviour',
            'skin_injury', 'unexplained_injury', 'pressure_injury',
            'unauthorised_absence', 'death', 'complaint',
            'sexual_misconduct', 'neglect', 'psychological_abuse',
            'physical_abuse', 'financial_abuse', 'other'
        )),

    sirs_priority            TEXT
        CHECK (sirs_priority IS NULL OR sirs_priority IN ('priority_1','priority_2')),
    cqc_notifiable           BOOLEAN NOT NULL DEFAULT FALSE,
    cqc_notification_type    TEXT,

    severity                 TEXT NOT NULL
        CHECK (severity IN ('low','medium','high','critical')),

    occurred_at              TIMESTAMPTZ NOT NULL,
    location                 TEXT,
    brief_description        TEXT NOT NULL,

    immediate_actions        TEXT,
    witnesses_text           TEXT,

    -- subject_outcome describes what happened TO the resident/patient
    -- (no_harm, minor_injury, hospitalised, deceased, complaint_resolved).
    -- Distinct from severity (which describes the event). Both feed
    -- regulator notifications: SIRS Priority 1 / CQC notifiable depend on
    -- subject_outcome rising over time, not just initial severity.
    subject_outcome          TEXT
        CHECK (subject_outcome IS NULL OR subject_outcome IN (
            'no_harm','minor_injury','moderate_injury',
            'hospitalised','deceased','complaint_resolved','unknown'
        )),

    nok_notified_at          TIMESTAMPTZ,
    gp_notified_at           TIMESTAMPTZ,
    regulator_notified_at    TIMESTAMPTZ,
    -- The reference number/id assigned by the regulator on submission
    -- (SIRS reference, CQC notification ID, HQSC sentinel event ID).
    -- Stored verbatim; audit reports cite this back.
    regulator_reference_number TEXT,

    notification_deadline    TIMESTAMPTZ,

    -- When status moves to 'escalated' (severity rose, regulator review
    -- triggered), the reason is documented for audit defensibility.
    escalation_reason        TEXT,
    escalated_at             TIMESTAMPTZ,
    escalated_by             UUID REFERENCES staff(id),

    reported_by              UUID NOT NULL REFERENCES staff(id),
    reviewed_by              UUID REFERENCES staff(id),
    reviewed_at              TIMESTAMPTZ,

    preventive_plan_summary  TEXT,
    care_plan_updated_at     TIMESTAMPTZ,

    status                   TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','investigating','closed','escalated','reported_to_regulator')),

    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Staff witnesses (M:M).
CREATE TABLE incident_witnesses (
    incident_id   UUID NOT NULL REFERENCES incident_events(id) ON DELETE CASCADE,
    staff_id      UUID NOT NULL REFERENCES staff(id),
    PRIMARY KEY (incident_id, staff_id)
);

-- Append-only addendum log for corrections / additional info post-close.
CREATE TABLE incident_addendums (
    id              UUID PRIMARY KEY,
    incident_id     UUID NOT NULL REFERENCES incident_events(id) ON DELETE CASCADE,
    addendum_text   TEXT NOT NULL,
    added_by        UUID NOT NULL REFERENCES staff(id),
    added_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX incident_events_clinic_idx
    ON incident_events (clinic_id, occurred_at DESC);

CREATE INDEX incident_events_subject_idx
    ON incident_events (subject_id, occurred_at DESC);

-- Supports the deadline-countdown background job.
CREATE INDEX incident_events_pending_notification_idx
    ON incident_events (notification_deadline)
    WHERE regulator_notified_at IS NULL
      AND notification_deadline IS NOT NULL
      AND status NOT IN ('closed','reported_to_regulator');

CREATE INDEX incident_addendums_incident_idx
    ON incident_addendums (incident_id, added_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS incident_addendums;
DROP TABLE IF EXISTS incident_witnesses;
DROP TABLE IF EXISTS incident_events;

-- +goose StatementEnd
