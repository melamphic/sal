package reports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Wire types ────────────────────────────────────────────────────────────────

//nolint:revive
type ReportScheduleResponse struct {
	ID           string   `json:"id"`
	ReportType   string   `json:"report_type"`
	Frequency    string   `json:"frequency"`
	Recipients   []string `json:"recipients"`
	Paused       bool     `json:"paused"`
	NextRunAt    string   `json:"next_run_at"`
	LastRunAt    *string  `json:"last_run_at,omitempty"`
	LastReportID *string  `json:"last_report_id,omitempty"`
	CreatedBy    string   `json:"created_by"`
	CreatedAt    string   `json:"created_at"`
}

//nolint:revive
type ReportScheduleListResponse struct {
	Items []*ReportScheduleResponse `json:"items"`
	Total int                       `json:"total"`
}

// ── Service inputs ────────────────────────────────────────────────────────────

type CreateReportScheduleInput struct {
	ClinicID   uuid.UUID
	StaffID    uuid.UUID
	ReportType string
	Frequency  string
	Recipients []string
}

type UpdateReportScheduleInput struct {
	ID         uuid.UUID
	ClinicID   uuid.UUID
	Recipients *[]string
	Paused     *bool
}

// ── Service methods ──────────────────────────────────────────────────────────

func (s *Service) CreateReportSchedule(ctx context.Context, in CreateReportScheduleInput) (*ReportScheduleResponse, error) {
	if !isSupportedComplianceType(in.ReportType) {
		return nil, fmt.Errorf("reports.service.CreateReportSchedule: unsupported type %q: %w", in.ReportType, domain.ErrValidation)
	}
	if !validFrequency(in.Frequency) {
		return nil, fmt.Errorf("reports.service.CreateReportSchedule: invalid frequency: %w", domain.ErrValidation)
	}
	clean := cleanRecipients(in.Recipients)
	if len(clean) == 0 {
		return nil, fmt.Errorf("reports.service.CreateReportSchedule: at least one recipient required: %w", domain.ErrValidation)
	}
	now := domain.TimeNow()
	rec, err := s.repo.CreateReportSchedule(ctx, CreateReportScheduleParams{
		ID:         domain.NewID(),
		ClinicID:   in.ClinicID,
		ReportType: in.ReportType,
		Frequency:  in.Frequency,
		Recipients: clean,
		NextRunAt:  nextFireFromNow(now, in.Frequency),
		CreatedBy:  in.StaffID,
	})
	if err != nil {
		return nil, fmt.Errorf("reports.service.CreateReportSchedule: %w", err)
	}
	return scheduleRecordToResponse(rec), nil
}

func (s *Service) ListReportSchedules(ctx context.Context, clinicID uuid.UUID) (*ReportScheduleListResponse, error) {
	recs, err := s.repo.ListReportSchedules(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.ListReportSchedules: %w", err)
	}
	out := make([]*ReportScheduleResponse, len(recs))
	for i, r := range recs {
		out[i] = scheduleRecordToResponse(r)
	}
	return &ReportScheduleListResponse{Items: out, Total: len(out)}, nil
}

func (s *Service) GetReportSchedule(ctx context.Context, id, clinicID uuid.UUID) (*ReportScheduleResponse, error) {
	rec, err := s.repo.GetReportSchedule(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("reports.service.GetReportSchedule: %w", err)
	}
	return scheduleRecordToResponse(rec), nil
}

func (s *Service) UpdateReportSchedule(ctx context.Context, in UpdateReportScheduleInput) (*ReportScheduleResponse, error) {
	p := UpdateReportScheduleParams{
		ID:       in.ID,
		ClinicID: in.ClinicID,
		Paused:   in.Paused,
	}
	if in.Recipients != nil {
		clean := cleanRecipients(*in.Recipients)
		if len(clean) == 0 {
			return nil, fmt.Errorf("reports.service.UpdateReportSchedule: at least one recipient required: %w", domain.ErrValidation)
		}
		p.Recipients = &clean
	}
	rec, err := s.repo.UpdateReportSchedule(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("reports.service.UpdateReportSchedule: %w", err)
	}
	return scheduleRecordToResponse(rec), nil
}

