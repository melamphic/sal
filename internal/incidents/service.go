package incidents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Adapter interfaces ────────────────────────────────────────────────────────

// ClinicLookup resolves vertical + country for a clinic — needed to
// drive the SIRS/CQC classifier. Same shape as drugs.ClinicLookup; an
// app.go adapter satisfies both.
type ClinicLookup interface {
	GetVerticalAndCountry(ctx context.Context, clinicID uuid.UUID) (vertical, country string, err error)
}

// SubjectAccessLogger writes a row to subject_access_log — incidents
// touch resident PII, so every read + write is audited.
type SubjectAccessLogger interface {
	LogAccess(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, action, purpose string) error
}

// ── Wire types ────────────────────────────────────────────────────────────────

// IncidentResponse is the API-safe view of an incident row + its
// computed fields (deadline countdown, classification reason).
//
//nolint:revive
type IncidentResponse struct {
	ID                       string                       `json:"id"`
	SubjectID                string                       `json:"subject_id"`
	NoteID                   *string                      `json:"note_id,omitempty"`
	IncidentType             string                       `json:"incident_type"`
	Severity                 string                       `json:"severity"`
	OccurredAt               string                       `json:"occurred_at"`
	Location                 *string                      `json:"location,omitempty"`
	BriefDescription         string                       `json:"brief_description"`
	ImmediateActions         *string                      `json:"immediate_actions,omitempty"`
	WitnessesText            *string                      `json:"witnesses_text,omitempty"`
	SubjectOutcome           *string                      `json:"subject_outcome,omitempty"`
	SIRSPriority             *string                      `json:"sirs_priority,omitempty"`
	CQCNotifiable            bool                         `json:"cqc_notifiable"`
	CQCNotificationType      *string                      `json:"cqc_notification_type,omitempty"`
	NotificationDeadline     *string                      `json:"notification_deadline,omitempty"`
	NOKNotifiedAt            *string                      `json:"nok_notified_at,omitempty"`
	GPNotifiedAt             *string                      `json:"gp_notified_at,omitempty"`
	RegulatorNotifiedAt      *string                      `json:"regulator_notified_at,omitempty"`
	RegulatorReferenceNumber *string                      `json:"regulator_reference_number,omitempty"`
	EscalationReason         *string                      `json:"escalation_reason,omitempty"`
	EscalatedAt              *string                      `json:"escalated_at,omitempty"`
	EscalatedBy              *string                      `json:"escalated_by,omitempty"`
	ReportedBy               string                       `json:"reported_by"`
	ReviewedBy               *string                      `json:"reviewed_by,omitempty"`
	ReviewedAt               *string                      `json:"reviewed_at,omitempty"`
	PreventivePlanSummary    *string                      `json:"preventive_plan_summary,omitempty"`
	CarePlanUpdatedAt        *string                      `json:"care_plan_updated_at,omitempty"`
	Status                   string                       `json:"status"`
	CreatedAt                string                       `json:"created_at"`
	UpdatedAt                string                       `json:"updated_at"`
	Witnesses                []string                     `json:"witnesses,omitempty"`
	Addendums                []*IncidentAddendumResponse  `json:"addendums,omitempty"`
}

//nolint:revive
type IncidentAddendumResponse struct {
	ID           string `json:"id"`
	AddendumText string `json:"addendum_text"`
	AddedBy      string `json:"added_by"`
	AddedAt      string `json:"added_at"`
}

