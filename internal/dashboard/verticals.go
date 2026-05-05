package dashboard

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// buildKPIStrip composes the 4-tile KPI row for the supplied vertical.
// All counters are best-effort — a single repo error renders a "—"
// tile rather than failing the whole dashboard.
//
// Tiles per vertical (proposed in earlier audit, confirmed by user):
//
//	Vet:        Encounters today · Drug ops awaiting witness · Patients seen this week · High pain alerts (24h)
//	Aged-care:  Notes signed today · Open incidents · High pain (24h) · Drug ops awaiting witness
//	Dental:     Notes signed today · Drug ops awaiting witness · Active patients · Patients seen this week
//	General:    Notes signed today · Active patients · Patients seen this week · Drug ops awaiting witness
//
// Aged-care surfaces incidents prominently (SIRS / CQC) and pain
// monitoring (PainAD). Vet surfaces witness compliance which is the
// daily operational pain point. Dental + general lean on volume +
// patient throughput — they don't generate as much vertical-specific
// telemetry yet.
func (s *Service) buildKPIStrip(
	ctx context.Context,
	clinicID uuid.UUID,
	vert domain.Vertical,
	startOfDay, startOfWeek time.Time,
) []KPI {
	signed := s.tile(
		"signed_today", "Signed today",
		s.repo.CountSubmittedSince, ctx, clinicID, startOfDay, "ok",
	)
	witness := s.tile(
		"witness_pending", "Witness pending",
		s.repo.CountDrugOpsAwaitingWitness, ctx, clinicID, startOfDay, "warn",
	)
	patientsWeek := s.tile(
		"patients_week", "Patients seen (7d)",
		s.repo.CountSubjectsSeenSince, ctx, clinicID, startOfWeek, "info",
	)
	patientsActive := s.tileNoTime(
		"patients_active", "Active patients",
		s.repo.CountActivePatients, ctx, clinicID, "",
	)
	highPain := s.tile(
		"high_pain", "High pain (24h)",
		s.repo.CountHighPainSince, ctx, clinicID, startOfDay.Add(-24*time.Hour), "danger",
	)
	openIncidents := s.tileNoTime(
		"open_incidents", "Open incidents",
		s.repo.CountOpenIncidents, ctx, clinicID, "warn",
	)

	switch vert {
	case domain.VerticalVeterinary:
		return []KPI{signed, witness, patientsWeek, highPain}
	case domain.VerticalAgedCare:
		return []KPI{signed, openIncidents, highPain, witness}
	case domain.VerticalDental:
		return []KPI{signed, witness, patientsActive, patientsWeek}
	default: // VerticalGeneralClinic
		return []KPI{signed, patientsActive, patientsWeek, witness}
	}
}

// buildHero returns the headline metric for the supplied vertical.
// 7-day sparkline + big number + delta-vs-prior-week. Aged-care
// surfaces incidents (the SIRS deadline pressure); everything else
// surfaces signed notes (the universal "is the AI documentation flow
// working" pulse).
func (s *Service) buildHero(ctx context.Context, clinicID uuid.UUID, vert domain.Vertical, today time.Time) *HeroMetric {
	weekStart := today.AddDate(0, 0, -6) // 7 days inclusive of today
	switch vert {
	case domain.VerticalAgedCare:
		series, err := s.repo.DailyIncidentSeries(ctx, clinicID, weekStart)
		if err != nil || len(series) == 0 {
			return nil
		}
		current := sumInts(series)
		prior := s.priorWeekIncidents(ctx, clinicID, today)
		delta, dir := pctDelta(current, prior)
		return &HeroMetric{
			Label:    "Incidents this week",
			Value:    current,
			Series:   series,
			DeltaPct: delta,
			DeltaDir: dir,
			SubLabel: "vs last 7 days",
			Tone:     toneForIncidents(current, dir),
		}
	default:
		series, err := s.repo.DailyNoteSeries(ctx, clinicID, weekStart)
		if err != nil || len(series) == 0 {
			return nil
		}
		current := sumInts(series)
		prior := s.priorWeekNotes(ctx, clinicID, today)
		delta, dir := pctDelta(current, prior)
		return &HeroMetric{
			Label:    "Notes signed this week",
			Value:    current,
			Series:   series,
			DeltaPct: delta,
			DeltaDir: dir,
			SubLabel: "vs last 7 days",
			Tone:     "info",
		}
	}
}

func (s *Service) priorWeekNotes(ctx context.Context, clinicID uuid.UUID, today time.Time) int {
	from := today.AddDate(0, 0, -13)
	series, err := s.repo.DailyNoteSeries(ctx, clinicID, from)
	if err != nil {
		return 0
	}
	return sumInts(series)
}

