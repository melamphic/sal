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

// ConsentDraftService drafts the risks_discussed + alternatives_discussed
// text for a consent record. The clinician reviews and edits before
// saving — Salvia never auto-publishes consent text.
//
// Vertical-agnostic. The prompt is parameterised by the clinic's
// (vertical, country) via Regulator metadata, so a vet clinic asking for
// euthanasia consent and an aged-care home asking for sedation consent
// both use the same code path with regulator-correct framing.
type ConsentDraftService struct {
	provider Provider
	logger   *slog.Logger
}

func NewConsentDraftService(p Provider, l *slog.Logger) *ConsentDraftService {
	if l == nil {
		l = slog.Default()
	}
	return &ConsentDraftService{provider: p, logger: l}
}

// ConsentDraftRequest — caller provides clinic context + the procedure /
// scope being consented to. Aigen does not query other domains.
type ConsentDraftRequest struct {
	Clinic    ClinicContext
	StaffID   string
	Procedure string // e.g. "Surgical castration of male cat under GA"
	ConsentType string // matches consent_records.consent_type CHECK values
	Audience  string  // "self" | "owner" | "guardian" | "epoa" | "nok" — shapes tone
}

// ConsentDraftResult is the AI's draft text + AIMetadata for persistence.
type ConsentDraftResult struct {
	Draft    *GeneratedConsentDraft
	Metadata AIMetadata
}

// Generate runs the consent-draft pipeline. Output is plain prose suitable
// for direct render under "Risks discussed" / "Alternatives discussed"
// fields on the consent capture form.
func (s *ConsentDraftService) Generate(ctx context.Context, req ConsentDraftRequest) (*ConsentDraftResult, error) {
	startedAt := time.Now()
	if strings.TrimSpace(req.Procedure) == "" {
		return nil, fmt.Errorf("aigen.consent.Generate: procedure is required")
	}
	if strings.TrimSpace(req.ConsentType) == "" {
		req.ConsentType = "other"
	}
	if strings.TrimSpace(req.Audience) == "" {
		req.Audience = "self"
	}

	regulator := LookupRegulator(req.Clinic.Country, req.Clinic.Vertical)
	prompt := renderConsentPrompt(req, regulator)

	raw, err := s.provider.GenerateJSON(ctx, prompt, SchemaConsentDraft)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("aigen.consent.Generate: %w", err)
		}
		return nil, fmt.Errorf("aigen.consent.Generate: %w", err)
	}

	var draft GeneratedConsentDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return nil, fmt.Errorf("aigen.consent.Generate: parse: %w", err)
	}
	draft.RisksDiscussed = strings.TrimSpace(draft.RisksDiscussed)
	draft.AlternativesDiscussed = strings.TrimSpace(draft.AlternativesDiscussed)
	if draft.RisksDiscussed == "" || draft.AlternativesDiscussed == "" {
		return nil, fmt.Errorf("aigen.consent.Generate: empty draft from model")
	}

	hash := promptShortHash(prompt)
	s.logger.InfoContext(ctx, "aigen.consent.generated",
		"clinic_id", req.Clinic.ClinicID,
		"vertical", req.Clinic.Vertical,
		"country", req.Clinic.Country,
		"consent_type", req.ConsentType,
		"audience", req.Audience,
		"provider", s.provider.Name(),
		"model", s.provider.Model(),
		"prompt_hash", hash,
		"latency_ms", time.Since(startedAt).Milliseconds(),
	)

	return &ConsentDraftResult{
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

func renderConsentPrompt(req ConsentDraftRequest, reg Regulator) string {
	var b strings.Builder
	b.WriteString("You are drafting two paragraphs for a clinical consent record. ")
	b.WriteString("Output JSON with exactly two keys: risks_discussed and alternatives_discussed. ")
	b.WriteString("Both fields are plain prose (no Markdown), 2 to 5 sentences each, in the second person addressed to the consenting party.\n\n")

	b.WriteString(fmt.Sprintf("Clinic context: %s clinic in %s.\n", reg.Vertical, reg.Country))
	b.WriteString(fmt.Sprintf("Regulator: %s (%s).\n", reg.Name, reg.Acronym))
	if reg.PrivacyRegime != "" {
		b.WriteString(fmt.Sprintf("Privacy regime: %s.\n", reg.PrivacyRegime))
	}
	b.WriteString(fmt.Sprintf("Consent type: %s.\n", req.ConsentType))
	b.WriteString(fmt.Sprintf("Consenting party relationship: %s.\n", req.Audience))
	b.WriteString(fmt.Sprintf("Procedure / scope: %s.\n\n", req.Procedure))

	b.WriteString("Constraints:\n")
	b.WriteString("- Use plain language an educated lay reader can understand.\n")
	b.WriteString("- Risks: name the most common foreseeable risks specific to this procedure (general anaesthesia, infection, etc. when relevant); avoid catastrophising.\n")
	b.WriteString("- Alternatives: name at least two realistic alternative options including 'no treatment' if applicable.\n")
	b.WriteString("- DO NOT include guarantees, percentages, or numerical risk figures unless universally accepted in the literature.\n")
	b.WriteString("- DO NOT include legal disclaimers, signatures, or witness fields — those live elsewhere in Salvia.\n")
	b.WriteString("- This is a draft; the clinician will review and edit before signing.\n")
	return b.String()
}
