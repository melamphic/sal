-- +goose Up
-- +goose StatementBegin

-- Compliance onboarding step. Inserted between clinic_profile (step 0) and
-- the existing invite_team / pdf_brand / done steps, so the step machine
-- shifts: 0=profile, 1=COMPLIANCE (new), 2=invite_team, 3=pdf_brand, 4=done
-- becomes 5 with compliance inserted. Existing tenants past step 0 are
-- grandfathered (compliance_onboarding_version='grandfathered_v0') and
-- shifted +1 so they land on the same conceptual step they were on.
--
-- Field rationale (sources: NZ Privacy Act 2020 s 201; HIPC 2020 Rule 12;
-- AU Privacy Act 1988 APPs 1.3 / 5.2(i)(j) / 8 / 11; My Health Records
-- Act 2012 Rule 42; AU Voluntary AI Safety Standard 2024; OAIC AI guidance):
--
--   privacy_officer_{name,email,phone}    NZ s201 mandates a designated
--                                         Privacy Officer with publishable
--                                         contact details. AU treats this
--                                         as best practice (APP 1).
--   po_training_attested_at               Clinic attests the PO has been
--                                         briefed on the privacy program.
--                                         Boolean attestation timestamp.
--   cross_border_ack_at + _version        AU APP 8 / NZ HIPC Rule 12
--                                         require informed acknowledgement
--                                         that data flows offshore (Deepgram
--                                         US, Gemini Vertex AI). Version
--                                         pins which disclosure copy was
--                                         shown so future copy changes
--                                         re-prompt acceptance.
--   mhr_registered                        AU My Health Records optional
--                                         flag — drives whether MHR-write
--                                         features surface in the UI. NULL
--                                         outside AU.
--   ai_oversight_ack_at                   AU Voluntary AI Safety Standard
--                                         + OAIC: clinician must acknowledge
--                                         AI is decision-support; human
--                                         must verify all output before sign.
--   patient_consent_ack_at                Clinic confirms responsibility for
--                                         obtaining patient consent for
--                                         audio capture + AI processing.
--   dpa_accepted_at + dpa_version         Salvia's DPA (data processing
--                                         agreement). Versioned so we can
--                                         re-prompt on amendment.
--   compliance_onboarding_*               Audit trail: who completed it,
--                                         from where, against which version.
--
-- All TIMESTAMPTZ-NULL fields follow the standard pattern: null = not
-- attested, set = attested at that instant. No separate booleans — the
-- timestamp's nullability is the boolean.

ALTER TABLE clinics
    ADD COLUMN privacy_officer_name             TEXT,
    ADD COLUMN privacy_officer_email            TEXT,
    ADD COLUMN privacy_officer_phone            TEXT,
    ADD COLUMN po_training_attested_at          TIMESTAMPTZ,
    ADD COLUMN cross_border_ack_at              TIMESTAMPTZ,
    ADD COLUMN cross_border_ack_version         TEXT,
    ADD COLUMN mhr_registered                   BOOLEAN,
    ADD COLUMN ai_oversight_ack_at              TIMESTAMPTZ,
    ADD COLUMN patient_consent_ack_at           TIMESTAMPTZ,
    ADD COLUMN dpa_accepted_at                  TIMESTAMPTZ,
    ADD COLUMN dpa_version                      TEXT,
    ADD COLUMN compliance_onboarding_completed_at TIMESTAMPTZ,
    ADD COLUMN compliance_onboarding_version    TEXT,
    ADD COLUMN compliance_onboarding_ip         INET,
    ADD COLUMN compliance_onboarding_user_id    UUID REFERENCES staff(id) ON DELETE SET NULL;

-- Grandfather existing tenants that already advanced past clinic_profile.
-- They never saw the compliance step; mark them with grandfathered_v0 so
-- the UI can surface a non-blocking banner asking them to complete it
-- post-hoc, and shift their step pointer +1 to keep them on the same
-- conceptual step they were on.
UPDATE clinics
   SET onboarding_step              = onboarding_step + 1,
       compliance_onboarding_version = 'grandfathered_v0'
 WHERE onboarding_step >= 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse the step shift for grandfathered rows before dropping columns.
UPDATE clinics
   SET onboarding_step = onboarding_step - 1
 WHERE compliance_onboarding_version = 'grandfathered_v0'
   AND onboarding_step >= 2;

ALTER TABLE clinics
    DROP COLUMN IF EXISTS compliance_onboarding_user_id,
    DROP COLUMN IF EXISTS compliance_onboarding_ip,
    DROP COLUMN IF EXISTS compliance_onboarding_version,
    DROP COLUMN IF EXISTS compliance_onboarding_completed_at,
    DROP COLUMN IF EXISTS dpa_version,
    DROP COLUMN IF EXISTS dpa_accepted_at,
    DROP COLUMN IF EXISTS patient_consent_ack_at,
    DROP COLUMN IF EXISTS ai_oversight_ack_at,
    DROP COLUMN IF EXISTS mhr_registered,
    DROP COLUMN IF EXISTS cross_border_ack_version,
    DROP COLUMN IF EXISTS cross_border_ack_at,
    DROP COLUMN IF EXISTS po_training_attested_at,
    DROP COLUMN IF EXISTS privacy_officer_phone,
    DROP COLUMN IF EXISTS privacy_officer_email,
    DROP COLUMN IF EXISTS privacy_officer_name;

-- +goose StatementEnd
