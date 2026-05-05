package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/staff"
)

// ── Cross-domain ports — implemented by adapters in app.go ───────────────

// VerticalProvider resolves a clinic's vertical so the snapshot picks
// the right KPI strip + action card.
type VerticalProvider interface {
	GetVertical(ctx context.Context, clinicID uuid.UUID) (domain.Vertical, error)
}

// SeatUsageProvider reuses staff.Service.GetAISeatUsage to surface the
// AI-seat counter at the top of the dashboard.
type SeatUsageProvider interface {
	GetAISeatUsage(ctx context.Context, clinicID uuid.UUID) (staff.AISeatUsage, error)
}

// ClinicStateProvider gives us trial / cap state for the watchcards.
// Single call shape so the adapter in app.go doesn't fan out to N
// clinic-service methods per dashboard build.
type ClinicStateProvider interface {
	LoadDashboardState(ctx context.Context, clinicID uuid.UUID) (DashboardState, error)
}

// DashboardState mirrors clinic.DashboardState (avoids importing the
// clinic package from dashboard). Only the fields the watchcards read.
type DashboardState struct {
	NoteCap            *int
	NoteCount          int
	TrialEndsAt        time.Time
	OnboardingComplete bool
}

// ── Service ──────────────────────────────────────────────────────────────

// Service builds Snapshot payloads from cross-domain reads + the
// dashboard repo, caches them per-clinic for DefaultTTL, and serves
// cache-hits without re-querying. Cache keyed on clinic_id only —
// every staff in the clinic shares one snapshot (per-staff fields like
// drafts count are computed inside but not cached separately; the
// per-staff fan-out happens at request time).
//
// All errors are wrapped so observability shows the failing leaf — the
// top-level handler maps them to 500/404 etc.
type Service struct {
	repo     *Repository
	cache    *Cache
	vert     VerticalProvider
	seats    SeatUsageProvider
	clinics  ClinicStateProvider
}

// NewService wires all the cross-domain readers + the cache. Pass the
// shared *Cache so tests can introspect / clear it.
func NewService(repo *Repository, cache *Cache, vert VerticalProvider, seats SeatUsageProvider, clinics ClinicStateProvider) *Service {
	return &Service{repo: repo, cache: cache, vert: vert, seats: seats, clinics: clinics}
}

// Invalidate is the public hook write paths call after any mutation
// that should immediately surface on the dashboard (note submitted,
// drug op logged, incident created, consent captured). Cheap — just
// drops the cache entry. Re-export of Cache.Invalidate for ergonomic
// imports (write paths only need to import the dashboard package).
func (s *Service) Invalidate(clinicID uuid.UUID) {
	s.cache.Invalidate(clinicID)
}

// SnapshotJSON returns the per-clinic snapshot as raw JSON bytes.
// Hits the in-process cache (60s TTL) on the hot path; cache miss
// triggers a single coordinated build via singleflight so concurrent
// requests don't fan out to N parallel SQL passes.
//
// The handler returns these bytes directly with Content-Type
// application/json; no per-request marshalling on cache hits.
func (s *Service) SnapshotJSON(ctx context.Context, clinicID, staffID uuid.UUID) ([]byte, error) {
	if cached, ok := s.cache.Get(clinicID); ok {
		// Cache hit — but draft count is per-staff so we patch it in.
		// Drafts query is one indexed lookup; well below cache-skip cost.
		return s.patchPerStaff(ctx, cached, clinicID, staffID)
	}
	return s.cache.Do(clinicID, func() ([]byte, error) {
		// Re-check after acquiring the singleflight slot — a sibling
		// goroutine may have populated while we waited.
		if cached, ok := s.cache.Get(clinicID); ok {
			return s.patchPerStaff(ctx, cached, clinicID, staffID)
		}
		snap, err := s.build(ctx, clinicID)
		if err != nil {
			return nil, err
		}
		// Cache the clinic-shared portion (zero out per-staff drafts
		// before serialising). DraftsCount is patched in below per
		// requesting staff.
		snap.DraftsCount = 0
		shared, err := json.Marshal(snap)
		if err != nil {
			return nil, fmt.Errorf("dashboard.service.SnapshotJSON: marshal shared: %w", err)
		}
		s.cache.Set(clinicID, shared, DefaultTTL)
		return s.patchPerStaff(ctx, shared, clinicID, staffID)
	})
}

// patchPerStaff splices the per-staff DraftsCount into the cached
// shared snapshot. Cheap: 1 indexed COUNT + 1 JSON unmarshal/marshal
// of the small Snapshot struct.
func (s *Service) patchPerStaff(ctx context.Context, shared []byte, clinicID, staffID uuid.UUID) ([]byte, error) {
	var snap Snapshot
	if err := json.Unmarshal(shared, &snap); err != nil {
		return nil, fmt.Errorf("dashboard.service.patchPerStaff: unmarshal: %w", err)
	}
	drafts, err := s.repo.CountDraftsForStaff(ctx, clinicID, staffID)
	if err != nil {
		return nil, fmt.Errorf("dashboard.service.patchPerStaff: drafts: %w", err)
	}
	snap.DraftsCount = drafts
	out, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("dashboard.service.patchPerStaff: marshal: %w", err)
	}
	return out, nil
}

