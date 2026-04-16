-- +goose Up

-- note_events: clinic_id and actor_id were missing FK references.
ALTER TABLE note_events
    ADD CONSTRAINT fk_note_events_clinic FOREIGN KEY (clinic_id) REFERENCES clinics(id),
    ADD CONSTRAINT fk_note_events_actor  FOREIGN KEY (actor_id)  REFERENCES staff(id);

-- report_jobs: clinic_id and created_by were missing FK references.
ALTER TABLE report_jobs
    ADD CONSTRAINT fk_report_jobs_clinic     FOREIGN KEY (clinic_id)  REFERENCES clinics(id),
    ADD CONSTRAINT fk_report_jobs_created_by FOREIGN KEY (created_by) REFERENCES staff(id);

-- +goose Down

ALTER TABLE note_events
    DROP CONSTRAINT IF EXISTS fk_note_events_clinic,
    DROP CONSTRAINT IF EXISTS fk_note_events_actor;

ALTER TABLE report_jobs
    DROP CONSTRAINT IF EXISTS fk_report_jobs_clinic,
    DROP CONSTRAINT IF EXISTS fk_report_jobs_created_by;
