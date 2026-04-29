// Package admin powers the live admin dashboard. It aggregates counts +
// alerts from sister-domain services and returns a single payload the UI
// renders as cards / charts. No persistent state of its own.
//
// Cross-domain rule: every aggregation goes through public Service
// methods. The admin package never queries another domain's tables.
package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/melamphic/sal/internal/consent"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/drugs"
	"github.com/melamphic/sal/internal/incidents"
	"github.com/melamphic/sal/internal/pain"
	"github.com/melamphic/sal/internal/patient"
)

// Service is the admin-dashboard aggregator.
type Service struct {
	patientsSvc  *patient.Service
	drugsSvc     *drugs.Service
	incidentsSvc *incidents.Service
	consentSvc   *consent.Service
	painSvc      *pain.Service
}

// NewService — wire every sister service in one shot.
func NewService(
	patientsSvc *patient.Service,
	drugsSvc *drugs.Service,
	incidentsSvc *incidents.Service,
	consentSvc *consent.Service,
	painSvc *pain.Service,
) *Service {
	return &Service{
		patientsSvc:  patientsSvc,
		drugsSvc:     drugsSvc,
		incidentsSvc: incidentsSvc,
		consentSvc:   consentSvc,
		painSvc:      painSvc,
	}
}

// ── Wire types ────────────────────────────────────────────────────────────────

