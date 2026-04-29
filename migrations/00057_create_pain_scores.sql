-- +goose Up
-- +goose StatementBegin

-- Per-subject pain scores over time. Three input methods:
--   manual                — clinician sets the score directly
--   painchek              — future integration with PainChek facial
--                           assessment (not used in v1)
--   extracted_from_audio  — score harvested from encounter transcript
--                           via existing field-extraction pipeline
--
-- 0–10 numeric rating scale. Plotted in the patient profile timeline
-- and used by the pre-encounter brief to flag pain-trend changes.
CREATE TABLE pain_scores (
    id              UUID PRIMARY KEY,
    clinic_id       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    subject_id      UUID NOT NULL REFERENCES subjects(id),
    note_id         UUID REFERENCES notes(id),

    score           SMALLINT NOT NULL
        CHECK (score BETWEEN 0 AND 10),
    note            TEXT,

    method          TEXT NOT NULL
        CHECK (method IN ('manual','painchek','extracted_from_audio','flacc_observed','wong_baker')),

    -- Pain rating instrument used. Defaults to 'nrs' (Numeric Rating
    -- Scale 0-10) which is universal. Aged care w/ non-verbal residents
    -- often uses 'flacc' (face/legs/activity/cry/consolability) or
    -- 'painad' (dementia pain assessment); paediatric uses 'wong_baker'.
    pain_scale_used TEXT NOT NULL DEFAULT 'nrs'
        CHECK (pain_scale_used IN ('nrs','flacc','painad','wong_baker','vrs','vas')),

    assessed_by     UUID NOT NULL REFERENCES staff(id),
    assessed_at     TIMESTAMPTZ NOT NULL,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX pain_scores_subject_time_idx
    ON pain_scores (subject_id, assessed_at DESC);

CREATE INDEX pain_scores_clinic_idx
    ON pain_scores (clinic_id, assessed_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS pain_scores;

-- +goose StatementEnd
