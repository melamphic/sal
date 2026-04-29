-- +goose Up
-- +goose StatementBegin

-- Compliance report generation jobs + per-report audit log.
--
-- The reports table is BOTH a job queue (status/file_key/started_at)
-- AND the report registry (report_hash for tamper detection, who
-- requested, when completed). Long-running generation runs as a River
-- worker; the row holds the state machine.
--
-- Status flow:
--   queued → running → done | failed
--
-- type identifies the report — one of:
--   audit_pack                    (zip bundle of all sub-reports)
--   records_activity              (universal)
--   policy_compliance             (universal)
--   subject_access_audit          (universal)
--   ai_provenance                 (universal)
--   note_overrides                (universal)
--   retention_compliance          (universal)
--   note_cap_usage                (universal)
--   privacy_officer_attestations  (universal)
--   form_policy_history           (universal)
--   vcnz_controlled_drugs         (vet NZ)
--   vcnz_records_consent          (vet NZ)
--   ava_records                   (vet AU)
--   rcvs_ai_use_compliance        (vet UK)
--   acqsc_evidence_pack           (aged_care AU)
--   sirs_notifications_log        (aged_care AU)
--   cqc_evidence_pack             (aged_care UK)
--   gdc_records_audit             (dental UK)
--   hipaa_disclosure_log          (general US)
--
-- vertical + country are denormalised for fast filtering and pinning
-- the regulator-aligned template. They MUST match clinic.vertical /
-- clinic.country at request time (service-layer enforced).
CREATE TABLE reports (
    id                       UUID PRIMARY KEY,
    clinic_id                UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,

    type                     TEXT NOT NULL,
    vertical                 TEXT NOT NULL,
    country                  TEXT NOT NULL,

    period_start             TIMESTAMPTZ NOT NULL,
    period_end               TIMESTAMPTZ NOT NULL,

    status                   TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','done','failed')),

    file_key                 TEXT,
    file_size_bytes          BIGINT,
    file_format              TEXT NOT NULL DEFAULT 'pdf'
        CHECK (file_format IN ('pdf','zip','csv')),

    -- sha256 of file contents — embedded in the PDF footer + persisted
    -- so anyone with the file can verify it hasn't been tampered with.
    report_hash              TEXT,

    requested_by             UUID NOT NULL REFERENCES staff(id),
    requested_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at               TIMESTAMPTZ,
    completed_at             TIMESTAMPTZ,

    error_message            TEXT,

    -- For audit_pack reports: array of sub-report ids (also rows in
    -- this table). For ai-assisted reports (e.g. AI-narrated pain
    -- trend report): provider/model/prompt_hash. Generic JSONB so the
    -- shape can evolve.
    generation_metadata      JSONB,

    -- When delivered via scheduled email (see report scheduling).
    -- delivered_to_emails snapshot of recipients at send time.
    delivered_at             TIMESTAMPTZ,
    delivered_to_emails      TEXT[],

    -- External-share support: privacy officer can mint a long-lived
    -- token to share a report with a regulator / insurer / lawyer
    -- without giving them a Salvia login. external_share_token is
    -- distinct from the file_key signed URL (which is short-lived).
    -- All external shares are audit-logged + tamper-checked via report_hash.
    external_share_token         TEXT,
    external_share_expires_at    TIMESTAMPTZ,
    external_share_created_by    UUID REFERENCES staff(id),
    external_share_recipient     TEXT,

    CONSTRAINT reports_period_valid
        CHECK (period_end > period_start),
    CONSTRAINT reports_done_has_file
        CHECK (
            (status <> 'done')
            OR (file_key IS NOT NULL AND completed_at IS NOT NULL)
        )
);

CREATE INDEX reports_clinic_idx
    ON reports (clinic_id, requested_at DESC);

CREATE INDEX reports_pending_idx
    ON reports (status, requested_at)
    WHERE status IN ('queued','running');

-- Per-report audit log: separate table because reports rows themselves
-- are the job state (mutates), and audit needs append-only.
CREATE TABLE report_audit (
    id          UUID PRIMARY KEY,
    report_id   UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    clinic_id   UUID NOT NULL REFERENCES clinics(id) ON DELETE CASCADE,
    staff_id    UUID NOT NULL REFERENCES staff(id),
    action      TEXT NOT NULL
        CHECK (action IN ('generated','downloaded','shared_externally','deleted')),
    at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    details     JSONB
);

CREATE INDEX report_audit_report_idx
    ON report_audit (report_id, at DESC);

CREATE INDEX report_audit_clinic_idx
    ON report_audit (clinic_id, at DESC);

CREATE INDEX report_audit_staff_idx
    ON report_audit (staff_id, at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS report_audit;
DROP TABLE IF EXISTS reports;

-- +goose StatementEnd
