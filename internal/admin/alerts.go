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
)

// AlertKind enumerates the actionable categories the alerts endpoint
// surfaces. The UI / D2 email digest decides how to present them; the
// service is purely a sweep + classify function.
type AlertKind string

const (
	AlertOverdueRegulator AlertKind = "incident_regulator_overdue"
	AlertDrugBelowPar     AlertKind = "drug_below_par"
	AlertDrugExpiring     AlertKind = "drug_expiring_soon"
	AlertConsentExpiring  AlertKind = "consent_expiring_soon"
	AlertReconDiscrepancy AlertKind = "drug_reconciliation_discrepancy"
)

// AlertSeverity — sorts the dashboard list. Higher severity floats up.
type AlertSeverity string

const (
	AlertSevCritical AlertSeverity = "critical" // regulator deadline missed
	AlertSevWarning  AlertSeverity = "warning"  // upcoming deadline / restock
	AlertSevInfo     AlertSeverity = "info"     // FYI
)

// Alert is one actionable item the admin should look at. Designed to be
// renderable as a row (icon + title + body + target link) and groupable
// by kind on the dashboard.
//
//nolint:revive
type Alert struct {
	Kind         AlertKind     `json:"kind"`
	Severity     AlertSeverity `json:"severity"`
	Title        string        `json:"title"`
	Body         string        `json:"body"`
	TargetID     *string       `json:"target_id,omitempty"`     // resource UUID
	TargetType   string        `json:"target_type,omitempty"`   // "incident", "drug_shelf", "consent", "reconciliation"
	OccurredAt   string        `json:"occurred_at"`
	DueAt        *string       `json:"due_at,omitempty"`        // for deadline-shaped alerts
}

//nolint:revive
type AlertsResponse struct {
	Items []*Alert `json:"items"`
	Total int      `json:"total"`
}

// GetAlerts sweeps every relevant data source for actionable items.
// Pure read; no state changes. Safe to call as often as the dashboard
// refreshes.
func (s *Service) GetAlerts(ctx context.Context, clinicID, staffID uuid.UUID) (*AlertsResponse, error) {
	alerts := []*Alert{}
	now := domain.TimeNow()

	overdue, err := s.incidentRegulatorOverdueAlerts(ctx, clinicID, staffID, now)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetAlerts: incidents: %w", err)
	}
	alerts = append(alerts, overdue...)

	drugAlerts, err := s.drugShelfAlerts(ctx, clinicID, now)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetAlerts: drugs: %w", err)
	}
	alerts = append(alerts, drugAlerts...)

	consentAlerts, err := s.consentExpiringAlerts(ctx, clinicID, staffID)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetAlerts: consent: %w", err)
	}
	alerts = append(alerts, consentAlerts...)

	reconAlerts, err := s.reconDiscrepancyAlerts(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("admin.service.GetAlerts: recon: %w", err)
	}
	alerts = append(alerts, reconAlerts...)

	return &AlertsResponse{Items: alerts, Total: len(alerts)}, nil
}

// ── Per-kind sweepers ───────────────────────────────────────────────────────

func (s *Service) incidentRegulatorOverdueAlerts(ctx context.Context, clinicID, staffID uuid.UUID, now time.Time) ([]*Alert, error) {
	out := []*Alert{}
	const pageSize = 200
	offset := 0
	for {
		page, err := s.incidentsSvc.ListIncidents(ctx, clinicID, staffID, incidents.ListIncidentsParams{
			Limit:    pageSize,
			Offset:   offset,
			OnlyOpen: true,
		})
		if err != nil {
			return nil, fmt.Errorf("walk: %w", err)
		}
		for _, inc := range page.Items {
			if inc.NotificationDeadline == nil || inc.RegulatorNotifiedAt != nil {
				continue
			}
			deadline, err := time.Parse(time.RFC3339, *inc.NotificationDeadline)
			if err != nil {
				continue
			}
			severity := AlertSevWarning
			title := "Regulator deadline approaching"
			if deadline.Before(now) {
				severity = AlertSevCritical
				title = "Regulator deadline missed"
			} else if deadline.Sub(now) > 24*time.Hour {
				continue // not urgent enough to surface
			}
			body := fmt.Sprintf("%s incident, severity %s — notify regulator before %s.",
				inc.IncidentType, inc.Severity, deadline.UTC().Format(time.RFC3339))
			id := inc.ID
			due := *inc.NotificationDeadline
			out = append(out, &Alert{
				Kind:       AlertOverdueRegulator,
				Severity:   severity,
				Title:      title,
				Body:       body,
				TargetID:   &id,
				TargetType: "incident",
				OccurredAt: inc.OccurredAt,
				DueAt:      &due,
			})
		}
		if len(page.Items) < pageSize {
			break
		}
		offset += pageSize
	}
	return out, nil
}