// build is the cache-miss path. Resolves vertical, then fans out to
// the cross-domain readers + the dashboard repo. Errors from the
// vertical-specific KPI builder fall back to the universal strip so a
// missing pain_scores table on a vet-only deploy doesn't break the
// page.
func (s *Service) build(ctx context.Context, clinicID uuid.UUID) (*Snapshot, error) {
	vert, err := s.vert.GetVertical(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("dashboard.service.build: vertical: %w", err)
	}

	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startOfWeek := startOfDay.AddDate(0, 0, -7)

	snap := &Snapshot{
		FetchedAt:  now,
		TTLSeconds: int(DefaultTTL.Seconds()),
		Vertical:   vert,
	}

	// Seat usage — drives the meter at the top.
	if usage, err := s.seats.GetAISeatUsage(ctx, clinicID); err == nil {
		snap.SeatUsage = SeatUsage{Used: usage.Used, Cap: usage.Cap}
	}

	// Watchcards.
	snap.Watchcards = s.buildWatchcards(ctx, clinicID, snap.SeatUsage)

	// Hero metric — big number + sparkline.
	snap.Hero = s.buildHero(ctx, clinicID, vert, startOfDay)

	// KPI strip — vertical-aware.
	snap.KPIStrip = s.buildKPIStrip(ctx, clinicID, vert, startOfDay, startOfWeek)

	// Vertical action card.
	snap.VerticalCard = s.buildVerticalCard(ctx, clinicID, vert, startOfDay)

	// Recent activity — universal.
	if rows, err := s.repo.RecentActivity(ctx, clinicID, 10); err == nil {
		snap.Activity = make([]ActivityEvent, 0, len(rows))
		for _, r := range rows {
			snap.Activity = append(snap.Activity, ActivityEvent{
				Kind:    r.Kind,
				When:    r.When,
				Summary: r.Summary,
				Tone:    activityTone(r.Kind),
			})
		}
		// Already sorted desc by SQL but defensive in case of UNION quirks.
		sort.SliceStable(snap.Activity, func(i, j int) bool {
			return snap.Activity[i].When.After(snap.Activity[j].When)
		})
	}

	return snap, nil
}

// buildWatchcards composes the universal warning cards. One clinic
// row read covers the cap / trial / onboarding inputs; AI seat usage
// is already in hand.
func (s *Service) buildWatchcards(ctx context.Context, clinicID uuid.UUID, seats SeatUsage) []Watchcard {
	out := make([]Watchcard, 0, 4)

	state, err := s.clinics.LoadDashboardState(ctx, clinicID)
	if err == nil {
		// Note cap.
		if state.NoteCap != nil {
			cap := *state.NoteCap
			used := state.NoteCount
			pct := 0
			if cap > 0 {
				pct = used * 100 / cap
			}
			switch {
			case pct >= 100:
				out = append(out, Watchcard{
					Kind: "note_cap_reached", Severity: "danger",
					Title: "Monthly note cap reached",
					Body:  fmt.Sprintf("You've used %d of %d notes this month. Upgrade to keep AI documentation rolling.", used, cap),
					CTA:   "Upgrade plan", CTAHref: "/settings/billing",
				})
			case pct >= 80:
				out = append(out, Watchcard{
					Kind: "note_cap_warning", Severity: "warn",
					Title: "Approaching note cap",
					Body:  fmt.Sprintf("%d%% of this month's note allowance used (%d / %d).", pct, used, cap),
					CTA:   "Review plan", CTAHref: "/settings/billing",
				})
			}
		}

		// Trial ending.
		if !state.TrialEndsAt.IsZero() {
			days := int(time.Until(state.TrialEndsAt).Hours() / 24)
			if days >= 0 && days <= 14 {
				out = append(out, Watchcard{
					Kind: "trial_ending", Severity: severityForTrialDays(days),
					Title: trialTitle(days),
					Body:  "Pick a plan to keep your records, audit packs, and AI features.",
					CTA:   "Choose plan", CTAHref: "/settings/billing",
				})
			}
		}

		// Compliance / onboarding incomplete.
		if !state.OnboardingComplete {
			out = append(out, Watchcard{
				Kind: "compliance_incomplete", Severity: "info",
				Title: "Finish setting up your clinic",
				Body:  "A few onboarding steps remain — pick them up where you left off.",
				CTA:   "Continue setup", CTAHref: "/onboarding",
			})
		}
	}

	// AI seat full.
	if seats.Cap > 0 && seats.Used >= seats.Cap {
		out = append(out, Watchcard{
			Kind: "ai_seat_full", Severity: "warn",
			Title: "All AI seats in use",
			Body:  fmt.Sprintf("Your plan includes %d AI recording seat(s). Upgrade to add more.", seats.Cap),
			CTA:   "Upgrade plan", CTAHref: "/settings/billing",
		})
	}

	return out
}

func severityForTrialDays(d int) string {
	switch {
	case d <= 3:
		return "danger"
	case d <= 7:
		return "warn"
	default:
		return "info"
	}
}

func trialTitle(d int) string {
	switch {
	case d <= 0:
		return "Trial ends today"
	case d == 1:
		return "Trial ends tomorrow"
	default:
		return fmt.Sprintf("Trial ends in %d days", d)
	}
}

func activityTone(kind string) string {
	switch kind {
	case "incident_logged":
		return "warn"
	case "note_signed":
		return "ok"
	case "drug_op":
		return "info"
	case "consent_captured":
		return "info"
	default:
		return ""
	}
}
