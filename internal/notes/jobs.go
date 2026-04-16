package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/extraction"
	"github.com/melamphic/sal/internal/platform/confidence"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// ExtractNoteArgs is the River job payload for AI form extraction.
type ExtractNoteArgs struct {
	NoteID uuid.UUID `json:"note_id"`
}

// Kind returns the unique job type string used by River.
func (ExtractNoteArgs) Kind() string { return "extract_note" }

// ComputePolicyAlignmentArgs is the River job payload for policy alignment scoring.
type ComputePolicyAlignmentArgs struct {
	NoteID uuid.UUID `json:"note_id"`
}

// Kind returns the unique job type string used by River.
func (ComputePolicyAlignmentArgs) Kind() string { return "compute_policy_alignment" }

// ── Provider interfaces ───────────────────────────────────────────────────────
// These are satisfied by adapters in app.go that bridge to other modules.

// noteJobEnqueuer is the subset of river.Client used by the extraction worker to
// enqueue downstream jobs (e.g. policy alignment).
type noteJobEnqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// PolicyClause is a single enforceable clause from a linked policy version.
type PolicyClause struct {
	PolicyID string
	BlockID  string
	Title    string
	Parity   string // "high" | "medium" | "low"
}

// PolicyClauseProvider returns the enforceable policy clauses for a note's form version.
// Implemented by an adapter in app.go that bridges to forms + policy repos.
type PolicyClauseProvider interface {
	GetClausesForNote(ctx context.Context, formVersionID uuid.UUID) ([]PolicyClause, error)
}