func (s *Service) drugShelfAlerts(ctx context.Context, clinicID uuid.UUID, now time.Time) ([]*Alert, error) {
	out := []*Alert{}
	page, err := s.drugsSvc.ListShelfEntries(ctx, clinicID, drugs.ListShelfInput{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("shelf: %w", err)
	}
	expCutoff := now.AddDate(0, 0, 30)
	for _, e := range page.Items {
		if e.ParLevel != nil && e.Balance <= *e.ParLevel {
			id := e.ID
			out = append(out, &Alert{
				Kind:       AlertDrugBelowPar,
				Severity:   AlertSevWarning,
				Title:      "Drug shelf below par",
				Body:       fmt.Sprintf("%s — balance %.1f %s, par level %.1f %s.", e.Location, e.Balance, e.Unit, *e.ParLevel, e.Unit),
				TargetID:   &id,
				TargetType: "drug_shelf",
				OccurredAt: e.UpdatedAt,
			})
		}
		if e.ExpiryDate != nil {
			t, err := time.Parse("2006-01-02", *e.ExpiryDate)
			if err == nil && t.Before(expCutoff) {
				severity := AlertSevWarning
				if t.Before(now) {
					severity = AlertSevCritical
				}
				id := e.ID
				due := t.Format(time.RFC3339)
				out = append(out, &Alert{
					Kind:       AlertDrugExpiring,
					Severity:   severity,
					Title:      "Drug stock expiring",
					Body:       fmt.Sprintf("%s — expires %s.", e.Location, *e.ExpiryDate),
					TargetID:   &id,
					TargetType: "drug_shelf",
					OccurredAt: e.UpdatedAt,
					DueAt:      &due,
				})
			}
		}
	}
	return out, nil
}

func (s *Service) consentExpiringAlerts(ctx context.Context, clinicID, staffID uuid.UUID) ([]*Alert, error) {
	out := []*Alert{}
	expiringWindow := 30 * 24 * time.Hour
	page, err := s.consentSvc.ListConsents(ctx, clinicID, staffID, consent.ListConsentParams{
		Limit:          200,
		ExpiringWithin: &expiringWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("consent: %w", err)
	}
	for _, c := range page.Items {
		if c.ExpiresAt == nil {
			continue
		}
		id := c.ID
		due := *c.ExpiresAt
		out = append(out, &Alert{
			Kind:       AlertConsentExpiring,
			Severity:   AlertSevInfo,
			Title:      "Consent expiring",
			Body:       fmt.Sprintf("%s — expires %s.", c.ConsentType, due),
			TargetID:   &id,
			TargetType: "consent",
			OccurredAt: c.CapturedAt,
			DueAt:      &due,
		})
	}
	return out, nil
}

func (s *Service) reconDiscrepancyAlerts(ctx context.Context, clinicID uuid.UUID) ([]*Alert, error) {
	out := []*Alert{}
	page, err := s.drugsSvc.ListReconciliations(ctx, clinicID, drugs.ListReconciliationsInput{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("recon: %w", err)
	}
	for _, r := range page.Items {
		if r.Status != "discrepancy_logged" {
			continue
		}
		id := r.ID
		out = append(out, &Alert{
			Kind:       AlertReconDiscrepancy,
			Severity:   AlertSevWarning,
			Title:      "Reconciliation discrepancy unreported",
			Body:       fmt.Sprintf("Discrepancy of %.2f units. Privacy officer may need to escalate.", r.Discrepancy),
			TargetID:   &id,
			TargetType: "reconciliation",
			OccurredAt: r.CreatedAt,
		})
	}
	return out, nil
}
