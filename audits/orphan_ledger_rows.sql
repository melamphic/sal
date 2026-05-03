-- Orphan ledger rows audit
--
-- Lists rows in the four typed compliance ledgers that point at a note
-- which was archived (or never advanced past draft when it was deleted).
-- These existed because the legacy materialise-* HTTP endpoints wrote
-- to the ledger eagerly the moment a clinician tapped Confirm on a
-- system widget — even before the parent note was Submitted. Discarding
-- the draft afterwards left ledger rows pointing at an archived note.
--
-- The fix shipped with the move-materialisation-to-Submit change
-- prevents new orphans from being created. This script lists the
-- pre-existing ones so an operator can decide per-row whether to keep
-- them (the captures may still be clinically valid even without a
-- backing submitted note) or null out their note_id and treat them as
-- standalone direct-from-patient creates.
--
-- Usage:
--   psql "$DATABASE_URL" -f audits/orphan_ledger_rows.sql
--
-- The query is read-only. To act on the results, follow up with an
-- explicit UPDATE / DELETE — never script destructive cleanup.
--
-- Rows where note_id IS NULL are NOT orphans — they're standalone
-- direct-from-patient creates (eg. logged via the patient profile
-- action bar) and have no parent note by design.

\echo '── Drug operations linked to archived notes ──────────────────────'
SELECT d.id,
       d.note_id,
       d.subject_id,
       d.operation,
       d.quantity,
       d.unit,
       d.created_at,
       n.archived_at AS note_archived_at,
       n.status      AS note_status
FROM drug_operations_log d
JOIN notes n ON n.id = d.note_id
WHERE n.archived_at IS NOT NULL
ORDER BY d.created_at DESC;

\echo
\echo '── Incident events linked to archived notes ──────────────────────'
SELECT i.id,
       i.note_id,
       i.subject_id,
       i.incident_type,
       i.severity,
       i.created_at,
       n.archived_at AS note_archived_at,
       n.status      AS note_status
FROM incident_events i
JOIN notes n ON n.id = i.note_id
WHERE n.archived_at IS NOT NULL
ORDER BY i.created_at DESC;

\echo
\echo '── Consent records linked to archived notes ──────────────────────'
SELECT c.id,
       c.note_id,
       c.subject_id,
       c.consent_type,
       c.scope,
       c.created_at,
       n.archived_at AS note_archived_at,
       n.status      AS note_status
FROM consent_records c
JOIN notes n ON n.id = c.note_id
WHERE n.archived_at IS NOT NULL
ORDER BY c.created_at DESC;

\echo
\echo '── Pain scores linked to archived notes ──────────────────────────'
SELECT p.id,
       p.note_id,
       p.subject_id,
       p.score,
       p.pain_scale_used,
       p.created_at,
       n.archived_at AS note_archived_at,
       n.status      AS note_status
FROM pain_scores p
JOIN notes n ON n.id = p.note_id
WHERE n.archived_at IS NOT NULL
ORDER BY p.created_at DESC;

\echo
\echo '── Summary counts ────────────────────────────────────────────────'
SELECT 'drug_operations_log' AS ledger,
       COUNT(*)              AS orphan_count
FROM drug_operations_log d
JOIN notes n ON n.id = d.note_id
WHERE n.archived_at IS NOT NULL
UNION ALL
SELECT 'incident_events',
       COUNT(*)
FROM incident_events i
JOIN notes n ON n.id = i.note_id
WHERE n.archived_at IS NOT NULL
UNION ALL
SELECT 'consent_records',
       COUNT(*)
FROM consent_records c
JOIN notes n ON n.id = c.note_id
WHERE n.archived_at IS NOT NULL
UNION ALL
SELECT 'pain_scores',
       COUNT(*)
FROM pain_scores p
JOIN notes n ON n.id = p.note_id
WHERE n.archived_at IS NOT NULL;
