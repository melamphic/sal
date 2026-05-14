package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
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
	LoadDashboardState(ctx context.Context, clinicID uuid.UUID) (ClinicSnapshotState, error)
}

// ClinicSnapshotState mirrors clinic.ClinicSnapshotState (avoids importing the
// clinic package from dashboard). Only the fields the watchcards read.
type ClinicSnapshotState struct {
	NoteCap            *int
	NoteCount          int
	TrialEndsAt        time.Time
	OnboardingComplete bool
}

// StaffNameResolver maps a set of staff UUIDs to their display names.
// Implemented in app.go via staff.Service; optional — when nil the
// activity feed falls back to "Staff" so the page still works in tests
// that don't wire staff.
type StaffNameResolver interface {
	ResolveStaffNames(ctx context.Context, clinicID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

// SubjectNameResolver maps a set of subject UUIDs to their display
// names. Implemented in app.go via patient.Service; optional — when nil
// activity rows skip the subject suffix and still render correctly.
type SubjectNameResolver interface {
	ResolveSubjectNames(ctx context.Context, clinicID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error)
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
	repo         *Repository
	cache        *Cache
	vert         VerticalProvider
	seats        SeatUsageProvider
	clinics      ClinicStateProvider
	staffNames   StaffNameResolver  // optional
	subjectNames SubjectNameResolver // optional
}

// NewService wires all the cross-domain readers + the cache. Pass the
// shared *Cache so tests can introspect / clear it.
func NewService(repo *Repository, cache *Cache, vert VerticalProvider, seats SeatUsageProvider, clinics ClinicStateProvider) *Service {
	return &Service{repo: repo, cache: cache, vert: vert, seats: seats, clinics: clinics}
}

// SetStaffNameResolver wires the staff display-name lookup used to
// enrich the activity feed with "{actor} did X" wording. Optional —
// callers in tests that don't exercise the feed can leave it unset.
func (s *Service) SetStaffNameResolver(r StaffNameResolver) {
	s.staffNames = r
}

// SetSubjectNameResolver wires the subject display-name lookup used to
// enrich the activity feed with "...to {subject}" wording. Optional —
// when unset the subject suffix is omitted but the row still renders.
func (s *Service) SetSubjectNameResolver(r SubjectNameResolver) {
	s.subjectNames = r
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

	// Recent activity — universal. Names get layered in here so the
	// payload is "Sarah · administered 990 mL · for Buddy" instead of
	// the raw SQL summary. Lookups are batched (one staff query + one
	// subject query) so the cost is O(1) regardless of feed length.
	if rows, err := s.repo.RecentActivity(ctx, clinicID, 10); err == nil {
		actorIDs := make([]uuid.UUID, 0, len(rows))
		subjectIDs := make([]uuid.UUID, 0, len(rows))
		actorSeen := make(map[uuid.UUID]struct{})
		subjectSeen := make(map[uuid.UUID]struct{})
		for _, r := range rows {
			if r.ActorStaffID != nil {
				if _, ok := actorSeen[*r.ActorStaffID]; !ok {
					actorSeen[*r.ActorStaffID] = struct{}{}
					actorIDs = append(actorIDs, *r.ActorStaffID)
				}
			}
			if r.SubjectID != nil {
				if _, ok := subjectSeen[*r.SubjectID]; !ok {
					subjectSeen[*r.SubjectID] = struct{}{}
					subjectIDs = append(subjectIDs, *r.SubjectID)
				}
			}
		}
		actorNames := map[uuid.UUID]string{}
		subjectNames := map[uuid.UUID]string{}
		if s.staffNames != nil && len(actorIDs) > 0 {
			if m, err := s.staffNames.ResolveStaffNames(ctx, clinicID, actorIDs); err == nil {
				actorNames = m
			}
		}
		if s.subjectNames != nil && len(subjectIDs) > 0 {
			if m, err := s.subjectNames.ResolveSubjectNames(ctx, clinicID, subjectIDs); err == nil {
				subjectNames = m
			}
		}

		snap.Activity = make([]ActivityEvent, 0, len(rows))
		for _, r := range rows {
			var actorName, subjectName, subjectID string
			if r.ActorStaffID != nil {
				actorName = actorNames[*r.ActorStaffID]
			}
			if r.SubjectID != nil {
				subjectName = subjectNames[*r.SubjectID]
				subjectID = r.SubjectID.String()
			}
			snap.Activity = append(snap.Activity, ActivityEvent{
				Kind:        r.Kind,
				When:        r.When,
				Summary:     r.Summary,
				ActorName:   actorName,
				SubjectName: subjectName,
				SubjectID:   subjectID,
				Tone:        activityTone(r.Kind),
			})
		}
		// Already sorted desc by SQL but defensive in case of UNION quirks.
		sort.SliceStable(snap.Activity, func(i, j int) bool {
			return snap.Activity[i].When.After(snap.Activity[j].When)
		})
	}

	// Attention panel + compliance health share underlying counters; we
	// fan out the eight queries in parallel so wall time stays close to
	// the slowest single query rather than their sum.
	snap.Attention, snap.ComplianceHealth = s.buildAttentionAndHealth(ctx, clinicID, startOfDay, startOfWeek)

	// Billing strip — leans on already-computed seat usage + the clinic
	// state lookup (cheap, single row).
	snap.Billing = s.buildBillingStrip(ctx, clinicID, snap.SeatUsage)

	return snap, nil
}

// buildAttentionAndHealth runs the attention-panel + compliance-health
// queries in parallel. Errors are absorbed — a missing counter falls
// back to zero so a partial outage degrades gracefully (the dashboard
// hero / KPI strip / activity feed are still useful even if one
// counter went sideways).
func (s *Service) buildAttentionAndHealth(ctx context.Context, clinicID uuid.UUID, startOfDay, startOfWeek time.Time) (*AttentionPanel, *ComplianceHealth) {
	type result struct {
		pendingApprovals  int
		oldestApprovalAge time.Duration
		draftsStale       int
		incidentsOverdue  int
		staffNoRegID      int
		notesNoRegID      int
		recsTotal         int
		recsDirty         int
		notesThisWeek     int
	}
	var r result
	var wg sync.WaitGroup
	staleCutoff := startOfDay.Add(-24 * time.Hour) // older than ~yesterday morning

	wg.Add(9)
	go func() { defer wg.Done(); n, _ := s.repo.CountPendingApprovals(ctx, clinicID); r.pendingApprovals = n }()
	go func() { defer wg.Done(); d, _ := s.repo.OldestPendingApprovalAge(ctx, clinicID); r.oldestApprovalAge = d }()
	go func() {
		defer wg.Done()
		n, _ := s.repo.CountDraftsOlderThan(ctx, clinicID, staleCutoff)
		r.draftsStale = n
	}()
	go func() { defer wg.Done(); n, _ := s.repo.CountIncidentsOverdueNotification(ctx, clinicID); r.incidentsOverdue = n }()
	go func() { defer wg.Done(); n, _ := s.repo.CountActiveStaffMissingRegulatorID(ctx, clinicID); r.staffNoRegID = n }()
	go func() {
		defer wg.Done()
		n, _ := s.repo.CountSubmittedNotesMissingRegulatorID(ctx, clinicID, startOfWeek)
		r.notesNoRegID = n
	}()
	go func() {
		defer wg.Done()
		n, _ := s.repo.CountReconciliationsSince(ctx, clinicID, startOfDay.AddDate(0, 0, -90))
		r.recsTotal = n
	}()
	go func() {
		defer wg.Done()
		n, _ := s.repo.CountReconciliationsWithDiscrepancySince(ctx, clinicID, startOfDay.AddDate(0, 0, -90))
		r.recsDirty = n
	}()
	go func() {
		defer wg.Done()
		n, _ := s.repo.CountSubmittedSince(ctx, clinicID, startOfWeek)
		r.notesThisWeek = n
	}()
	wg.Wait()

	// ── Attention panel ────────────────────────────────────────────────
	attention := &AttentionPanel{Items: []AttentionItem{
		attentionApprovalsItem(r.pendingApprovals, r.oldestApprovalAge),
		attentionDraftsItem(r.draftsStale),
		attentionIncidentsItem(r.incidentsOverdue),
		attentionMissingRegItem(r.staffNoRegID),
	}}

	// ── Compliance health row ──────────────────────────────────────────
	pctNotesWithRegID := 100.0
	if r.notesThisWeek > 0 {
		pctNotesWithRegID = 100.0 * float64(r.notesThisWeek-r.notesNoRegID) / float64(r.notesThisWeek)
	}
	pctReconsClean := 100.0
	if r.recsTotal > 0 {
		pctReconsClean = 100.0 * float64(r.recsTotal-r.recsDirty) / float64(r.recsTotal)
	}

	health := &ComplianceHealth{Metrics: []HealthMetric{
		{
			ID:        "notes.regulator_id",
			Label:     "Signed notes with regulator ID",
			Value:     fmt.Sprintf("%.0f%%", pctNotesWithRegID),
			Detail:    fmt.Sprintf("%d signed this week", r.notesThisWeek),
			ValueKind: "percent",
			Pct:       ptrFloat(pctNotesWithRegID),
			Tone:      tonePercent(pctNotesWithRegID, 95, 80),
		},
		{
			ID:        "approvals.oldest_age",
			Label:     "Oldest pending witness",
			Value:     prettyDuration(r.oldestApprovalAge),
			Detail:    fmt.Sprintf("%d in queue", r.pendingApprovals),
			ValueKind: "duration",
			Tone:      toneApprovalAge(r.oldestApprovalAge, r.pendingApprovals),
		},
		{
			ID:        "drugs.recon_clean_rate",
			Label:     "Reconciliations clean (90d)",
			Value:     fmt.Sprintf("%.0f%%", pctReconsClean),
			Detail:    fmt.Sprintf("%d total · %d with findings", r.recsTotal, r.recsDirty),
			ValueKind: "percent",
			Pct:       ptrFloat(pctReconsClean),
			Tone:      tonePercent(pctReconsClean, 95, 80),
		},
	}}

	return attention, health
}

// buildBillingStrip composes the trial / plan / seat strip. Plan label
// is derived from TrialEndsAt for v1 — Stripe-driven labels can come
// later when the billing service exposes them through ClinicSnapshotState.
func (s *Service) buildBillingStrip(ctx context.Context, clinicID uuid.UUID, seats SeatUsage) *BillingStrip {
	state, err := s.clinics.LoadDashboardState(ctx, clinicID)
	if err != nil {
		return nil
	}
	now := domain.TimeNow()
	var trialDays *int
	planLabel := "Active plan"
	tone := "ok"
	if !state.TrialEndsAt.IsZero() && state.TrialEndsAt.After(now) {
		d := int(state.TrialEndsAt.Sub(now).Hours() / 24)
		trialDays = &d
		planLabel = "Trial"
		switch {
		case d <= 3:
			tone = "danger"
		case d <= 7:
			tone = "warn"
		default:
			tone = "info"
		}
	}
	pct := 0.0
	if seats.Cap > 0 {
		pct = 100.0 * float64(seats.Used) / float64(seats.Cap)
		if pct > 100 {
			pct = 100
		}
	}
	if pct >= 100 {
		tone = "danger"
	} else if pct >= 80 && tone == "ok" {
		tone = "warn"
	}
	return &BillingStrip{
		PlanLabel:     planLabel,
		TrialDaysLeft: trialDays,
		SeatsUsed:     seats.Used,
		SeatsCap:      seats.Cap,
		SeatPct:       pct,
		Tone:          tone,
		CTAHref:       "/settings/billing",
	}
}

// ── Attention-panel item builders ────────────────────────────────────────

func attentionApprovalsItem(count int, oldest time.Duration) AttentionItem {
	if count == 0 {
		return AttentionItem{
			ID: "approvals.pending", Title: "Witness queue",
			Detail: "All clear", Count: 0, Tone: "ok",
			Icon: "users-three", CTAHref: "/witness-queue",
		}
	}
	tone := "info"
	switch {
	case oldest > 24*time.Hour:
		tone = "danger"
	case oldest > 4*time.Hour:
		tone = "warn"
	}
	return AttentionItem{
		ID: "approvals.pending", Title: "Witness queue",
		Detail: fmt.Sprintf("Oldest waiting %s", prettyDuration(oldest)),
		Count:  count, Tone: tone,
		Icon: "users-three", CTAHref: "/witness-queue",
	}
}

func attentionDraftsItem(count int) AttentionItem {
	if count == 0 {
		return AttentionItem{
			ID: "drafts.stale", Title: "Stale drafts",
			Detail: "All recent drafts moved on", Count: 0, Tone: "ok",
			Icon: "notepad",
		}
	}
	tone := "warn"
	if count >= 10 {
		tone = "danger"
	}
	return AttentionItem{
		ID: "drafts.stale", Title: "Drafts > 24h",
		Detail: "Older than yesterday — risk of forgotten encounters",
		Count:  count, Tone: tone, Icon: "notepad",
	}
}

func attentionIncidentsItem(count int) AttentionItem {
	if count == 0 {
		return AttentionItem{
			ID: "incidents.overdue", Title: "Regulator notifications",
			Detail: "All deadlines met", Count: 0, Tone: "ok", Icon: "warning",
		}
	}
	return AttentionItem{
		ID: "incidents.overdue", Title: "Incidents past notify deadline",
		Detail: "Regulator window has lapsed", Count: count,
		Tone: "danger", Icon: "warning",
	}
}

func attentionMissingRegItem(count int) AttentionItem {
	if count == 0 {
		return AttentionItem{
			ID: "staff.missing_reg_id", Title: "Regulator IDs",
			Detail: "Every clinician on file", Count: 0, Tone: "ok",
			Icon: "identification-card", CTAHref: "/settings/team",
		}
	}
	return AttentionItem{
		ID: "staff.missing_reg_id", Title: "Staff missing regulator ID",
		Detail: "Their signed PDFs ship without a registration #",
		Count:  count, Tone: "warn", Icon: "identification-card",
		CTAHref: "/settings/team",
	}
}

// ── Tone + format helpers ────────────────────────────────────────────────

func ptrFloat(v float64) *float64 { return &v }

// tonePercent maps a 0..100 percentage to a tone slug given two
// thresholds: at-or-above okBelow → ok, otherwise warnBelow → warn,
// below → danger.
func tonePercent(v float64, okBelow, warnBelow float64) string {
	switch {
	case v >= okBelow:
		return "ok"
	case v >= warnBelow:
		return "warn"
	default:
		return "danger"
	}
}

// toneApprovalAge promotes the oldest-pending duration into a tone
// for the compliance-health row. Empty queue is "ok" regardless of
// age (the duration itself is meaningless then).
func toneApprovalAge(d time.Duration, queueDepth int) string {
	if queueDepth == 0 {
		return "ok"
	}
	switch {
	case d > 24*time.Hour:
		return "danger"
	case d > 4*time.Hour:
		return "warn"
	default:
		return "info"
	}
}

func prettyDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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
