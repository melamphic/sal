package aidrafts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/aigen"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// ── Args ─────────────────────────────────────────────────────────────────────

type ExtractAIDraftArgs struct {
	DraftID uuid.UUID `json:"draft_id"`
}

func (ExtractAIDraftArgs) Kind() string { return "extract_ai_draft" }

// ── Adapter interfaces (cross-domain, app.go wires) ─────────────────────────

// ClinicLookup resolves vertical/country/tier so the aigen prompts can
// pick the right regulator framing. Same shape as forms.AIGenClinicLookup.
type ClinicLookup interface {
	GetForAIGen(ctx context.Context, clinicID uuid.UUID) (vertical, country, tier string, err error)
}

// IncidentDrafter is the aigen.IncidentDraftService surface the worker
// uses. Decoupled so tests can substitute a fake.
type IncidentDrafter interface {
	Generate(ctx context.Context, req aigen.IncidentDraftRequest) (*aigen.IncidentDraftResult, error)
}

// ConsentDrafter — same idea for the consent flow.
type ConsentDrafter interface {
	Generate(ctx context.Context, req aigen.ConsentDraftRequest) (*aigen.ConsentDraftResult, error)
}

// ── Worker ───────────────────────────────────────────────────────────────────

type ExtractAIDraftWorker struct {
	river.WorkerDefaults[ExtractAIDraftArgs]
	repo            *Repository
	recording       RecordingProvider
	clinics         ClinicLookup
	incidentDrafter IncidentDrafter
	consentDrafter  ConsentDrafter
}

func NewExtractAIDraftWorker(repo *Repository, recording RecordingProvider, clinics ClinicLookup, incidentDrafter IncidentDrafter, consentDrafter ConsentDrafter) *ExtractAIDraftWorker {
	return &ExtractAIDraftWorker{
		repo:            repo,
		recording:       recording,
		clinics:         clinics,
		incidentDrafter: incidentDrafter,
		consentDrafter:  consentDrafter,
	}
}

func (w *ExtractAIDraftWorker) Work(ctx context.Context, job *river.Job[ExtractAIDraftArgs]) error {
	rec, err := w.repo.GetDraftInternal(ctx, job.Args.DraftID)
	if err != nil {
		return fmt.Errorf("extract_ai_draft: load: %w", err)
	}
	// Already finished — idempotent path (the listener can fire after
	// the create-time enqueue has already completed).
	if rec.Status == "done" || rec.Status == "failed" {
		return nil
	}

	if rec.RecordingID == nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, "recording_id missing")
		return nil
	}

	// If the transcript still isn't ready, snooze. The audio listener
	// fan-out also fires on transcript completion (UniqueOpts dedupes),
	// so the snooze is the backstop, not the primary trigger.
	transcript, err := w.recording.GetTranscript(ctx, *rec.RecordingID)
	if err != nil {
		return fmt.Errorf("extract_ai_draft: transcript: %w", err)
	}
	if transcript == nil || *transcript == "" {
		return &rivertype.JobSnoozeError{Duration: 3 * time.Second}
	}

	if err := w.repo.MarkExtracting(ctx, rec.ID); err != nil {
		return fmt.Errorf("extract_ai_draft: mark extracting: %w", err)
	}

	vertical, country, tier, err := w.clinics.GetForAIGen(ctx, rec.ClinicID)
	if err != nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, "clinic lookup: "+err.Error())
		return fmt.Errorf("extract_ai_draft: clinic: %w", err)
	}

	clinicCtx := aigen.ClinicContext{
		ClinicID: rec.ClinicID.String(),
		Vertical: vertical,
		Country:  country,
		Tier:     tier,
	}

	switch rec.TargetType {
	case "incident":
		return w.runIncident(ctx, rec, clinicCtx, *transcript)
	case "consent":
		return w.runConsent(ctx, rec, clinicCtx, *transcript)
	default:
		_ = w.repo.MarkFailed(ctx, rec.ID, "unsupported target_type: "+rec.TargetType)
		return nil
	}
}

func (w *ExtractAIDraftWorker) runIncident(ctx context.Context, rec *DraftRecord, clinic aigen.ClinicContext, transcript string) error {
	if w.incidentDrafter == nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, "incident drafter not configured")
		return nil
	}
	res, err := w.incidentDrafter.Generate(ctx, aigen.IncidentDraftRequest{
		Clinic:  clinic,
		StaffID: rec.RequestedBy.String(),
		Account: transcript,
	})
	if err != nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, err.Error())
		return fmt.Errorf("extract_ai_draft: incident: %w", err)
	}
	payload, err := json.Marshal(res.Draft)
	if err != nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, "marshal: "+err.Error())
		return fmt.Errorf("extract_ai_draft: marshal: %w", err)
	}
	if err := w.repo.MarkDone(ctx, MarkDoneParams{
		ID:           rec.ID,
		DraftPayload: string(payload),
		AIProvider:   res.Metadata.Provider,
		AIModel:      res.Metadata.Model,
		PromptHash:   res.Metadata.PromptHash,
	}); err != nil {
		return fmt.Errorf("extract_ai_draft: mark done: %w", err)
	}
	return nil
}

func (w *ExtractAIDraftWorker) runConsent(ctx context.Context, rec *DraftRecord, clinic aigen.ClinicContext, transcript string) error {
	if w.consentDrafter == nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, "consent drafter not configured")
		return nil
	}
	// Consent prompt needs a procedure description + consenting-party
	// audience. Pull from the context_payload the caller passed; fall
	// back to the transcript itself when context is empty (the AI then
	// has to infer the procedure from the conversation, which is
	// regulator-borderline — UI should encourage explicit context).
	type consentContext struct {
		Procedure   string `json:"procedure,omitempty"`
		ConsentType string `json:"consent_type,omitempty"`
		Audience    string `json:"audience,omitempty"`
	}
	cc := consentContext{}
	if rec.ContextPayload != nil && *rec.ContextPayload != "" {
		_ = json.Unmarshal([]byte(*rec.ContextPayload), &cc)
	}
	procedure := cc.Procedure
	if procedure == "" {
		procedure = transcript
	}
	if cc.ConsentType == "" {
		cc.ConsentType = "other"
	}
	if cc.Audience == "" {
		cc.Audience = "self"
	}

	res, err := w.consentDrafter.Generate(ctx, aigen.ConsentDraftRequest{
		Clinic:      clinic,
		StaffID:     rec.RequestedBy.String(),
		Procedure:   procedure,
		ConsentType: cc.ConsentType,
		Audience:    cc.Audience,
	})
	if err != nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, err.Error())
		return fmt.Errorf("extract_ai_draft: consent: %w", err)
	}
	payload, err := json.Marshal(res.Draft)
	if err != nil {
		_ = w.repo.MarkFailed(ctx, rec.ID, "marshal: "+err.Error())
		return fmt.Errorf("extract_ai_draft: marshal: %w", err)
	}
	if err := w.repo.MarkDone(ctx, MarkDoneParams{
		ID:           rec.ID,
		DraftPayload: string(payload),
		AIProvider:   res.Metadata.Provider,
		AIModel:      res.Metadata.Model,
		PromptHash:   res.Metadata.PromptHash,
	}); err != nil {
		return fmt.Errorf("extract_ai_draft: mark done: %w", err)
	}
	return nil
}
