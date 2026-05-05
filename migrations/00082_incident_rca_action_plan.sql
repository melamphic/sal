-- +goose Up
-- +goose StatementBegin

-- Structured Root Cause Analysis + Action Plan for incident reports.
-- Today incidents only have `preventive_plan_summary TEXT` — fine for
-- a one-line postmortem but not enough to render a regulator-grade
-- incident PDF (CQC Reg 18, AU SIRS, NZ HQSC SAC all expect a
-- factor-by-factor analysis + tracked actions).
--
-- Both columns NULLable; the renderer falls back to the legacy free-text
-- preventive_plan_summary when the structured fields are absent so old
-- incidents still PDF correctly.
--
-- Shape (informal — service layer validates):
--
--   rca = {
--     "method": "five_whys" | "fishbone" | "narrative",
--     "factors": [
--       { "factor": "Care plan reduced night prompts",
--         "finding": "...",
--         "contributory": "primary" | "contributory" | "no" }
--     ],
--     "root_cause": "..."
--   }
--
--   action_plan = [
--     { "action":   "Reinstate hourly night prompts on return",
--       "owner_staff_id": "uuid|null",
--       "due_date": "2026-04-30",
--       "status":   "scheduled" | "in_progress" | "done" | "cancelled" }
--   ]
ALTER TABLE incident_events
    ADD COLUMN rca         JSONB,
    ADD COLUMN action_plan JSONB;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE incident_events
    DROP COLUMN IF EXISTS action_plan,
    DROP COLUMN IF EXISTS rca;
-- +goose StatementEnd
