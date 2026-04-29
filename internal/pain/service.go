package pain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Adapters ─────────────────────────────────────────────────────────────────

// SubjectAccessLogger writes a row to subject_access_log.
type SubjectAccessLogger interface {
	LogAccess(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, action, purpose string) error
}

// ── Default scale recommendation ────────────────────────────────────────────
//
// Same vertical-agnostic registry pattern as drugs / incidents. The
// recommendation is a hint — the clinician picks the scale; we pre-fill.

// RecommendedScale returns the most common pain scale for a (vertical,
// country) combo. Universal NRS is the global fallback.
func RecommendedScale(vertical, country string) string {
	switch vertical {
	case "aged_care":
		// Cognitive impairment is common; PainAD is the standard.
		return "painad"
	case "vet", "veterinary":
		return "nrs"
	case "dental":
		return "vas"
	case "general", "general_clinic":
		return "nrs"
	}
	_ = country // reserved for future per-country deviations
	return "nrs"
}

// ── Wire types ────────────────────────────────────────────────────────────────

//nolint:revive
type PainScoreResponse struct {
	ID            string  `json:"id"`
	SubjectID     string  `json:"subject_id"`
	NoteID        *string `json:"note_id,omitempty"`
	Score         int     `json:"score"`
	Note          *string `json:"note,omitempty"`
	Method        string  `json:"method"`
	PainScaleUsed string  `json:"pain_scale_used"`
	AssessedBy    string  `json:"assessed_by"`
	AssessedAt    string  `json:"assessed_at"`
	CreatedAt     string  `json:"created_at"`
}