func (s *Service) DeleteReportSchedule(ctx context.Context, id, clinicID uuid.UUID) error {
	if err := s.repo.DeleteReportSchedule(ctx, id, clinicID); err != nil {
		return fmt.Errorf("reports.service.DeleteReportSchedule: %w", err)
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func validFrequency(f string) bool {
	switch f {
	case "daily", "weekly", "monthly", "quarterly":
		return true
	}
	return false
}

func cleanRecipients(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range in {
		r = strings.TrimSpace(strings.ToLower(r))
		if r == "" || !strings.Contains(r, "@") {
			continue
		}
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// nextFireFromNow returns the next run timestamp for a freshly-created
// schedule. We fire on the period boundary that falls AFTER `now` so the
// first email goes out at the end of the next period rather than
// immediately.
//
//   daily      → tomorrow 00:00 UTC
//   weekly     → next Monday 00:00 UTC
//   monthly    → 1st of next month 00:00 UTC
//   quarterly  → 1st of next quarter 00:00 UTC
func nextFireFromNow(now time.Time, freq string) time.Time {
	now = now.UTC()
	switch freq {
	case "daily":
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return t.Add(24 * time.Hour)
	case "weekly":
		// 0 = Sunday in time.Weekday — bump to next Monday.
		offset := (8 - int(now.Weekday())) % 7
		if offset == 0 {
			offset = 7
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return t.Add(time.Duration(offset) * 24 * time.Hour)
	case "monthly":
		nextMonth := now.Month() + 1
		nextYear := now.Year()
		if nextMonth > 12 {
			nextMonth = 1
			nextYear++
		}
		return time.Date(nextYear, nextMonth, 1, 0, 0, 0, 0, time.UTC)
	case "quarterly":
		// Quarter starts: Jan, Apr, Jul, Oct.
		q := (int(now.Month())-1)/3 + 1
		nextQ := q + 1
		nextYear := now.Year()
		if nextQ > 4 {
			nextQ = 1
			nextYear++
		}
		startMonth := time.Month((nextQ-1)*3 + 1)
		return time.Date(nextYear, startMonth, 1, 0, 0, 0, 0, time.UTC)
	}
	// Defensive: bump 1 day forward.
	return now.Add(24 * time.Hour)
}

// PeriodForFire — given the schedule's frequency and a fire timestamp,
// returns the (start, end) of the period the report should cover. For a
// monthly schedule firing on Jan 1, the report covers all of December.
func PeriodForFire(freq string, firedAt time.Time) (time.Time, time.Time) {
	firedAt = firedAt.UTC()
	switch freq {
	case "daily":
		dayStart := time.Date(firedAt.Year(), firedAt.Month(), firedAt.Day(), 0, 0, 0, 0, time.UTC)
		return dayStart.Add(-24 * time.Hour), dayStart.Add(-time.Second)
	case "weekly":
		periodEnd := time.Date(firedAt.Year(), firedAt.Month(), firedAt.Day(), 0, 0, 0, 0, time.UTC).Add(-time.Second)
		periodStart := periodEnd.Add(-7*24*time.Hour + time.Second)
		return periodStart, periodEnd
	case "monthly":
		monthStart := time.Date(firedAt.Year(), firedAt.Month(), 1, 0, 0, 0, 0, time.UTC)
		prevMonthStart := monthStart.AddDate(0, -1, 0)
		return prevMonthStart, monthStart.Add(-time.Second)
	case "quarterly":
		// firedAt is on a quarter boundary (1st of Jan/Apr/Jul/Oct).
		startMonth := firedAt.Month()
		quarterStart := time.Date(firedAt.Year(), startMonth, 1, 0, 0, 0, 0, time.UTC)
		prevQuarterStart := quarterStart.AddDate(0, -3, 0)
		return prevQuarterStart, quarterStart.Add(-time.Second)
	}
	return firedAt.Add(-24 * time.Hour), firedAt
}

func scheduleRecordToResponse(r *ReportScheduleRecord) *ReportScheduleResponse {
	out := &ReportScheduleResponse{
		ID:         r.ID.String(),
		ReportType: r.ReportType,
		Frequency:  r.Frequency,
		Recipients: r.Recipients,
		Paused:     r.Paused,
		NextRunAt:  r.NextRunAt.Format(time.RFC3339),
		CreatedBy:  r.CreatedBy.String(),
		CreatedAt:  r.CreatedAt.Format(time.RFC3339),
	}
	if r.LastRunAt != nil {
		s := r.LastRunAt.Format(time.RFC3339)
		out.LastRunAt = &s
	}
	if r.LastReportID != nil {
		s := r.LastReportID.String()
		out.LastReportID = &s
	}
	return out
}
