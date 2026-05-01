package consent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// ── Adapter interfaces ────────────────────────────────────────────────────────

// ClinicLookup resolves the clinic's vertical + country — used to apply
// per-jurisdiction consent defaults (retention windows, capacity-assessment
// expectations). Same shape as drugs.ClinicLookup so a single app.go
// adapter satisfies both.
type ClinicLookup interface {
	GetVerticalAndCountry(ctx context.Context, clinicID uuid.UUID) (vertical, country string, err error)
}

// SubjectAccessLogger writes a row to subject_access_log — every consent
// capture / read is a PII touch.
type SubjectAccessLogger interface {
	LogAccess(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, action, purpose string) error
}

// ── Default expiry / renewal windows by (vertical, consent_type) ─────────────
//
// Regulators don't all set hard expiry windows; what they do agree on is
// that some consents are time-limited (audio recording, AI processing,
// telemedicine, photography, MHR write) while procedural consents stand
// until withdrawn or the procedure completes. Defaults below are the
// conservative minimums — clinics can extend explicitly.
//
// Renewal windows mark "review this even if technically still valid" —
// e.g. aged-care MHR consent often gets re-affirmed annually.

var defaultExpiry = map[string]time.Duration{
	"audio_recording":  365 * 24 * time.Hour,       // re-consent yearly
	"ai_processing":    365 * 24 * time.Hour,       // re-consent yearly
	"telemedicine":     365 * 24 * time.Hour,       // re-consent yearly
	"photography":      2 * 365 * 24 * time.Hour,   // 2 years
	"data_sharing":     365 * 24 * time.Hour,       // re-consent yearly
	"mhr_write":        365 * 24 * time.Hour,       // 1 year (NZ Health Records)
	"treatment_plan":   180 * 24 * time.Hour,       // 6 months — care plans evolve
	// Procedural consents (sedation, euthanasia, invasive_procedure,
	// controlled_drug_administration) intentionally have no auto-expiry —
	// they're tied to a single event.
}

// ── Wire types ────────────────────────────────────────────────────────────────

//nolint:revive
type ConsentResponse struct {
	ID                          string  `json:"id"`
	SubjectID                   string  `json:"subject_id"`
	NoteID                      *string `json:"note_id,omitempty"`
	ConsentType                 string  `json:"consent_type"`
	Scope                       string  `json:"scope"`
	ProcedureOrFormID           *string `json:"procedure_or_form_id,omitempty"`
	RisksDiscussed              *string `json:"risks_discussed,omitempty"`
	AlternativesDiscussed       *string `json:"alternatives_discussed,omitempty"`
	CapturedVia                 string  `json:"captured_via"`
	SignatureImageKey           *string `json:"signature_image_key,omitempty"`
	TranscriptRecordingID       *string `json:"transcript_recording_id,omitempty"`
	ConsentingPartyRelationship *string `json:"consenting_party_relationship,omitempty"`
	ConsentingPartyName         *string `json:"consenting_party_name,omitempty"`
	CapacityAssessmentID        *string `json:"capacity_assessment_id,omitempty"`
	CapturedBy                  string  `json:"captured_by"`
	CapturedAt                  string  `json:"captured_at"`
	WitnessID                   *string `json:"witness_id,omitempty"`
	ExpiresAt                   *string `json:"expires_at,omitempty"`
	RenewalDueAt                *string `json:"renewal_due_at,omitempty"`
	WithdrawalAt                *string `json:"withdrawal_at,omitempty"`
	WithdrawalReason            *string `json:"withdrawal_reason,omitempty"`
	AIAssistanceMetadata        *string `json:"ai_assistance_metadata,omitempty"`
	CreatedAt                   string  `json:"created_at"`
	UpdatedAt                   string  `json:"updated_at"`
	// Active is computed from withdrawal_at + expires_at — saves the
	// client having to do date math.
	Active bool `json:"active"`
}