// DashboardResponse is the full admin overview, scoped to a single
// (clinic, period). The UI renders one card per top-level field.
//
//nolint:revive
type DashboardResponse struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`

	Subjects    *SubjectsCard    `json:"subjects"`
	Drugs       *DrugsCard       `json:"drugs"`
	Incidents   *IncidentsCard   `json:"incidents"`
	Consent     *ConsentCard     `json:"consent"`
	Pain        *PainCard        `json:"pain"`
}

//nolint:revive
type SubjectsCard struct {
	Total  int `json:"total"`
	Active int `json:"active"`
}

//nolint:revive
type DrugsCard struct {
	ShelfTotal      int `json:"shelf_total"`
	BelowPar        int `json:"below_par"`
	ExpiringIn30d   int `json:"expiring_in_30d"`
	ReconsThisMonth int `json:"recons_this_month"`
	ReconsWithDiff  int `json:"recons_with_discrepancy"`
}

//nolint:revive
type IncidentsCard struct {
	OpenTotal       int `json:"open_total"`
	OverdueDeadline int `json:"overdue_deadline"`
	SIRSPriority1   int `json:"sirs_priority_1"`
	SIRSPriority2   int `json:"sirs_priority_2"`
	CQCNotifiable   int `json:"cqc_notifiable"`
}

//nolint:revive
type ConsentCard struct {
	CapturedInPeriod int `json:"captured_in_period"`
	ExpiringIn30d    int `json:"expiring_in_30d"`
	WithdrawnInPeriod int `json:"withdrawn_in_period"`
}

//nolint:revive
type PainCard struct {
	AssessmentsInPeriod int     `json:"assessments_in_period"`
	AvgScore            float64 `json:"avg_score"`
	PeakScore           int     `json:"peak_score"`
}

// ── Aggregation ──────────────────────────────────────────────────────────────

// GetDashboard runs every per-card query and assembles the response.
// Sequential for v1 — a 30-day window across the existing list endpoints
// fits comfortably in 2-3 round-trips per card. Future work: parallelise
// with errgroup if dashboard latency becomes an issue.
func (s *Service) GetDashboard(ctx context.Context, clinicID, staffID uuid.UUID) (*DashboardResponse, error) {
	now := domain.TimeNow()
	periodStart := now.AddDate(0, 0, -30)
	periodEnd := now

	subjects, err := s.subjectsCard(ctx, clinicID, staffID)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetDashboard: subjects: %w", err)
	}
	drugsCard, err := s.drugsCard(ctx, clinicID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetDashboard: drugs: %w", err)
	}
	incidentsCard, err := s.incidentsCard(ctx, clinicID, staffID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetDashboard: incidents: %w", err)
	}
	consentCard, err := s.consentCard(ctx, clinicID, staffID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetDashboard: consent: %w", err)
	}
	painCard, err := s.painCard(ctx, clinicID, staffID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetDashboard: pain: %w", err)
	}

	return &DashboardResponse{
		PeriodStart: periodStart.Format(time.RFC3339),
		PeriodEnd:   periodEnd.Format(time.RFC3339),
		Subjects:    subjects,
		Drugs:       drugsCard,
		Incidents:   incidentsCard,
		Consent:     consentCard,
		Pain:        painCard,
	}, nil
}

// ── Per-card aggregations ────────────────────────────────────────────────────

func (s *Service) subjectsCard(ctx context.Context, clinicID, staffID uuid.UUID) (*SubjectsCard, error) {
	all, err := s.patientsSvc.ListSubjects(ctx, clinicID, patient.ListSubjectsInput{
		Limit:    1,
		ViewAll:  true,
		CallerID: staffID,
	})
	if err != nil {
		return nil, fmt.Errorf("subjects total: %w", err)
	}
	active := domain.SubjectStatusActive
	activeList, err := s.patientsSvc.ListSubjects(ctx, clinicID, patient.ListSubjectsInput{
		Limit:    1,
		Status:   &active,
		ViewAll:  true,
		CallerID: staffID,
	})
	if err != nil {
		return nil, fmt.Errorf("subjects active: %w", err)
	}
	return &SubjectsCard{Total: all.Total, Active: activeList.Total}, nil
}

func (s *Service) drugsCard(ctx context.Context, clinicID uuid.UUID, periodStart, periodEnd time.Time) (*DrugsCard, error) {
	shelf, err := s.drugsSvc.ListShelfEntries(ctx, clinicID, drugs.ListShelfInput{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("shelf list: %w", err)
	}
	belowPar, expiring := 0, 0
	for _, e := range shelf.Items {
		if e.ParLevel != nil && e.Balance <= *e.ParLevel {
			belowPar++
		}
		if e.ExpiryDate != nil {
			if t, err := time.Parse("2006-01-02", *e.ExpiryDate); err == nil {
				if t.Before(periodEnd.AddDate(0, 0, 30)) {
					expiring++
				}
			}
		}
	}

	recons, err := s.drugsSvc.ListReconciliations(ctx, clinicID, drugs.ListReconciliationsInput{
		Limit: 200,
		Since: &periodStart,
		Until: &periodEnd,
	})
	if err != nil {
		return nil, fmt.Errorf("recon list: %w", err)
	}
	withDiff := 0
	for _, r := range recons.Items {
		if r.Discrepancy != 0 || r.Status == "discrepancy_logged" || r.Status == "reported_to_regulator" {
			withDiff++
		}
	}

	return &DrugsCard{
		ShelfTotal:      shelf.Total,
		BelowPar:        belowPar,
		ExpiringIn30d:   expiring,
		ReconsThisMonth: recons.Total,
		ReconsWithDiff:  withDiff,
	}, nil
}

func (s *Service) incidentsCard(ctx context.Context, clinicID, staffID uuid.UUID, periodStart, periodEnd time.Time) (*IncidentsCard, error) {
	openOnly, err := s.incidentsSvc.ListIncidents(ctx, clinicID, staffID, incidents.ListIncidentsParams{
		Limit:    1,
		OnlyOpen: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	// Pull a deeper page (200) to count by SIRS priority + CQC notifiable +
	// overdue regulator deadlines. v1 uses one page; D3 sweeps the deep
	// queue — that's where the real pattern-alert work lives.
	deep, err := s.incidentsSvc.ListIncidents(ctx, clinicID, staffID, incidents.ListIncidentsParams{
		Limit: 200,
		Since: &periodStart,
		Until: &periodEnd,
	})
	if err != nil {
		return nil, fmt.Errorf("deep: %w", err)
	}
	now := domain.TimeNow()
	card := &IncidentsCard{OpenTotal: openOnly.Total}
	for _, inc := range deep.Items {
		if inc.SIRSPriority != nil {
			switch *inc.SIRSPriority {
			case "priority_1":
				card.SIRSPriority1++
			case "priority_2":
				card.SIRSPriority2++
			}
		}
		if inc.CQCNotifiable {
			card.CQCNotifiable++
		}
		if inc.NotificationDeadline != nil && inc.RegulatorNotifiedAt == nil {
			t, err := time.Parse(time.RFC3339, *inc.NotificationDeadline)
			if err == nil && t.Before(now) {
				card.OverdueDeadline++
			}
		}
	}
	return card, nil
}

func (s *Service) consentCard(ctx context.Context, clinicID, staffID uuid.UUID, periodStart, periodEnd time.Time) (*ConsentCard, error) {
	expiringWindow := 30 * 24 * time.Hour
	expiring, err := s.consentSvc.ListConsents(ctx, clinicID, staffID, consent.ListConsentParams{
		Limit:          1,
		ExpiringWithin: &expiringWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("expiring: %w", err)
	}

	// Captured-in-period and withdrawn-in-period both walk the page —
	// consent.Service doesn't yet have a count-only path; the page-walk
	// scales to dashboards-of-tens-of-thousands which is fine for the
	// 2026 roadmap. Add a Count* method on the service if dashboards get
	// hot.
	captured, withdrawn := 0, 0
	const pageSize = 200
	offset := 0
	for {
		page, err := s.consentSvc.ListConsents(ctx, clinicID, staffID, consent.ListConsentParams{
			Limit:  pageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("walk: %w", err)
		}
		for _, c := range page.Items {
			capturedAt, err := time.Parse(time.RFC3339, c.CapturedAt)
			if err != nil {
				continue
			}
			if capturedAt.Before(periodStart) || capturedAt.After(periodEnd) {
				continue
			}
			captured++
			if c.WithdrawalAt != nil {
				withdrawalAt, err := time.Parse(time.RFC3339, *c.WithdrawalAt)
				if err == nil && !withdrawalAt.Before(periodStart) && !withdrawalAt.After(periodEnd) {
					withdrawn++
				}
			}
		}
		if len(page.Items) < pageSize {
			break
		}
		offset += pageSize
	}

	return &ConsentCard{
		CapturedInPeriod:  captured,
		ExpiringIn30d:     expiring.Total,
		WithdrawnInPeriod: withdrawn,
	}, nil
}

func (s *Service) painCard(ctx context.Context, clinicID, staffID uuid.UUID, periodStart, periodEnd time.Time) (*PainCard, error) {
	const pageSize = 200
	offset := 0
	count, peak, totalScore := 0, 0, 0
	for {
		page, err := s.painSvc.ListPainScores(ctx, clinicID, staffID, pain.ListPainScoresInput{
			Limit:  pageSize,
			Offset: offset,
			Since:  &periodStart,
			Until:  &periodEnd,
		})
		if err != nil {
			return nil, fmt.Errorf("walk: %w", err)
		}
		for _, p := range page.Items {
			count++
			totalScore += p.Score
			if p.Score > peak {
				peak = p.Score
			}
		}
		if len(page.Items) < pageSize {
			break
		}
		offset += pageSize
	}
	avg := 0.0
	if count > 0 {
		avg = float64(totalScore) / float64(count)
	}
	return &PainCard{
		AssessmentsInPeriod: count,
		AvgScore:            avg,
		PeakScore:           peak,
	}, nil
}
