package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository holds direct SQL the dashboard needs that isn't already
// exposed by another package's service. Today that's the recent-activity
// feed (UNION across notes / drug ops / incidents / consents) and a
// handful of cheap counters for the per-vertical KPI strips.
//
// All queries are scoped by clinic_id and use existing indexes. Each
// helper returns a small fixed-size result so we never page-load —
// dashboard widgets cap at 5–20 rows by design. Staff names are
// resolved in the service layer (they're encrypted at rest, so SQL
// can't surface them directly without a decryption pass).
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository wires the dashboard repo onto the shared pgx pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ActivityRow is one row in the recent-activity feed. ActorStaffID
// (when set) is the staff UUID the service needs to resolve to a
// display name through staff.Service.
type ActivityRow struct {
	Kind          string    // "note_signed" | "drug_op" | "incident_logged" | "consent_captured"
	When          time.Time
	Summary       string    // pre-formatted, no PII
	ActorStaffID  *uuid.UUID
}

// RecentActivity returns the most recent N events across notes (signed),
// drug operations, incidents, and consents — UNIONed in one round-trip.
// Each subquery is an indexed lookup over (clinic_id, created_at DESC),
// then the outer UNION sorts and limits.
//
// Limit caps at 20; callers pass 5–10 in practice. Cheap: 4 indexed
// LIMIT $2 scans + 1 sort over ≤80 rows.
func (r *Repository) RecentActivity(ctx context.Context, clinicID uuid.UUID, limit int) ([]ActivityRow, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	const q = `
SELECT kind, occurred_at, summary, actor
FROM (
  SELECT 'note_signed'::text       AS kind,
         submitted_at                AS occurred_at,
         'Note signed'::text         AS summary,
         submitted_by                AS actor
  FROM notes
  WHERE clinic_id = $1
    AND status = 'submitted'
    AND submitted_at IS NOT NULL
  ORDER BY submitted_at DESC
  LIMIT $2
) AS n
UNION ALL SELECT * FROM (
  SELECT 'drug_op'::text            AS kind,
         created_at                  AS occurred_at,
         (operation || ' · ' || quantity::text || ' ' || unit) AS summary,
         administered_by             AS actor
  FROM drug_operations_log
  WHERE clinic_id = $1
  ORDER BY created_at DESC
  LIMIT $2
) AS d
UNION ALL SELECT * FROM (
  SELECT 'incident_logged'::text    AS kind,
         created_at                  AS occurred_at,
         (incident_type || ' · ' || severity) AS summary,
         reported_by                 AS actor
  FROM incident_events
  WHERE clinic_id = $1
  ORDER BY created_at DESC
  LIMIT $2
) AS i
UNION ALL SELECT * FROM (
  SELECT 'consent_captured'::text   AS kind,
         created_at                  AS occurred_at,
         consent_type                AS summary,
         captured_by                 AS actor
  FROM consent_records
  WHERE clinic_id = $1
  ORDER BY created_at DESC
  LIMIT $2
) AS c
ORDER BY occurred_at DESC
LIMIT $2
`
	rows, err := r.db.Query(ctx, q, clinicID, limit)
	if err != nil {
		return nil, fmt.Errorf("dashboard.repo.RecentActivity: query: %w", err)
	}
	defer rows.Close()

	var out []ActivityRow
	for rows.Next() {
		var ar ActivityRow
		var actor *uuid.UUID
		if err := rows.Scan(&ar.Kind, &ar.When, &ar.Summary, &actor); err != nil {
			return nil, fmt.Errorf("dashboard.repo.RecentActivity: scan: %w", err)
		}
		ar.ActorStaffID = actor
		out = append(out, ar)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dashboard.repo.RecentActivity: rows: %w", err)
	}
	return out, nil
}

// CountSubmittedSince returns the count of notes with status='submitted'
// and submitted_at >= since for the clinic. Used for "signed today"
// KPI tile. One indexed scan over idx_notes_status (clinic_id, status).
func (r *Repository) CountSubmittedSince(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error) {
	const q = `
SELECT COUNT(*) FROM notes
WHERE clinic_id = $1
  AND status = 'submitted'
  AND submitted_at >= $2
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountSubmittedSince: %w", err)
	}
	return n, nil
}

// CountDraftsForStaff returns this staff member's open draft count.
// Per-staff scope so a busy clinic doesn't make this O(everyone).
func (r *Repository) CountDraftsForStaff(ctx context.Context, clinicID, staffID uuid.UUID) (int, error) {
	const q = `
SELECT COUNT(*) FROM notes
WHERE clinic_id = $1
  AND status = 'draft'
  AND created_by = $2
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID, staffID).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountDraftsForStaff: %w", err)
	}
	return n, nil
}

