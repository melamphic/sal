-- +goose Up
-- +goose StatementBegin

-- AI drafts produced from a recording's transcript. Universal across
-- target domains (incident, consent, future pain / pre-encounter brief).
-- The clinician requests a draft, audio is transcribed by the existing
-- audio pipeline, and the audio TranscribeAudioWorker fans out via the
-- TranscriptListener interface to the aidrafts worker, which calls the
-- relevant aigen.*DraftService and stores the structured result in
-- draft_payload. The Flutter modal polls /api/v1/ai-drafts/{id} until
-- status flips to `done`, then prefills the create-incident /
-- create-consent form with the values for the clinician to review.
--
-- target_type controls which aigen service runs:
--   incident → aigen.IncidentDraftService
--   consent  → aigen.ConsentDraftService
--   (future) pain · pre_encounter_brief
--
-- Status flow:
--   pending_transcript → extracting → done | failed
--
-- The draft row is the source of truth for the modal — the Flutter
-- client never reads the recording transcript directly. Drafts are
-- never auto-applied to the target domain; they are a UI prefill.
CREATE TABLE ai_drafts (
    id              UUID PRIMARY KEY,
    clinic_id       UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,

    target_type     TEXT NOT NULL
        CHECK (target_type IN ('incident', 'consent', 'pain', 'pre_encounter_brief')),

    recording_id    UUID REFERENCES recordings(id),

    -- Optional context the caller passes alongside the recording. For
    -- consent drafts, this carries the procedure description + audience.
    -- For incidents, leave empty — the transcript is the entire input.
    context_payload JSONB,

    -- Filled after extraction. Shape mirrors aigen.GeneratedXxxDraft;
    -- the Flutter side knows which fields to read based on target_type.
    draft_payload   JSONB,

    status          TEXT NOT NULL DEFAULT 'pending_transcript'
        CHECK (status IN ('pending_transcript', 'extracting', 'done', 'failed')),

    error_message   TEXT,

    ai_provider     TEXT,
    ai_model        TEXT,
    prompt_hash     TEXT,

    requested_by    UUID NOT NULL REFERENCES staff(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX ai_drafts_clinic_idx
    ON ai_drafts (clinic_id, created_at DESC);

-- Used by the audio TranscribeAudioWorker listener to find every draft
-- waiting on a freshly-completed recording.
CREATE INDEX ai_drafts_recording_idx
    ON ai_drafts (recording_id)
    WHERE status = 'pending_transcript';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS ai_drafts;

-- +goose StatementEnd