func (s *Service) priorWeekIncidents(ctx context.Context, clinicID uuid.UUID, today time.Time) int {
	from := today.AddDate(0, 0, -13)
	series, err := s.repo.DailyIncidentSeries(ctx, clinicID, from)
	if err != nil {
		return 0
	}
	return sumInts(series)
}

func sumInts(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func pctDelta(current, prior int) (float64, string) {
	if prior == 0 {
		if current > 0 {
			return 100.0, "up"
		}
		return 0, "flat"
	}
	pct := float64(current-prior) * 100 / float64(prior)
	switch {
	case pct > 0.5:
		return pct, "up"
	case pct < -0.5:
		return pct, "down"
	default:
		return 0, "flat"
	}
}

func toneForIncidents(current int, dir string) string {
	if current == 0 {
		return "ok"
	}
	if dir == "up" {
		return "warn"
	}
	return "info"
}

// buildVerticalCard returns the bigger action card under the KPI
// strip. Today: aged-care surfaces the open-incidents list (SIRS
// urgency); the other verticals surface a recent-encounters teaser.
// Vet's "today's surgical list" needs an appointments table we don't
// have yet — falls back to the universal teaser until that ships.
func (s *Service) buildVerticalCard(ctx context.Context, clinicID uuid.UUID, vert domain.Vertical, startOfDay time.Time) *VerticalCard {
	switch vert {
	case domain.VerticalAgedCare:
		// Surface open incidents so the duty nurse sees what needs
		// to escalate. Reuses RecentActivity filtered to incidents
		// — repo-level query specifically would cost a separate
		// scan; this reuses the cached UNION result. Future work:
		// dedicated query when the list grows beyond 10 items.
		rows, err := s.repo.RecentActivity(ctx, clinicID, 10)
		if err != nil {
			return nil
		}
		items := make([]VerticalItem, 0, 5)
		for _, r := range rows {
			if r.Kind != "incident_logged" {
				continue
			}
			items = append(items, VerticalItem{
				Title:    r.Summary,
				Subtitle: r.When.Format("15:04 · Mon 02 Jan"),
				Pill:     "Open",
				PillTone: "warn",
			})
			if len(items) >= 5 {
				break
			}
		}
		return &VerticalCard{
			ID:       "aged_care_open_incidents",
			Title:    "Open incidents",
			Subtitle: "Most recent — escalate or close",
			Empty:    "No open incidents — the floor is calm.",
			Items:    items,
		}
	case domain.VerticalVeterinary:
		// Until the appointments module ships, surface a "today's
		// note pipeline" view: recently-signed notes so the lead vet
		// sees what came through. Same data the activity feed shows;
		// presented as a focused list.
		rows, err := s.repo.RecentActivity(ctx, clinicID, 10)
		if err != nil {
			return nil
		}
		items := make([]VerticalItem, 0, 5)
		for _, r := range rows {
			if r.Kind != "note_signed" {
				continue
			}
			items = append(items, VerticalItem{
				Title:    r.Summary,
				Subtitle: r.When.Format("15:04 · Mon 02 Jan"),
				Pill:     "Signed",
				PillTone: "ok",
			})
			if len(items) >= 5 {
				break
			}
		}
		return &VerticalCard{
			ID:       "vet_recent_notes",
			Title:    "Today's note pipeline",
			Subtitle: "Recently signed clinical notes",
			Empty:    "No notes signed yet today.",
			Items:    items,
		}
	default:
		// Dental + general clinic — universal "recent activity" card.
		// Skipped here so the activity feed at the bottom is the
		// canonical surface; returning nil hides the slot entirely.
		return nil
	}
}

// ── tile builder helpers ─────────────────────────────────────────────────

type counterSince func(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error)
type counterAll func(ctx context.Context, clinicID uuid.UUID) (int, error)

func (s *Service) tile(id, label string, fn counterSince, ctx context.Context, clinicID uuid.UUID, since time.Time, tone string) KPI {
	v, err := fn(ctx, clinicID, since)
	if err != nil {
		return KPI{ID: id, Label: label, Value: "—"}
	}
	return numericKPI(id, label, v, tone)
}

func (s *Service) tileNoTime(id, label string, fn counterAll, ctx context.Context, clinicID uuid.UUID, tone string) KPI {
	v, err := fn(ctx, clinicID)
	if err != nil {
		return KPI{ID: id, Label: label, Value: "—"}
	}
	return numericKPI(id, label, v, tone)
}

func numericKPI(id, label string, n int, tone string) KPI {
	val := n
	t := tone
	if n == 0 && (tone == "warn" || tone == "danger") {
		// Zero is a good sign for warn/danger tiles — drop the tone
		// so the tile renders neutral rather than alarming-and-empty.
		t = ""
	}
	return KPI{
		ID:           id,
		Label:        label,
		Value:        strconv.Itoa(n),
		NumericValue: &val,
		Tone:         t,
	}
}

