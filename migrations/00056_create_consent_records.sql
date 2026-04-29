-- +goose Up
-- +goose StatementBegin

-- Per-subject consent records. Standalone module: consent capture is a
-- discrete event tied to a subject + scope, not embedded in an
-- encounter form. A single subject can carry many consent records over
-- time (sedation today, telemedicine in 6 months, photography for case
-- study at the next visit, etc.).
--
-- Capture methods cover the spectrum:
--   verbal_clinic        — verbal in clinic, witness staff_id required
--   written_signature    — paper or electronic signed PDF stored in S3
--   electronic_signature — signature pad / portal sig
--   guardian             — captured from authorised representative
--
-- AI-supported flows (Phase C):
--   risks_discussed and alternatives_discussed can be AI-drafted from
--   the procedure description; user reviews before commit.
--   transcript_recording_id points at the verbal-consent recording so
--   the transcript serves as the audit-defensible record.
CREATE TABLE consent_records (
    id                       UUID PRIMARY KEY,
    clinic_id                UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    subject_id               UUID NOT NULL REFERENCES subjects(id),
    note_id                  UUID REFERENCES notes(id),

    consent_type             TEXT NOT NULL
        CHECK (consent_type IN (
            'audio_recording','ai_processing','telemedicine',
            'sedation','euthanasia','invasive_procedure',
            'mhr_write','photography','data_sharing',
            'controlled_drug_administration','treatment_plan','other'
        )),

    scope                    TEXT NOT NULL,
    procedure_or_form_id     UUID,

    risks_discussed          TEXT,
    alternatives_discussed   TEXT,

    captured_via             TEXT NOT NULL
        CHECK (captured_via IN (
            'verbal_clinic','verbal_telehealth','written_signature','electronic_signature','guardian'
        )),
    signature_image_key      TEXT,
    transcript_recording_id  UUID REFERENCES recordings(id),

    -- Who actually consented? In vet, owner of the animal; in dental,
    -- patient or guardian for under-18; in aged care, resident or EPOA
    -- when resident lacks capacity. Captured here so the regulator-
    -- defensible trail is complete (vs. just "captured_by clinician").
    consenting_party_relationship TEXT
        CHECK (consenting_party_relationship IN (
            'self','owner','guardian','epoa','nok','authorised_representative','other'
        )),
    consenting_party_name    TEXT,

    -- Forward link to a future capacity_assessments table (aged care
    -- consent under MCA / DoLS context). NULL when capacity is presumed
    -- intact. No FK for now since the table doesn't exist; reserved.
    capacity_assessment_id   UUID,

    captured_by              UUID NOT NULL REFERENCES staff(id),
    captured_at              TIMESTAMPTZ NOT NULL,
    witness_id               UUID REFERENCES staff(id),

    expires_at               TIMESTAMPTZ,
    -- When the consent must be re-affirmed even if not yet expired
    -- (e.g. aged-care MHR consent reviewed annually). Distinct from
    -- expires_at: a consent can be valid AND due for renewal review.
    renewal_due_at           TIMESTAMPTZ,
    withdrawal_at            TIMESTAMPTZ,
    withdrawal_reason        TEXT,

    -- Provenance metadata if AI assisted with risks/alternatives drafting.
    -- JSONB: {provider, model, prompt_hash, drafted_at}. NULL for fully
    -- human-authored consents.
    ai_assistance_metadata   JSONB,

    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Verbal-clinic consent requires a witness (regulator-grade
    -- defensibility); other capture methods don't.
    CONSTRAINT consent_verbal_requires_witness
        CHECK (captured_via <> 'verbal_clinic' OR witness_id IS NOT NULL)
);

CREATE INDEX consent_records_subject_idx
    ON consent_records (subject_id, captured_at DESC);

CREATE INDEX consent_records_clinic_idx
    ON consent_records (clinic_id, captured_at DESC);

CREATE INDEX consent_records_expiring_idx
    ON consent_records (expires_at)
    WHERE expires_at IS NOT NULL
      AND withdrawal_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS consent_records;

-- +goose StatementEnd