// FormFieldMeta is the subset of form_fields data needed by the extraction job.
type FormFieldMeta struct {
	ID             uuid.UUID
	Title          string
	Type           string
	AIPrompt       *string
	Required       bool
	Skippable      bool
	AllowInference bool
	MinConfidence  *float64
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

// RecordingProvider fetches transcript and ASR word data from a recording.
// Implemented by an adapter over audio.Repository in app.go.
type RecordingProvider interface {
	GetTranscript(ctx context.Context, recordingID uuid.UUID) (transcript *string, err error)
	// GetWordConfidences returns the ASR word confidence index for a recording.
	// Returns nil (no error) when the recording has no word data (GeminiTranscriber).
	GetWordConfidences(ctx context.Context, recordingID uuid.UUID) ([]confidence.WordConfidence, error)
}

// ── Worker ────────────────────────────────────────────────────────────────────

// ExtractNoteWorker is the River worker that fills note fields using the AI extractor.
type ExtractNoteWorker struct {
	river.WorkerDefaults[ExtractNoteArgs]
	notes     repo
	forms     FormFieldProvider
	recording RecordingProvider
	extractor extraction.Extractor // nil = skip extraction (no API key configured)
	events    EventEmitter
	enqueue   noteJobEnqueuer // nil = skip downstream job enqueue
}

// NewExtractNoteWorker constructs an ExtractNoteWorker.
func NewExtractNoteWorker(
	notes repo,
	forms FormFieldProvider,
	recording RecordingProvider,
	extractor extraction.Extractor,
	events EventEmitter,
	enqueue noteJobEnqueuer,
) *ExtractNoteWorker {
	if events == nil {
		events = noopEmitter{}
	}
	return &ExtractNoteWorker{
		notes:     notes,
		forms:     forms,
		recording: recording,
		extractor: extractor,
		events:    events,
		enqueue:   enqueue,
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
		if w.enqueue != nil {
			_, _ = w.enqueue.Insert(ctx, ComputePolicyAlignmentArgs{NoteID: noteID}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByArgs: true},
			})
		}
		w.events.Emit(ctx, NoteEvent{
			NoteID:    noteID,
			SubjectID: note.SubjectID,
			ClinicID:  note.ClinicID,
			EventType: NoteEventExtractionComplete,
			ActorID:   note.CreatedBy,
			ActorRole: "system",
		})
		return nil
	}

	// Manual notes (no recording) should never reach this worker — guard defensively.
	if note.RecordingID == nil {
		_, err := w.notes.UpdateNoteStatus(ctx, noteID, domain.NoteStatusDraft, nil)
		if err != nil {
			return fmt.Errorf("extract_note: set draft (manual note): %w", err)
		}
		return nil
	}

	// Fetch transcript.
	transcript, err := w.recording.GetTranscript(ctx, *note.RecordingID)
	if err != nil {
		return fmt.Errorf("extract_note: get transcript: %w", err)
	}
	if transcript == nil || *transcript == "" {
		msg := "recording has no transcript — ensure transcription completed before extraction"
		_, _ = w.notes.UpdateNoteStatus(ctx, noteID, domain.NoteStatusFailed, &msg)
		return fmt.Errorf("extract_note: %s", msg)
	}

	// Fetch ASR word confidence index (nil for GeminiTranscriber — handled gracefully).
	var wordIndex []confidence.WordConfidence
	wc, wcErr := w.recording.GetWordConfidences(ctx, *note.RecordingID)
	if wcErr != nil {
		return fmt.Errorf("extract_note: get word confidences: %w", wcErr)
	}
	wordIndex = wc

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
	// Also build a meta index for post-processing enforcement.
	specs := make([]extraction.FieldSpec, 0, len(formFields))
	skippableIDs := make(map[uuid.UUID]bool)
	fieldMetaByID := make(map[uuid.UUID]FormFieldMeta, len(formFields))
	for _, f := range formFields {
		fieldMetaByID[f.ID] = f
		if f.Skippable {
			skippableIDs[f.ID] = true
			continue
		}
		prompt := ""
		if f.AIPrompt != nil {
			prompt = *f.AIPrompt
		}
		specs = append(specs, extraction.FieldSpec{
			ID:             f.ID.String(),
			Title:          f.Title,
			Type:           f.Type,
			AIPrompt:       prompt,
			Required:       f.Required,
			AllowInference: f.AllowInference,
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
			w.events.Emit(ctx, NoteEvent{
				NoteID:    noteID,
				SubjectID: note.SubjectID,
				ClinicID:  note.ClinicID,
				EventType: NoteEventExtractionFailed,
				ActorID:   note.CreatedBy,
				ActorRole: "system",
			})
			return fmt.Errorf("extract_note: %w", err)
		}
	}

	// Build upsert params from AI results, attaching deterministic confidence scores.
	upserts := make([]UpsertFieldParams, 0, len(results)+len(skippableIDs))
	for _, r := range results {
		fieldID, err := uuid.Parse(r.FieldID)
		if err != nil {
			continue
		}
		cr := confidence.ComputeFieldConfidence(
			derefStr(r.SourceQuote),
			derefStr(r.TransformationType),
			wordIndex,
		)

		p := UpsertFieldParams{
			ID:                 domain.NewID(),
			NoteID:             noteID,
			FieldID:            fieldID,
			Value:              r.Value,
			Confidence:         r.Confidence,
			SourceQuote:        r.SourceQuote,
			TransformationType: r.TransformationType,
			RequiresReview:     cr.GroundingSource == "ungrounded",
		}
		// Only populate ASR columns when we have real word data.
		if cr.GroundingSource != "no_asr_data" {
			p.ASRConfidence = &cr.ASRConfidence
			p.MinWordConfidence = &cr.MinWordConfidence
			p.AlignmentScore = &cr.AlignmentScore
			p.GroundingSource = &cr.GroundingSource
		}

		// Inference control: reject AI-inferred values for direct-only fields.
		meta := fieldMetaByID[fieldID]
		if !meta.AllowInference && derefStr(r.TransformationType) == "inference" {
			p.Value = nil
			p.Confidence = nil
			p.RequiresReview = true
		}
		// Min-confidence threshold: flag for review when ASR score is too low.
		if meta.MinConfidence != nil && cr.GroundingSource != "no_asr_data" && cr.GroundingSource != "ungrounded" {
			if cr.ASRConfidence < *meta.MinConfidence {
				p.RequiresReview = true
			}
		}

		upserts = append(upserts, p)
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

	// Best-effort: kick off policy alignment scoring now that fields are populated.
	if w.enqueue != nil {
		_, _ = w.enqueue.Insert(ctx, ComputePolicyAlignmentArgs{NoteID: noteID}, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		})
	}

	w.events.Emit(ctx, NoteEvent{
		NoteID:    noteID,
		SubjectID: note.SubjectID,
		ClinicID:  note.ClinicID,
		EventType: NoteEventExtractionComplete,
		ActorID:   note.CreatedBy,
		ActorRole: "system",
	})

	return nil
}