//nolint:revive
type ConsentListResponse struct {
	Items  []*ConsentResponse `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// ── Service inputs ────────────────────────────────────────────────────────────

type CaptureConsentInput struct {
	ClinicID                    uuid.UUID
	StaffID                     uuid.UUID
	SubjectID                   uuid.UUID
	NoteID                      *uuid.UUID
	NoteFieldID                 *uuid.UUID
	ConsentType                 string
	Scope                       string
	ProcedureOrFormID           *uuid.UUID
	RisksDiscussed              *string
	AlternativesDiscussed       *string
	CapturedVia                 string
	SignatureImageKey           *string
	TranscriptRecordingID       *uuid.UUID
	ConsentingPartyRelationship *string
	ConsentingPartyName         *string
	CapacityAssessmentID        *uuid.UUID
	WitnessID                   *uuid.UUID
	CapturedAt                  time.Time
	ExpiresAt                   *time.Time
	RenewalDueAt                *time.Time
	AIAssistanceMetadata        *string
}

type UpdateConsentInput struct {
	ID                    uuid.UUID
	ClinicID              uuid.UUID
	StaffID               uuid.UUID
	RisksDiscussed        *string
	AlternativesDiscussed *string
	ExpiresAt             *time.Time
	RenewalDueAt          *time.Time
	SignatureImageKey     *string
	WitnessID             *uuid.UUID
}

type WithdrawConsentInput struct {
	ID       uuid.UUID
	ClinicID uuid.UUID
	StaffID  uuid.UUID
	Reason   string
}

// ── Service ──────────────────────────────────────────────────────────────────

type Service struct {
	repo         *Repository
	clinics      ClinicLookup
	accessLogger SubjectAccessLogger
}

func NewService(r *Repository, clinics ClinicLookup, accessLogger SubjectAccessLogger) *Service {
	return &Service{repo: r, clinics: clinics, accessLogger: accessLogger}
}

// CaptureConsent is the single capture entrypoint. Validates the input,
// applies per-(vertical, country) expiry defaults if the caller didn't
// override, audits the subject touch, and persists.
func (s *Service) CaptureConsent(ctx context.Context, in CaptureConsentInput) (*ConsentResponse, error) {
	if !validConsentType(in.ConsentType) {
		return nil, fmt.Errorf("consent.service.CaptureConsent: invalid consent_type: %w", domain.ErrValidation)
	}
	if !validCapturedVia(in.CapturedVia) {
		return nil, fmt.Errorf("consent.service.CaptureConsent: invalid captured_via: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Scope) == "" {
		return nil, fmt.Errorf("consent.service.CaptureConsent: scope required: %w", domain.ErrValidation)
	}
	if in.CapturedAt.IsZero() {
		in.CapturedAt = domain.TimeNow()
	}
	// Verbal in-clinic consent must carry a witness — DB CHECK enforces
	// this too, but we surface a clean error before hitting the row.
	if in.CapturedVia == "verbal_clinic" && in.WitnessID == nil {
		return nil, fmt.Errorf("consent.service.CaptureConsent: witness_id required for verbal_clinic consent: %w", domain.ErrValidation)
	}
	if in.ConsentingPartyRelationship != nil && !validRelationship(*in.ConsentingPartyRelationship) {
		return nil, fmt.Errorf("consent.service.CaptureConsent: invalid consenting_party_relationship: %w", domain.ErrValidation)
	}

	// Apply default expiry if the caller didn't set one and the type has a
	// recommended window. Clinics can override by passing ExpiresAt.
	if in.ExpiresAt == nil {
		if d, ok := defaultExpiry[in.ConsentType]; ok {
			t := in.CapturedAt.Add(d)
			in.ExpiresAt = &t
		}
	}

	rec, err := s.repo.CreateConsent(ctx, CreateConsentParams{
		ID:                          domain.NewID(),
		ClinicID:                    in.ClinicID,
		SubjectID:                   in.SubjectID,
		NoteID:                      in.NoteID,
		NoteFieldID:                 in.NoteFieldID,
		ConsentType:                 in.ConsentType,
		Scope:                       in.Scope,
		ProcedureOrFormID:           in.ProcedureOrFormID,
		RisksDiscussed:              in.RisksDiscussed,
		AlternativesDiscussed:       in.AlternativesDiscussed,
		CapturedVia:                 in.CapturedVia,
		SignatureImageKey:           in.SignatureImageKey,
		TranscriptRecordingID:       in.TranscriptRecordingID,
		ConsentingPartyRelationship: in.ConsentingPartyRelationship,
		ConsentingPartyName:         in.ConsentingPartyName,
		CapacityAssessmentID:        in.CapacityAssessmentID,
		CapturedBy:                  in.StaffID,
		CapturedAt:                  in.CapturedAt,
		WitnessID:                   in.WitnessID,
		ExpiresAt:                   in.ExpiresAt,
		RenewalDueAt:                in.RenewalDueAt,
		AIAssistanceMetadata:        in.AIAssistanceMetadata,
	})
	if err != nil {
		return nil, fmt.Errorf("consent.service.CaptureConsent: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, in.ClinicID, in.SubjectID, in.StaffID,
			"consent_capture", "consent.service.CaptureConsent")
	}
	return recordToResponse(rec), nil
}

func (s *Service) GetConsent(ctx context.Context, id, clinicID, staffID uuid.UUID) (*ConsentResponse, error) {
	rec, err := s.repo.GetConsent(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("consent.service.GetConsent: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, rec.SubjectID, staffID,
			"consent_view", "consent.service.GetConsent")
	}
	return recordToResponse(rec), nil
}

// SummariseForNote returns the public ConsentResponse for an id without
// writing an access log. Called when the parent note is being rendered —
// the note itself is the access event, so logging a separate
// `consent_view` would double-count and clutter the audit trail.
func (s *Service) SummariseForNote(ctx context.Context, id, clinicID uuid.UUID) (*ConsentResponse, error) {
	rec, err := s.repo.GetConsent(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("consent.service.SummariseForNote: %w", err)
	}
	return recordToResponse(rec), nil
}

func (s *Service) ListConsents(ctx context.Context, clinicID, staffID uuid.UUID, p ListConsentParams) (*ConsentListResponse, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	recs, total, err := s.repo.ListConsents(ctx, clinicID, p)
	if err != nil {
		return nil, fmt.Errorf("consent.service.ListConsents: %w", err)
	}
	out := make([]*ConsentResponse, len(recs))
	for i, r := range recs {
		out[i] = recordToResponse(r)
	}
	if p.SubjectID != nil && s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, clinicID, *p.SubjectID, staffID,
			"consent_history_view", "consent.service.ListConsents")
	}
	return &ConsentListResponse{Items: out, Total: total, Limit: p.Limit, Offset: p.Offset}, nil
}

func (s *Service) UpdateConsent(ctx context.Context, in UpdateConsentInput) (*ConsentResponse, error) {
	rec, err := s.repo.UpdateConsent(ctx, UpdateConsentParams{
		ID:                    in.ID,
		ClinicID:              in.ClinicID,
		RisksDiscussed:        in.RisksDiscussed,
		AlternativesDiscussed: in.AlternativesDiscussed,
		ExpiresAt:             in.ExpiresAt,
		RenewalDueAt:          in.RenewalDueAt,
		SignatureImageKey:     in.SignatureImageKey,
		WitnessID:             in.WitnessID,
	})
	if err != nil {
		return nil, fmt.Errorf("consent.service.UpdateConsent: %w", err)
	}
	return recordToResponse(rec), nil
}

func (s *Service) WithdrawConsent(ctx context.Context, in WithdrawConsentInput) (*ConsentResponse, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return nil, fmt.Errorf("consent.service.WithdrawConsent: reason required: %w", domain.ErrValidation)
	}
	rec, err := s.repo.WithdrawConsent(ctx, WithdrawConsentParams{
		ID:          in.ID,
		ClinicID:    in.ClinicID,
		Reason:      in.Reason,
		WithdrawnAt: domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("consent.service.WithdrawConsent: %w", err)
	}
	if s.accessLogger != nil {
		_ = s.accessLogger.LogAccess(ctx, in.ClinicID, rec.SubjectID, in.StaffID,
			"consent_withdraw", "consent.service.WithdrawConsent")
	}
	return recordToResponse(rec), nil
}

// ── Validators ───────────────────────────────────────────────────────────────

func validConsentType(t string) bool {
	switch t {
	case "audio_recording", "ai_processing", "telemedicine",
		"sedation", "euthanasia", "invasive_procedure",
		"mhr_write", "photography", "data_sharing",
		"controlled_drug_administration", "treatment_plan", "other":
		return true
	}
	return false
}

func validCapturedVia(v string) bool {
	switch v {
	case "verbal_clinic", "verbal_telehealth",
		"written_signature", "electronic_signature", "guardian":
		return true
	}
	return false
}

func validRelationship(r string) bool {
	switch r {
	case "self", "owner", "guardian", "epoa", "nok",
		"authorised_representative", "other":
		return true
	}
	return false
}

// ── Converters ───────────────────────────────────────────────────────────────

func recordToResponse(r *ConsentRecord) *ConsentResponse {
	out := &ConsentResponse{
		ID:                          r.ID.String(),
		SubjectID:                   r.SubjectID.String(),
		ConsentType:                 r.ConsentType,
		Scope:                       r.Scope,
		RisksDiscussed:              r.RisksDiscussed,
		AlternativesDiscussed:       r.AlternativesDiscussed,
		CapturedVia:                 r.CapturedVia,
		SignatureImageKey:           r.SignatureImageKey,
		ConsentingPartyRelationship: r.ConsentingPartyRelationship,
		ConsentingPartyName:         r.ConsentingPartyName,
		CapturedBy:                  r.CapturedBy.String(),
		CapturedAt:                  r.CapturedAt.Format(time.RFC3339),
		WithdrawalReason:            r.WithdrawalReason,
		AIAssistanceMetadata:        r.AIAssistanceMetadata,
		CreatedAt:                   r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:                   r.UpdatedAt.Format(time.RFC3339),
		Active:                      r.WithdrawalAt == nil && (r.ExpiresAt == nil || r.ExpiresAt.After(domain.TimeNow())),
	}
	if r.NoteID != nil {
		s := r.NoteID.String()
		out.NoteID = &s
	}
	if r.ProcedureOrFormID != nil {
		s := r.ProcedureOrFormID.String()
		out.ProcedureOrFormID = &s
	}
	if r.TranscriptRecordingID != nil {
		s := r.TranscriptRecordingID.String()
		out.TranscriptRecordingID = &s
	}
	if r.CapacityAssessmentID != nil {
		s := r.CapacityAssessmentID.String()
		out.CapacityAssessmentID = &s
	}
	if r.WitnessID != nil {
		s := r.WitnessID.String()
		out.WitnessID = &s
	}
	if r.ExpiresAt != nil {
		s := r.ExpiresAt.Format(time.RFC3339)
		out.ExpiresAt = &s
	}
	if r.RenewalDueAt != nil {
		s := r.RenewalDueAt.Format(time.RFC3339)
		out.RenewalDueAt = &s
	}
	if r.WithdrawalAt != nil {
		s := r.WithdrawalAt.Format(time.RFC3339)
		out.WithdrawalAt = &s
	}
	return out
}
