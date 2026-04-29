# Admin Dashboard & Alerts

Two endpoints power the admin home: a periodic dashboard snapshot of
the last 30 days, and a live alerts list of items that need attention
right now. Both are aggregations — no persistent state of their own —
and both call sister-domain Services exclusively (no cross-domain
table queries).

Permission for both: `manage_staff ∪ manage_billing` — admin-grade.
Non-admin staff don't see at-a-glance regulator-overdue counts.

---

## Dashboard

```
GET /api/v1/admin/dashboard
```

Response shape (one card per top-level field):

```json
{
  "period_start": "2026-03-30T00:00:00Z",
  "period_end":   "2026-04-29T00:00:00Z",
  "subjects":  { "total": 142, "active": 138 },
  "drugs":     { "shelf_total": 24, "below_par": 2, "expiring_in_30d": 1, "recons_this_month": 3, "recons_with_discrepancy": 0 },
  "incidents": { "open_total": 4, "overdue_deadline": 1, "sirs_priority_1": 0, "sirs_priority_2": 2, "cqc_notifiable": 0 },
  "consent":   { "captured_in_period": 18, "expiring_in_30d": 3, "withdrawn_in_period": 1 },
  "pain":      { "assessments_in_period": 86, "avg_score": 2.4, "peak_score": 7 }
}
```

Per-card aggregations call the sister Services' list endpoints and
walk pages where needed. Some short-circuit on `Total` from the list
response; some fold accumulator math (avg pain score, recon
discrepancy count). The whole call sits at ~5–10 page reads on a
typical 30-day window.

The same payload shape ships across all 16 (vertical × country)
combos. The UI hides cards that don't apply via empty-state
rendering — e.g. a vet clinic that doesn't capture pain still sees
the pain card with "no assessments" rather than a missing slot.

---

## Alerts

```
GET /api/v1/admin/alerts
```

Returns a flat list of actionable items (not a per-card aggregate):

| `kind` | Severity | Trigger |
|---|---|---|
| `incident_regulator_overdue` | `critical` if past, `warning` if within 24h | open incident, deadline ≤ 24h, not yet notified |
| `drug_below_par` | `warning` | shelf entry balance ≤ par level |
| `drug_expiring_soon` | `critical` if past, `warning` if within 30d | shelf entry expiring within 30 days |
| `consent_expiring_soon` | `info` | consent with `expires_at` within 30 days, not withdrawn |
| `drug_reconciliation_discrepancy` | `warning` | recon with `status = discrepancy_logged`, not yet escalated |

Each alert carries `target_id` + `target_type` so the UI can deep-link
the user straight to the affected resource. `due_at` is set on
deadline-shaped alerts.

The same data sources back the (D2) email digest — when scheduled
report delivery turns into a daily compliance brief, the Alerts
sweeper is the data path.

---

## Architecture notes

- The admin module is a thin aggregator. No repo — all reads go
  through `patient.Service`, `drugs.Service`, `incidents.Service`,
  `consent.Service`, `pain.Service`. CLAUDE rule: "Cross-domain: call
  exported service interfaces only — never import another domain's
  types or query another domain's tables."
- Aggregations are sequential in v1. A 30-day window across the
  existing list endpoints fits comfortably in 2–3 round-trips per
  card. Future work: parallelise with `errgroup` if dashboard latency
  hits a wall.
- Adding a new card or alert kind: extend `service.go` with a new
  `xxxCard` or `xxxAlerts` method, register it in `GetDashboard` /
  `GetAlerts`, and add the response field. No new module needed.