// ── ComputePolicyAlignmentWorker ──────────────────────────────────────────────

// ComputePolicyAlignmentWorker scores how well a note's field values align with
// the enforceable clauses of all policies linked to the note's form.
// The score is weighted by clause parity: high=3, medium=2, low=1.
type ComputePolicyAlignmentWorker struct {
	river.WorkerDefaults[ComputePolicyAlignmentArgs]
	notes   repo
	forms   FormFieldProvider
	clauses PolicyClauseProvider
	aligner extraction.PolicyAligner // nil = skip (no AI key configured)
}

// NewComputePolicyAlignmentWorker constructs a ComputePolicyAlignmentWorker.
func NewComputePolicyAlignmentWorker(
	notes repo,
	forms FormFieldProvider,
	clauses PolicyClauseProvider,
	aligner extraction.PolicyAligner,
) *ComputePolicyAlignmentWorker {
	return &ComputePolicyAlignmentWorker{
		notes:   notes,
		forms:   forms,
		clauses: clauses,
		aligner: aligner,
	}
}

// Work executes the policy alignment job.
// Steps: fetch note → fetch fields → fetch clauses → call AI → persist score.
func (w *ComputePolicyAlignmentWorker) Work(ctx context.Context, job *river.Job[ComputePolicyAlignmentArgs]) error {
	noteID := job.Args.NoteID

	if w.aligner == nil {
		return nil // no AI key — skip silently
	}

	note, err := w.notes.GetNoteByID(ctx, noteID, uuid.Nil)
	if err != nil {
		return fmt.Errorf("compute_policy_alignment: get note: %w", err)
	}

	clauses, err := w.clauses.GetClausesForNote(ctx, note.FormVersionID)
	if err != nil {
		return fmt.Errorf("compute_policy_alignment: get clauses: %w", err)
	}
	if len(clauses) == 0 {
		return nil // no linked policies with clauses — nothing to score
	}

	// Build note content string from field values paired with field titles.
	noteFields, err := w.notes.GetNoteFields(ctx, noteID)
	if err != nil {
		return fmt.Errorf("compute_policy_alignment: get fields: %w", err)
	}
	formFields, err := w.forms.GetFieldsByVersionID(ctx, note.FormVersionID)
	if err != nil {
		return fmt.Errorf("compute_policy_alignment: get form fields: %w", err)
	}

	// Index form field metadata by ID for O(1) title lookup.
	titleByID := make(map[uuid.UUID]string, len(formFields))
	for _, f := range formFields {
		titleByID[f.ID] = f.Title
	}

	var sb strings.Builder
	for _, f := range noteFields {
		title := titleByID[f.FieldID]
		if title == "" {
			title = f.FieldID.String()
		}
		val := "null"
		if f.Value != nil {
			val = *f.Value
		}
		sb.WriteString(title)
		sb.WriteString(": ")
		sb.WriteString(val)
		sb.WriteString("\n")
	}

	// Convert clauses to extraction type.
	extClauses := make([]extraction.PolicyClause, len(clauses))
	for i, c := range clauses {
		extClauses[i] = extraction.PolicyClause{
			BlockID: c.BlockID,
			Title:   c.Title,
			Parity:  c.Parity,
		}
	}

	pct, err := w.aligner.AlignPolicy(ctx, sb.String(), extClauses)
	if err != nil {
		return fmt.Errorf("compute_policy_alignment: align: %w", err)
	}

	if err := w.notes.UpdatePolicyAlignment(ctx, noteID, pct); err != nil {
		return fmt.Errorf("compute_policy_alignment: update: %w", err)
	}
	return nil
}

func jsonNull() string {
	b, _ := json.Marshal(nil)
	return string(b)
}

// derefStr returns the string value of a pointer, or "" if nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