// CountActivePatients is universal — any vertical's KPI strip can show
// "active patients" or "active residents".
func (r *Repository) CountActivePatients(ctx context.Context, clinicID uuid.UUID) (int, error) {
	const q = `
SELECT COUNT(*) FROM subjects
WHERE clinic_id = $1
  AND status = 'active'
  AND archived_at IS NULL
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountActivePatients: %w", err)
	}
	return n, nil
}

// CountSubjectsSeenSince returns the count of distinct subjects with at
// least one note submitted since `since`. Drives the universal
// "patients seen this week" tile. Single GROUP BY over the (clinic,
// status, submitted_at) index.
func (r *Repository) CountSubjectsSeenSince(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error) {
	const q = `
SELECT COUNT(DISTINCT subject_id) FROM notes
WHERE clinic_id = $1
  AND status = 'submitted'
  AND submitted_at >= $2
  AND subject_id IS NOT NULL
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountSubjectsSeenSince: %w", err)
	}
	return n, nil
}

// CountDrugOpsAwaitingWitness returns controlled-drug operations
// without a witness — vet/aged-care KPI tile. Compliance-critical.
func (r *Repository) CountDrugOpsAwaitingWitness(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error) {
	const q = `
SELECT COUNT(*) FROM drug_operations_log
WHERE clinic_id = $1
  AND created_at >= $2
  AND operation IN ('administer','dispense','discard','transfer')
  AND witnessed_by IS NULL
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountDrugOpsAwaitingWitness: %w", err)
	}
	return n, nil
}

// CountOpenIncidents returns incidents not yet closed — aged-care KPI tile.
func (r *Repository) CountOpenIncidents(ctx context.Context, clinicID uuid.UUID) (int, error) {
	const q = `
SELECT COUNT(*) FROM incident_events
WHERE clinic_id = $1
  AND status != 'closed'
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountOpenIncidents: %w", err)
	}
	return n, nil
}

// DailyNoteSeries returns the count of submitted notes per day for the
// last 7 days, oldest first. Drives the hero-card sparkline. One
// indexed scan over (clinic_id, status, submitted_at) bucketed via
// generate_series so empty days come back as zero (no client-side
// gap-filling needed).
func (r *Repository) DailyNoteSeries(ctx context.Context, clinicID uuid.UUID, since time.Time) ([]int, error) {
	const q = `
WITH days AS (
  SELECT generate_series(
    date_trunc('day', $2::timestamptz),
    date_trunc('day', $2::timestamptz) + interval '6 days',
    interval '1 day'
  ) AS day
)
SELECT COALESCE(COUNT(n.id), 0)::int AS c
FROM days
LEFT JOIN notes n
  ON n.clinic_id = $1
 AND n.status = 'submitted'
 AND n.submitted_at >= days.day
 AND n.submitted_at <  days.day + interval '1 day'
GROUP BY days.day
ORDER BY days.day
`
	rows, err := r.db.Query(ctx, q, clinicID, since)
	if err != nil {
		return nil, fmt.Errorf("dashboard.repo.DailyNoteSeries: query: %w", err)
	}
	defer rows.Close()
	out := make([]int, 0, 7)
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("dashboard.repo.DailyNoteSeries: scan: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dashboard.repo.DailyNoteSeries: rows: %w", err)
	}
	return out, nil
}

// DailyIncidentSeries — same shape as DailyNoteSeries but for
// incident_events.created_at. Drives the aged-care hero sparkline.
func (r *Repository) DailyIncidentSeries(ctx context.Context, clinicID uuid.UUID, since time.Time) ([]int, error) {
	const q = `
WITH days AS (
  SELECT generate_series(
    date_trunc('day', $2::timestamptz),
    date_trunc('day', $2::timestamptz) + interval '6 days',
    interval '1 day'
  ) AS day
)
SELECT COALESCE(COUNT(i.id), 0)::int
FROM days
LEFT JOIN incident_events i
  ON i.clinic_id = $1
 AND i.created_at >= days.day
 AND i.created_at <  days.day + interval '1 day'
GROUP BY days.day
ORDER BY days.day
`
	rows, err := r.db.Query(ctx, q, clinicID, since)
	if err != nil {
		return nil, fmt.Errorf("dashboard.repo.DailyIncidentSeries: query: %w", err)
	}
	defer rows.Close()
	out := make([]int, 0, 7)
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("dashboard.repo.DailyIncidentSeries: scan: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dashboard.repo.DailyIncidentSeries: rows: %w", err)
	}
	return out, nil
}

// CountHighPainSince returns pain assessments with score >= 7 since
// `since` — aged-care + vet KPI tile.
func (r *Repository) CountHighPainSince(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error) {
	const q = `
SELECT COUNT(*) FROM pain_scores
WHERE clinic_id = $1
  AND assessed_at >= $2
  AND score >= 7
`
	var n int
	if err := r.db.QueryRow(ctx, q, clinicID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("dashboard.repo.CountHighPainSince: %w", err)
	}
	return n, nil
}
