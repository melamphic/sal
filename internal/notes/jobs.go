package notes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/extraction"
	"github.com/riverqueue/river"
)

// ExtractNoteArgs is the River job payload for AI form extraction.
type ExtractNoteArgs struct {
	NoteID uuid.UUID `json:"note_id"`
}

// Kind returns the unique job type string used by River.
func (ExtractNoteArgs) Kind() string { return "extract_note" }

// ── Provider interfaces ───────────────────────────────────────────────────────
// These are satisfied by adapters in app.go that bridge to other modules.

// FormFieldMeta is the subset of form_fields data needed by the extraction job.
type FormFieldMeta struct {
	ID        uuid.UUID
	Title     string
	Type      string
	AIPrompt  *string
	Required  bool
	Skippable bool
}

// FormFieldProvider fetches form field definitions for a version.
// Implemented by an adapter over forms.Repository in app.go.
type FormFieldProvider interface {
	GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]FormFieldMeta, error)
	// GetFormPrompt returns the overall_prompt for the form that owns the given version.
	// This context prompt is passed to the AI alongside per-field prompts.
	// Returns nil if no overall prompt has been configured.
	GetFormPrompt(ctx context.Context, versionID uuid.UUID) (*string, error)
}

// RecordingProvider fetches transcript data from a recording.
// Implemented by an adapter over audio.Repository in app.go.
type RecordingProvider interface {
	GetTranscript(ctx context.Context, recordingID uuid.UUID) (transcript *string, err error)
}

// ── Worker ────────────────────────────────────────────────────────────────────

// ExtractNoteWorker is the River worker that fills note fields using the AI extractor.
type ExtractNoteWorker struct {
	river.WorkerDefaults[ExtractNoteArgs]
	notes     repo
	forms     FormFieldProvider
	recording RecordingProvider
	extractor extraction.Extractor // nil = skip extraction (no API key configured)
}

// NewExtractNoteWorker constructs an ExtractNoteWorker.
func NewExtractNoteWorker(
	notes repo,
	forms FormFieldProvider,
	recording RecordingProvider,
	extractor extraction.Extractor,
) *ExtractNoteWorker {
	return &ExtractNoteWorker{
		notes:     notes,
		forms:     forms,
		recording: recording,
		extractor: extractor,
	}
}

// Work executes the extraction job.
// Steps: fetch note → fetch transcript → fetch fields → call AI → upsert results → mark draft.
func (w *ExtractNoteWorker) Work(ctx context.Context, job *river.Job[ExtractNoteArgs]) error {
	noteID := job.Args.NoteID

	// Fetch note using empty clinicID — the job trusts internal IDs.
	note, err := w.notes.GetNoteByID(ctx, noteID, uuid.Nil)
	if err != nil {
		return fmt.Errorf("extract_note: get note: %w", err)
	}

	// If extractor not configured, skip AI and go straight to draft so staff
	// can fill fields manually.
	if w.extractor == nil {
		_, err := w.notes.UpdateNoteStatus(ctx, noteID, domain.NoteStatusDraft, nil)
		if err != nil {
			return fmt.Errorf("extract_note: set draft (no extractor): %w", err)
		}
		return nil
	}

	// Fetch transcript.
	transcript, err := w.recording.GetTranscript(ctx, note.RecordingID)
	if err != nil {
		return fmt.Errorf("extract_note: get transcript: %w", err)
	}
	if transcript == nil || *transcript == "" {
		msg := "recording has no transcript — ensure transcription completed before extraction"
		_, _ = w.notes.UpdateNoteStatus(ctx, noteID, domain.NoteStatusFailed, &msg)
		return fmt.Errorf("extract_note: %s", msg)
	}

	// Fetch form-level AI context prompt and field definitions.
	overallPrompt, err := w.forms.GetFormPrompt(ctx, note.FormVersionID)
	if err != nil {
		return fmt.Errorf("extract_note: get form prompt: %w", err)
	}
	formFields, err := w.forms.GetFieldsByVersionID(ctx, note.FormVersionID)
	if err != nil {
		return fmt.Errorf("extract_note: get fields: %w", err)
	}

	// Build extraction specs — exclude skippable fields.
	specs := make([]extraction.FieldSpec, 0, len(formFields))
	skippableIDs := make(map[uuid.UUID]bool)
	for _, f := range formFields {
		if f.Skippable {
			skippableIDs[f.ID] = true
			continue
		}
		prompt := ""
		if f.AIPrompt != nil {
			prompt = *f.AIPrompt
		}
		specs = append(specs, extraction.FieldSpec{
			ID:       f.ID.String(),
			Title:    f.Title,
			Type:     f.Type,
			AIPrompt: prompt,
			Required: f.Required,
		})
	}

	// Call AI extractor.
	formPrompt := ""
	if overallPrompt != nil {
		formPrompt = *overallPrompt
	}
	var results []extraction.FieldResult
	if len(specs) > 0 {
		results, err = w.extractor.Extract(ctx, *transcript, formPrompt, specs)
		if err != nil {
			errMsg := fmt.Sprintf("extraction failed: %v", err)
			_, _ = w.notes.UpdateNoteStatus(ctx, noteID, domain.NoteStatusFailed, &errMsg)
			return fmt.Errorf("extract_note: %w", err)
		}
	}

	// Build upsert params from AI results.
	upserts := make([]UpsertFieldParams, 0, len(results)+len(skippableIDs))
	for _, r := range results {
		fieldID, err := uuid.Parse(r.FieldID)
		if err != nil {
			continue
		}
		upserts = append(upserts, UpsertFieldParams{
			ID:          domain.NewID(),
			NoteID:      noteID,
			FieldID:     fieldID,
			Value:       r.Value,
			Confidence:  r.Confidence,
			SourceQuote: r.SourceQuote,
		})
	}
	// Insert empty rows for skippable fields so they appear in the review screen.
	for _, f := range formFields {
		if !skippableIDs[f.ID] {
			continue
		}
		nullVal := jsonNull()
		upserts = append(upserts, UpsertFieldParams{
			ID:      domain.NewID(),
			NoteID:  noteID,
			FieldID: f.ID,
			Value:   &nullVal,
		})
	}

	if _, err := w.notes.UpsertNoteFields(ctx, noteID, upserts); err != nil {
		return fmt.Errorf("extract_note: upsert fields: %w", err)
	}

	// Mark note as draft — ready for staff review.
	if _, err := w.notes.UpdateNoteStatus(ctx, noteID, domain.NoteStatusDraft, nil); err != nil {
		return fmt.Errorf("extract_note: set draft: %w", err)
	}

	return nil
}

func jsonNull() string {
	b, _ := json.Marshal(nil)
	return string(b)
}