//nolint:revive
type IncidentListResponse struct {
	Items  []*IncidentResponse `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

// ── Service inputs ────────────────────────────────────────────────────────────

type CreateIncidentInput struct {
	ClinicID         uuid.UUID
	StaffID          uuid.UUID
	SubjectID        uuid.UUID
	NoteID           *uuid.UUID
	NoteFieldID      *uuid.UUID
	IncidentType     string
	Severity         string
	OccurredAt       time.Time
	Location         *string
	BriefDescription string
	ImmediateActions *string
	WitnessesText    *string
	SubjectOutcome   *string
}

type UpdateIncidentInput struct {
	ID                    uuid.UUID
	ClinicID              uuid.UUID
	StaffID               uuid.UUID
	Severity              *string
	Location              *string
	BriefDescription      *string
	ImmediateActions      *string
	WitnessesText         *string
	SubjectOutcome        *string
	NOKNotifiedAt         *time.Time
	GPNotifiedAt          *time.Time
	PreventivePlanSummary *string
	CarePlanUpdatedAt     *time.Time
	Status                *string
	Reviewed              bool // when true: stamps reviewed_by/reviewed_at to caller + now
}

type EscalateInput struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Reason   string
}

type NotifyRegulatorInput struct {
	ID              uuid.UUID
	ClinicID        uuid.UUID
	StaffID         uuid.UUID
	ReferenceNumber *string
	NotifiedAt      *time.Time
}

type AddAddendumInput struct {
	IncidentID uuid.UUID
	ClinicID   uuid.UUID
	StaffID    uuid.UUID
	Text       string
}

// ── Service ──────────────────────────────────────────────────────────────────

// Service is the incidents business-logic layer. Concurrency-safe.
type Service struct {
	repo         *Repository
	clinics      ClinicLookup
	accessLogger SubjectAccessLogger
}

func NewService(r *Repository, clinics ClinicLookup, accessLogger SubjectAccessLogger) *Service {
	return &Service{repo: r, clinics: clinics, accessLogger: accessLogger}
}

// CreateIncident validates the input, runs the SIRS/CQC classifier,
// inserts the row, and audits the subject touch.
func (s *Service) CreateIncident(ctx context.Context, in CreateIncidentInput) (*IncidentResponse, error) {
	if strings.TrimSpace(in.BriefDescription) == "" {
		return nil, fmt.Errorf("incidents.service.CreateIncident: brief_description required: %w", domain.ErrValidation)
	}
	if !validIncidentType(in.IncidentType) {
		return nil, fmt.Errorf("incidents.service.CreateIncident: unknown incident_type %q: %w", in.IncidentType, domain.ErrValidation)
	}
	if !validSeverity(in.Severity) {
		return nil, fmt.Errorf("incidents.service.CreateIncident: invalid severity: %w", domain.ErrValidation)
	}
	if in.OccurredAt.IsZero() {
		return nil, fmt.Errorf("incidents.service.CreateIncident: occurred_at required: %w", domain.ErrValidation)
	}

	vertical, country, err := s.clinics.GetVerticalAndCountry(ctx, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.CreateIncident: clinic lookup: %w", err)
	}

	classification := Classify(ClassifyInput{
		Vertical:       vertical,
		Country:        country,
		IncidentType:   in.IncidentType,
		Severity:       in.Severity,
		SubjectOutcome: derefOr(in.SubjectOutcome, ""),
		OccurredAt:     in.OccurredAt,
	})

	params := CreateIncidentParams{
		ID:                   domain.NewID(),
		ClinicID:             in.ClinicID,
		SubjectID:            in.SubjectID,
		NoteID:               in.NoteID,
		NoteFieldID:          in.NoteFieldID,
		IncidentType:         in.IncidentType,
		Severity:             in.Severity,
		OccurredAt:           in.OccurredAt,
		Location:             in.Location,
		BriefDescription:     in.BriefDescription,
		ImmediateActions:     in.ImmediateActions,
		WitnessesText:        in.WitnessesText,
		SubjectOutcome:       in.SubjectOutcome,
		ReportedBy:           in.StaffID,
		CQCNotifiable:        classification.CQCNotifiable,
		NotificationDeadline: classification.NotificationDeadline,
	}
	if classification.SIRSPriority != "" {
		v := classification.SIRSPriority
		params.SIRSPriority = &v
	}
	if classification.CQCNotificationType != "" {
		v := classification.CQCNotificationType
		params.CQCNotificationType = &v
	}

	rec, err := s.repo.CreateIncident(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.CreateIncident: %w", err)
	}

	// Audit the subject access — incidents are PII reads/writes.
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, in.ClinicID, in.SubjectID, in.StaffID,
			"incident_create", "incidents.service.CreateIncident")
	}

	return s.hydrate(ctx, rec)
}

// GetIncident — single read with witnesses + addendums hydrated.
func (s *Service) GetIncident(ctx context.Context, id, clinicID, staffID uuid.UUID) (*IncidentResponse, error) {
	rec, err := s.repo.GetIncident(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.GetIncident: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, rec.SubjectID, staffID,
			"incident_view", "incidents.service.GetIncident")
	}
	return s.hydrate(ctx, rec)
}

// SummariseForNote — fetches a hydrated incident without writing a
// separate access log. The parent note render is the access event.
func (s *Service) SummariseForNote(ctx context.Context, id, clinicID uuid.UUID) (*IncidentResponse, error) {
	rec, err := s.repo.GetIncident(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.SummariseForNote: %w", err)
	}
	return s.hydrate(ctx, rec)
}

// ListIncidents — paginated list scoped to clinic. When filtered by
// subject we audit a per-subject access; broad-list reads are not
// per-subject so we don't flood the audit table.
func (s *Service) ListIncidents(ctx context.Context, clinicID, staffID uuid.UUID, p ListIncidentsParams) (*IncidentListResponse, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	recs, total, err := s.repo.ListIncidents(ctx, clinicID, p)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.ListIncidents: %w", err)
	}
	out := make([]*IncidentResponse, 0, len(recs))
	for _, r := range recs {
		// Don't hydrate witnesses/addendums on the list view — keep it cheap.
		out = append(out, recordToResponse(r, nil, nil))
	}
	if p.SubjectID != nil && s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, *p.SubjectID, staffID,
			"incident_history_view", "incidents.service.ListIncidents")
	}
	return &IncidentListResponse{
		Items:  out,
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	}, nil
}

// UpdateIncident — partial update. Status transitions to closed /
// investigating happen here; escalation has its own endpoint because
// it requires the reason field.
func (s *Service) UpdateIncident(ctx context.Context, in UpdateIncidentInput) (*IncidentResponse, error) {
	p := UpdateIncidentParams{
		ID:                    in.ID,
		ClinicID:              in.ClinicID,
		Severity:              in.Severity,
		Location:              in.Location,
		BriefDescription:      in.BriefDescription,
		ImmediateActions:      in.ImmediateActions,
		WitnessesText:         in.WitnessesText,
		SubjectOutcome:        in.SubjectOutcome,
		NOKNotifiedAt:         in.NOKNotifiedAt,
		GPNotifiedAt:          in.GPNotifiedAt,
		PreventivePlanSummary: in.PreventivePlanSummary,
		CarePlanUpdatedAt:     in.CarePlanUpdatedAt,
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return nil, fmt.Errorf("incidents.service.UpdateIncident: invalid status: %w", domain.ErrValidation)
		}
		p.Status = in.Status
	}
	if in.Reviewed {
		now := domain.TimeNow()
		p.ReviewedAt = &now
		p.ReviewedBy = &in.StaffID
	}
	rec, err := s.repo.UpdateIncident(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.UpdateIncident: %w", err)
	}
	return s.hydrate(ctx, rec)
}

// EscalateIncident flips status to escalated + records the reason.
// Required: a non-empty reason — escalation without a documented reason
// is a regulator-defensibility hole.
func (s *Service) EscalateIncident(ctx context.Context, in EscalateInput) (*IncidentResponse, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return nil, fmt.Errorf("incidents.service.EscalateIncident: reason required: %w", domain.ErrValidation)
	}
	rec, err := s.repo.EscalateIncident(ctx, EscalateParams{
		ID:         in.ID,
		ClinicID:   in.ClinicID,
		StaffID:    in.StaffID,
		Reason:     in.Reason,
		OccurredAt: domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("incidents.service.EscalateIncident: %w", err)
	}
	return s.hydrate(ctx, rec)
}

// NotifyRegulator records that the SIRS/CQC notification has been
// filed externally. Status flips to reported_to_regulator and the
// reference number is stamped for audit citations.
func (s *Service) NotifyRegulator(ctx context.Context, in NotifyRegulatorInput) (*IncidentResponse, error) {
	notifiedAt := domain.TimeNow()
	if in.NotifiedAt != nil {
		notifiedAt = *in.NotifiedAt
	}
	rec, err := s.repo.MarkRegulatorNotified(ctx, NotifyRegulatorParams{
		ID:              in.ID,
		ClinicID:        in.ClinicID,
		StaffID:         in.StaffID,
		ReferenceNumber: in.ReferenceNumber,
		NotifiedAt:      notifiedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("incidents.service.NotifyRegulator: %w", err)
	}
	return s.hydrate(ctx, rec)
}

// ── Witnesses ────────────────────────────────────────────────────────────────

func (s *Service) AddWitness(ctx context.Context, incidentID, clinicID, staffID uuid.UUID) (*IncidentResponse, error) {
	if _, err := s.repo.GetIncident(ctx, incidentID, clinicID); err != nil {
		return nil, fmt.Errorf("incidents.service.AddWitness: %w", err)
	}
	if err := s.repo.AddWitness(ctx, incidentID, staffID); err != nil {
		return nil, fmt.Errorf("incidents.service.AddWitness: %w", err)
	}
	rec, err := s.repo.GetIncident(ctx, incidentID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.AddWitness: refetch: %w", err)
	}
	return s.hydrate(ctx, rec)
}

func (s *Service) RemoveWitness(ctx context.Context, incidentID, clinicID, staffID uuid.UUID) (*IncidentResponse, error) {
	if _, err := s.repo.GetIncident(ctx, incidentID, clinicID); err != nil {
		return nil, fmt.Errorf("incidents.service.RemoveWitness: %w", err)
	}
	if err := s.repo.RemoveWitness(ctx, incidentID, staffID); err != nil {
		return nil, fmt.Errorf("incidents.service.RemoveWitness: %w", err)
	}
	rec, err := s.repo.GetIncident(ctx, incidentID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.RemoveWitness: refetch: %w", err)
	}
	return s.hydrate(ctx, rec)
}

// ── Addendums ────────────────────────────────────────────────────────────────

func (s *Service) AddAddendum(ctx context.Context, in AddAddendumInput) (*IncidentResponse, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("incidents.service.AddAddendum: text required: %w", domain.ErrValidation)
	}
	if _, err := s.repo.GetIncident(ctx, in.IncidentID, in.ClinicID); err != nil {
		return nil, fmt.Errorf("incidents.service.AddAddendum: %w", err)
	}
	if _, err := s.repo.CreateAddendum(ctx, CreateAddendumParams{
		ID:         domain.NewID(),
		IncidentID: in.IncidentID,
		ClinicID:   in.ClinicID,
		StaffID:    in.StaffID,
		Text:       in.Text,
	}); err != nil {
		return nil, fmt.Errorf("incidents.service.AddAddendum: %w", err)
	}
	rec, err := s.repo.GetIncident(ctx, in.IncidentID, in.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.AddAddendum: refetch: %w", err)
	}
	return s.hydrate(ctx, rec)
}

// UpdateReviewStatus is the entry point used by the approvals service
// when an incident's pending/approved/challenged state changes. Pure
// passthrough to the repository — kept on the service so cross-domain
// callers still go through the typed surface.
func (s *Service) UpdateReviewStatus(ctx context.Context, id, clinicID uuid.UUID, status domain.EntityReviewStatus) error {
	if err := s.repo.UpdateReviewStatus(ctx, id, clinicID, status); err != nil {
		return fmt.Errorf("incidents.service.UpdateReviewStatus: %w", err)
	}
	return nil
}

// ── Hydration + helpers ──────────────────────────────────────────────────────

// hydrate fills in witnesses + addendums for the single-incident views.
// Cheap (~2 small queries); list view skips it for performance.
func (s *Service) hydrate(ctx context.Context, rec *IncidentRecord) (*IncidentResponse, error) {
	witnesses, err := s.repo.ListWitnesses(ctx, rec.ID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.hydrate: witnesses: %w", err)
	}
	adds, err := s.repo.ListAddendums(ctx, rec.ID)
	if err != nil {
		return nil, fmt.Errorf("incidents.service.hydrate: addendums: %w", err)
	}
	witnessIDs := make([]string, len(witnesses))
	for i, w := range witnesses {
		witnessIDs[i] = w.String()
	}
	addResp := make([]*IncidentAddendumResponse, len(adds))
	for i, a := range adds {
		addResp[i] = &IncidentAddendumResponse{
			ID:           a.ID.String(),
			AddendumText: a.AddendumText,
			AddedBy:      a.AddedBy.String(),
			AddedAt:      a.AddedAt.Format(time.RFC3339),
		}
	}
	return recordToResponse(rec, witnessIDs, addResp), nil
}

func recordToResponse(r *IncidentRecord, witnesses []string, addendums []*IncidentAddendumResponse) *IncidentResponse {
	out := &IncidentResponse{
		ID:                       r.ID.String(),
		SubjectID:                r.SubjectID.String(),
		IncidentType:             r.IncidentType,
		Severity:                 r.Severity,
		OccurredAt:               r.OccurredAt.Format(time.RFC3339),
		Location:                 r.Location,
		BriefDescription:         r.BriefDescription,
		ImmediateActions:         r.ImmediateActions,
		WitnessesText:            r.WitnessesText,
		SubjectOutcome:           r.SubjectOutcome,
		SIRSPriority:             r.SIRSPriority,
		CQCNotifiable:            r.CQCNotifiable,
		CQCNotificationType:      r.CQCNotificationType,
		RegulatorReferenceNumber: r.RegulatorReferenceNumber,
		EscalationReason:         r.EscalationReason,
		PreventivePlanSummary:    r.PreventivePlanSummary,
		ReportedBy:               r.ReportedBy.String(),
		Status:                   r.Status,
		CreatedAt:                r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:                r.UpdatedAt.Format(time.RFC3339),
		Witnesses:                witnesses,
		Addendums:                addendums,
	}
	if r.NoteID != nil {
		s := r.NoteID.String()
		out.NoteID = &s
	}
	if r.NotificationDeadline != nil {
		s := r.NotificationDeadline.Format(time.RFC3339)
		out.NotificationDeadline = &s
	}
	if r.NOKNotifiedAt != nil {
		s := r.NOKNotifiedAt.Format(time.RFC3339)
		out.NOKNotifiedAt = &s
	}
	if r.GPNotifiedAt != nil {
		s := r.GPNotifiedAt.Format(time.RFC3339)
		out.GPNotifiedAt = &s
	}
	if r.RegulatorNotifiedAt != nil {
		s := r.RegulatorNotifiedAt.Format(time.RFC3339)
		out.RegulatorNotifiedAt = &s
	}
	if r.EscalatedAt != nil {
		s := r.EscalatedAt.Format(time.RFC3339)
		out.EscalatedAt = &s
	}
	if r.EscalatedBy != nil {
		s := r.EscalatedBy.String()
		out.EscalatedBy = &s
	}
	if r.ReviewedAt != nil {
		s := r.ReviewedAt.Format(time.RFC3339)
		out.ReviewedAt = &s
	}
	if r.ReviewedBy != nil {
		s := r.ReviewedBy.String()
		out.ReviewedBy = &s
	}
	if r.CarePlanUpdatedAt != nil {
		s := r.CarePlanUpdatedAt.Format(time.RFC3339)
		out.CarePlanUpdatedAt = &s
	}
	return out
}

// ── Validators ───────────────────────────────────────────────────────────────

func validIncidentType(t string) bool {
	switch t {
	case "fall", "medication_error", "restraint", "behaviour",
		"skin_injury", "unexplained_injury", "pressure_injury",
		"unauthorised_absence", "death", "complaint",
		"sexual_misconduct", "neglect", "psychological_abuse",
		"physical_abuse", "financial_abuse", "other":
		return true
	}
	return false
}

func validSeverity(s string) bool {
	switch s {
	case "low", "medium", "high", "critical":
		return true
	}
	return false
}

func validStatus(s string) bool {
	switch s {
	case "open", "investigating", "closed", "escalated", "reported_to_regulator":
		return true
	}
	return false
}

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}
