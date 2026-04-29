package aigen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// IncidentDraftService drafts a structured incident from a free-text or
// audio-transcribed account. The reporting clinician reviews + edits the
// fields before submitting; SIRS / CQC classification still happens
// server-side based on the FINAL committed values, so AI suggestions
// never bypass the regulator-decision logic.
type IncidentDraftService struct {
	provider Provider
	logger   *slog.Logger
}

func NewIncidentDraftService(p Provider, l *slog.Logger) *IncidentDraftService {
	if l == nil {
		l = slog.Default()
	}
	return &IncidentDraftService{provider: p, logger: l}
}

// IncidentDraftRequest — caller provides clinic context + the source
// account. The Account can be a hand-typed paragraph, an audio transcript,
// or anything else from which fields can be harvested.
type IncidentDraftRequest struct {
	Clinic  ClinicContext
	StaffID string
	Account string
}

// IncidentDraftResult is the AI's typed sketch + AIMetadata.
type IncidentDraftResult struct {
	Draft    *GeneratedIncidentDraft
	Metadata AIMetadata
}

// Generate runs the incident-draft pipeline.
func (s *IncidentDraftService) Generate(ctx context.Context, req IncidentDraftRequest) (*IncidentDraftResult, error) {
	startedAt := time.Now()
	if strings.TrimSpace(req.Account) == "" {
		return nil, fmt.Errorf("aigen.incident.Generate: account is required")
	}

	regulator := LookupRegulator(req.Clinic.Country, req.Clinic.Vertical)
	prompt := renderIncidentPrompt(req, regulator)

	raw, err := s.provider.GenerateJSON(ctx, prompt, SchemaIncidentDraft)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("aigen.incident.Generate: %w", err)
		}
		return nil, fmt.Errorf("aigen.incident.Generate: %w", err)
	}

	var draft GeneratedIncidentDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, fmt.Errorf("aigen.incident.Generate: parse: %w", err)
	}
	draft.IncidentType = strings.TrimSpace(draft.IncidentType)
	draft.Severity = strings.TrimSpace(draft.Severity)
	draft.BriefDescription = strings.TrimSpace(draft.BriefDescription)
	if draft.IncidentType == "" || draft.Severity == "" || draft.BriefDescription == "" {
		return nil, fmt.Errorf("aigen.incident.Generate: required fields missing in draft")
	}

	hash := promptShortHash(prompt)
	s.logger.InfoContext(ctx, "aigen.incident.generated",
		"clinic_id", req.Clinic.ClinicID,
		"vertical", req.Clinic.Vertical,
		"country", req.Clinic.Country,
		"provider", s.provider.Name(),
		"model", s.provider.Model(),
		"prompt_hash", hash,
		"latency_ms", time.Since(startedAt).Milliseconds(),
	)

	return &IncidentDraftResult{
		Draft: &draft,
		Metadata: AIMetadata{
			Source:             "ai_generated",
			Provider:           s.provider.Name(),
			Model:              s.provider.Model(),
			PromptHash:         hash,
			GeneratedByStaffID: req.StaffID,
			GeneratedAt:        startedAt,
		},
	}, nil
}

func renderIncidentPrompt(req IncidentDraftRequest, reg Regulator) string {
	var b strings.Builder
	b.WriteString("You are extracting structured fields from an incident account for a clinical incident report. ")
	b.WriteString("Output JSON with these keys: incident_type, severity, brief_description, immediate_actions, witnesses_text, subject_outcome, location.\n\n")

	b.WriteString(fmt.Sprintf("Clinic context: %s clinic in %s.\n", reg.Vertical, reg.Country))
	b.WriteString(fmt.Sprintf("Regulator: %s (%s).\n\n", reg.Name, reg.Acronym))

	b.WriteString("Allowed incident_type values (pick the closest):\n")
	b.WriteString("  fall, medication_error, restraint, behaviour, skin_injury, unexplained_injury,\n")
	b.WriteString("  pressure_injury, unauthorised_absence, death, complaint, sexual_misconduct,\n")
	b.WriteString("  neglect, psychological_abuse, physical_abuse, financial_abuse, other.\n\n")

	b.WriteString("Allowed severity values: low, medium, high, critical.\n")
	b.WriteString("Allowed subject_outcome values: no_harm, minor_injury, moderate_injury, hospitalised, deceased, complaint_resolved, unknown.\n\n")

	b.WriteString("Constraints:\n")
	b.WriteString("- Be conservative: when in doubt about severity, classify UP (a regulator-defensible report).\n")
	b.WriteString("- brief_description: 1–2 sentences, factual, third person past tense.\n")
	b.WriteString("- immediate_actions: what was done in response. Empty string if none stated.\n")
	b.WriteString("- witnesses_text: free-text names / roles of witnesses. Empty string if none stated.\n")
	b.WriteString("- subject_outcome: outcome FOR the resident / patient. Empty string if not yet determinable.\n")
	b.WriteString("- location: where the event happened (room, area). Empty string if not stated.\n")
	b.WriteString("- DO NOT add details not in the account. The reporting clinician will review and add anything missing.\n\n")

	b.WriteString("Account:\n")
	b.WriteString(req.Account)
	return b.String()
}