//nolint:revive
type PainScoreListResponse struct {
	Items  []*PainScoreResponse `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

// SubjectTrendResponse — compact summary for the patient hub.
//
//nolint:revive
type SubjectTrendResponse struct {
	SubjectID    string   `json:"subject_id"`
	Count        int      `json:"count"`
	AvgScore     float64  `json:"avg_score"`
	LatestAt     *string  `json:"latest_at,omitempty"`
	LatestScore  *int     `json:"latest_score,omitempty"`
	HighestScore *int     `json:"highest_score,omitempty"`
	Since        string   `json:"since"`
	Until        string   `json:"until"`
}

// ── Service inputs ────────────────────────────────────────────────────────────

type RecordPainScoreInput struct {
	ClinicID      uuid.UUID
	StaffID       uuid.UUID
	SubjectID     uuid.UUID
	NoteID        *uuid.UUID
	Score         int
	Note          *string
	Method        string
	PainScaleUsed string
	AssessedAt    time.Time
}

type ListPainScoresInput struct {
	Limit     int
	Offset    int
	SubjectID *uuid.UUID
	Since     *time.Time
	Until     *time.Time
}

// ── Service ──────────────────────────────────────────────────────────────────

type Service struct {
	repo         *Repository
	accessLogger SubjectAccessLogger
}

func NewService(r *Repository, accessLogger SubjectAccessLogger) *Service {
	return &Service{repo: r, accessLogger: accessLogger}
}

func (s *Service) RecordPainScore(ctx context.Context, in RecordPainScoreInput) (*PainScoreResponse, error) {
	if in.Score < 0 || in.Score > 10 {
		return nil, fmt.Errorf("pain.service.RecordPainScore: score must be 0-10: %w", domain.ErrValidation)
	}
	if !validMethod(in.Method) {
		return nil, fmt.Errorf("pain.service.RecordPainScore: invalid method: %w", domain.ErrValidation)
	}
	if !validScale(in.PainScaleUsed) {
		return nil, fmt.Errorf("pain.service.RecordPainScore: invalid pain_scale_used: %w", domain.ErrValidation)
	}
	if in.Note != nil && len(*in.Note) > 1000 {
		return nil, fmt.Errorf("pain.service.RecordPainScore: note too long: %w", domain.ErrValidation)
	}
	if in.AssessedAt.IsZero() {
		in.AssessedAt = domain.TimeNow()
	}
	if in.Note != nil {
		t := strings.TrimSpace(*in.Note)
		in.Note = &t
	}

	rec, err := s.repo.CreatePainScore(ctx, CreatePainScoreParams{
		ID:            domain.NewID(),
		ClinicID:      in.ClinicID,
		SubjectID:     in.SubjectID,
		NoteID:        in.NoteID,
		Score:         in.Score,
		Note:          in.Note,
		Method:        in.Method,
		PainScaleUsed: in.PainScaleUsed,
		AssessedBy:    in.StaffID,
		AssessedAt:    in.AssessedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("pain.service.RecordPainScore: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, in.ClinicID, in.SubjectID, in.StaffID,
			"pain_score_record", "pain.service.RecordPainScore")
	}
	return recordToResponse(rec), nil
}

func (s *Service) GetPainScore(ctx context.Context, id, clinicID, staffID uuid.UUID) (*PainScoreResponse, error) {
	rec, err := s.repo.GetPainScore(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("pain.service.GetPainScore: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, rec.SubjectID, staffID,
			"pain_score_view", "pain.service.GetPainScore")
	}
	return recordToResponse(rec), nil
}

func (s *Service) ListPainScores(ctx context.Context, clinicID, staffID uuid.UUID, in ListPainScoresInput) (*PainScoreListResponse, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	recs, total, err := s.repo.ListPainScores(ctx, clinicID, ListPainScoresParams(in))
	if err != nil {
		return nil, fmt.Errorf("pain.service.ListPainScores: %w", err)
	}
	out := make([]*PainScoreResponse, len(recs))
	for i, r := range recs {
		out[i] = recordToResponse(r)
	}
	if in.SubjectID != nil && s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, *in.SubjectID, staffID,
			"pain_history_view", "pain.service.ListPainScores")
	}
	return &PainScoreListResponse{Items: out, Total: total, Limit: in.Limit, Offset: in.Offset}, nil
}

// SubjectTrend returns aggregate stats over a window — used by the
// pre-encounter brief + the patient-hub trend chip.
func (s *Service) SubjectTrend(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, since, until time.Time) (*SubjectTrendResponse, error) {
	if !until.After(since) {
		return nil, fmt.Errorf("pain.service.SubjectTrend: until must be after since: %w", domain.ErrValidation)
	}
	t, err := s.repo.SubjectTrend(ctx, clinicID, subjectID, since, until)
	if err != nil {
		return nil, fmt.Errorf("pain.service.SubjectTrend: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, subjectID, staffID,
			"pain_trend_view", "pain.service.SubjectTrend")
	}
	resp := &SubjectTrendResponse{
		SubjectID:    t.SubjectID.String(),
		Count:        t.Count,
		AvgScore:     t.AvgScore,
		LatestScore:  t.LatestScore,
		HighestScore: t.HighestScore,
		Since:        t.Since.Format(time.RFC3339),
		Until:        t.Until.Format(time.RFC3339),
	}
	if t.LatestAt != nil {
		s := t.LatestAt.Format(time.RFC3339)
		resp.LatestAt = &s
	}
	return resp, nil
}

// ── Validators ───────────────────────────────────────────────────────────────

func validMethod(m string) bool {
	switch m {
	case "manual", "painchek", "extracted_from_audio",
		"flacc_observed", "wong_baker":
		return true
	}
	return false
}

func validScale(s string) bool {
	switch s {
	case "nrs", "flacc", "painad", "wong_baker", "vrs", "vas":
		return true
	}
	return false
}

// ── Converter ────────────────────────────────────────────────────────────────

func recordToResponse(r *PainScoreRecord) *PainScoreResponse {
	out := &PainScoreResponse{
		ID:            r.ID.String(),
		SubjectID:     r.SubjectID.String(),
		Score:         r.Score,
		Note:          r.Note,
		Method:        r.Method,
		PainScaleUsed: r.PainScaleUsed,
		AssessedBy:    r.AssessedBy.String(),
		AssessedAt:    r.AssessedAt.Format(time.RFC3339),
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
	}
	if r.NoteID != nil {
		s := r.NoteID.String()
		out.NoteID = &s
	}
	return out
}
