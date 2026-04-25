package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

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

// VerticalProvider returns the configured clinical vertical for a clinic
// ("veterinary", "dental", "aged_care", "general_clinic"). Used to frame the
// AI prompt so extraction and compliance checks target the right discipline.
// Implemented by an adapter in app.go.
type VerticalProvider interface {
	GetClinicVertical(ctx context.Context, clinicID uuid.UUID) (string, error)
}

// ── Worker ────────────────────────────────────────────────────────────────────

// ExtractNoteWorker is the River worker that fills note fields using the AI extractor.
type ExtractNoteWorker struct {
	river.WorkerDefaults[ExtractNoteArgs]
	notes     repo
	forms     FormFieldProvider
	recording RecordingProvider
	extractor extraction.Extractor // nil = skip extraction (no API key configured)
	verticals VerticalProvider     // nil = skip vertical context (generic prompt)
	events    EventEmitter
	enqueue   noteJobEnqueuer // nil = skip downstream job enqueue
}

// NewExtractNoteWorker constructs an ExtractNoteWorker.
func NewExtractNoteWorker(
	notes repo,
	forms FormFieldProvider,
	recording RecordingProvider,
	extractor extraction.Extractor,
	verticals VerticalProvider,
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
		verticals: verticals,
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

	// Fetch transcript. If the transcription job hasn't finished yet (it runs
	// concurrently with this one), return a retryable error so River retries
	// with exponential backoff. We do NOT mark the note Failed here — the
	// transient absence of a transcript is expected during the first ~30s
	// after upload while transcription is still running.
	transcript, err := w.recording.GetTranscript(ctx, *note.RecordingID)
	if err != nil {
		return fmt.Errorf("extract_note: get transcript: %w", err)
	}
	if transcript == nil || *transcript == "" {
		return fmt.Errorf("extract_note: transcript not ready yet, retrying")
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
	vertical := ""
	if w.verticals != nil {
		v, vErr := w.verticals.GetClinicVertical(ctx, note.ClinicID)
		if vErr == nil {
			vertical = v
		}
	}

	var results []extraction.FieldResult
	if len(specs) > 0 {
		results, err = w.extractor.Extract(ctx, vertical, *transcript, formPrompt, specs)
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
	notes     repo
	forms     FormFieldProvider
	clauses   PolicyClauseProvider
	aligner   extraction.PolicyAligner // nil = skip (no AI key configured)
	verticals VerticalProvider         // nil = skip vertical context (generic prompt)
}

// NewComputePolicyAlignmentWorker constructs a ComputePolicyAlignmentWorker.
func NewComputePolicyAlignmentWorker(
	notes repo,
	forms FormFieldProvider,
	clauses PolicyClauseProvider,
	aligner extraction.PolicyAligner,
	verticals VerticalProvider,
) *ComputePolicyAlignmentWorker {
	return &ComputePolicyAlignmentWorker{
		notes:     notes,
		forms:     forms,
		clauses:   clauses,
		aligner:   aligner,
		verticals: verticals,
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

	vertical := ""
	if w.verticals != nil {
		v, vErr := w.verticals.GetClinicVertical(ctx, note.ClinicID)
		if vErr == nil {
			vertical = v
		}
	}

	pct, err := w.aligner.AlignPolicy(ctx, vertical, sb.String(), extClauses)
	if err != nil {
		return fmt.Errorf("compute_policy_alignment: align: %w", err)
	}

	if err := w.notes.UpdatePolicyAlignment(ctx, noteID, pct); err != nil {
		return fmt.Errorf("compute_policy_alignment: update: %w", err)
	}
	return nil
}

// ── GenerateNotePDFWorker ─────────────────────────────────────────────────────

// GenerateNotePDFArgs is the River job payload for PDF generation after note submission.
type GenerateNotePDFArgs struct {
	NoteID uuid.UUID `json:"note_id"`
}

// Kind returns the unique job type string used by River.
func (GenerateNotePDFArgs) Kind() string { return "generate_note_pdf" }

// ClinicForRender is the clinic-profile data the PDF renderer needs to fill
// header/footer slots. Color is now sourced from the doc theme — the brand
// color lives there, not on the clinic row.
type ClinicForRender struct {
	Name    string
	Address *string
	Phone   *string
	Email   *string
}

// ClinicStyleProvider returns the clinic-profile fields used by the PDF
// renderer (header bar, footer slot substitution).
type ClinicStyleProvider interface {
	GetClinicStyle(ctx context.Context, clinicID uuid.UUID) (*ClinicForRender, error)
}

// StaffNameProvider returns a staff member's full name for the PDF audit footer.
type StaffNameProvider interface {
	GetStaffName(ctx context.Context, staffID, clinicID uuid.UUID) (string, error)
}

// FormMetaProvider returns the form name and version string for the PDF header.
type FormMetaProvider interface {
	GetFormMeta(ctx context.Context, formVersionID, clinicID uuid.UUID) (formName string, version string, err error)
}

// DocThemeProvider returns the active doc-theme for a clinic. Returns nil
// (no error) when the clinic has not customised — renderer falls back to
// built-in defaults.
type DocThemeProvider interface {
	GetActiveDocTheme(ctx context.Context, clinicID uuid.UUID) (*DocTheme, error)
}

// SystemHeaderProvider returns the per-form-version system_header config
// (the "patient" identity card pinned above body fields). Returns nil with
// no error if the version row carries no config.
type SystemHeaderProvider interface {
	GetSystemHeader(ctx context.Context, formVersionID uuid.UUID) (*SystemHeaderConfigForPDF, error)
}

// SubjectProvider resolves the linked subject row into the typed PDFSubject
// the renderer needs. Returns nil with no error when the note has no
// subject (skip-extraction manual notes recorded without a patient).
type SubjectProvider interface {
	GetSubjectForRender(ctx context.Context, subjectID, clinicID uuid.UUID) (*PDFSubject, error)
}

// GenerateNotePDFWorker builds a branded PDF after note submission, uploads to S3,
// and stores the storage key on the note.
type GenerateNotePDFWorker struct {
	river.WorkerDefaults[GenerateNotePDFArgs]
	notes      repo
	formMeta   FormMetaProvider
	formFields FormFieldProvider
	clinics    ClinicStyleProvider
	staff      StaffNameProvider
	theme      DocThemeProvider     // nil = renderer defaults
	headers    SystemHeaderProvider // nil = no patient header card
	subjects   SubjectProvider      // nil = no subject lookups (manual-only)
	store      pdfStore
}

// pdfStore is the subset of storage.Store used by the PDF worker.
type pdfStore interface {
	Upload(ctx context.Context, key, contentType string, body io.Reader, size int64) error
}

// NewGenerateNotePDFWorker constructs a GenerateNotePDFWorker.
func NewGenerateNotePDFWorker(
	notes repo,
	formMeta FormMetaProvider,
	formFields FormFieldProvider,
	clinics ClinicStyleProvider,
	staff StaffNameProvider,
	theme DocThemeProvider,
	headers SystemHeaderProvider,
	subjects SubjectProvider,
	store pdfStore,
) *GenerateNotePDFWorker {
	return &GenerateNotePDFWorker{
		notes:      notes,
		formMeta:   formMeta,
		formFields: formFields,
		clinics:    clinics,
		staff:      staff,
		theme:      theme,
		headers:    headers,
		subjects:   subjects,
		store:      store,
	}
}

// Work executes the PDF generation job.
func (w *GenerateNotePDFWorker) Work(ctx context.Context, job *river.Job[GenerateNotePDFArgs]) error {
	noteID := job.Args.NoteID

	note, err := w.notes.GetNoteByID(ctx, noteID, uuid.Nil)
	if err != nil {
		return fmt.Errorf("generate_note_pdf: get note: %w", err)
	}

	clinic, err := w.clinics.GetClinicStyle(ctx, note.ClinicID)
	if err != nil {
		return fmt.Errorf("generate_note_pdf: get clinic style: %w", err)
	}

	formName, formVersion, err := w.formMeta.GetFormMeta(ctx, note.FormVersionID, note.ClinicID)
	if err != nil {
		return fmt.Errorf("generate_note_pdf: get form meta: %w", err)
	}

	noteFields, err := w.notes.GetNoteFields(ctx, noteID)
	if err != nil {
		return fmt.Errorf("generate_note_pdf: get note fields: %w", err)
	}

	// Title index from the form's field definitions so the PDF can label
	// rows by title rather than UUID. Missing entries (rare — only after a
	// hard delete) fall back to the field ID.
	formFields, err := w.formFields.GetFieldsByVersionID(ctx, note.FormVersionID)
	if err != nil {
		return fmt.Errorf("generate_note_pdf: get form fields: %w", err)
	}
	titleByID := make(map[uuid.UUID]string, len(formFields))
	for _, f := range formFields {
		titleByID[f.ID] = f.Title
	}

	// Best-effort theme lookup — defaults render fine if unavailable.
	var theme *DocTheme
	if w.theme != nil {
		t, themeErr := w.theme.GetActiveDocTheme(ctx, note.ClinicID)
		if themeErr == nil {
			theme = t
		}
	}

	var sysHeader *SystemHeaderConfigForPDF
	if w.headers != nil {
		h, headerErr := w.headers.GetSystemHeader(ctx, note.FormVersionID)
		if headerErr == nil {
			sysHeader = h
		}
	}

	var subject *PDFSubject
	if w.subjects != nil && note.SubjectID != nil {
		s, subjErr := w.subjects.GetSubjectForRender(ctx, *note.SubjectID, note.ClinicID)
		if subjErr == nil {
			subject = s
		}
	}

	submittedBy := "Unknown"
	if note.SubmittedBy != nil {
		name, nameErr := w.staff.GetStaffName(ctx, *note.SubmittedBy, note.ClinicID)
		if nameErr == nil {
			submittedBy = name
			if subject != nil && subject.ClinicianName == nil {
				clin := name
				subject.ClinicianName = &clin
			}
		}
	}

	pdfFields := make([]PDFField, 0, len(noteFields))
	for _, f := range noteFields {
		val := ""
		if f.Value != nil && *f.Value != "null" {
			val = *f.Value
		}
		label := titleByID[f.FieldID]
		if label == "" {
			label = f.FieldID.String()
		}
		pdfFields = append(pdfFields, PDFField{Label: label, Value: val})
	}

	var submittedAt time.Time
	if note.SubmittedAt != nil {
		submittedAt = *note.SubmittedAt
	}

	visitDate := note.CreatedAt
	buf, err := BuildNotePDF(PDFInput{
		Theme:         theme,
		ClinicName:    clinic.Name,
		ClinicAddress: clinic.Address,
		ClinicPhone:   clinic.Phone,
		ClinicEmail:   clinic.Email,
		FormName:      formName,
		FormVersion:   formVersion,
		Fields:        pdfFields,
		SubmittedAt:   submittedAt,
		SubmittedBy:   submittedBy,
		NoteID:        noteID.String(),
		SystemHeader:  sysHeader,
		Subject:       subject,
		VisitDate:     &visitDate,
	})
	if err != nil {
		return fmt.Errorf("generate_note_pdf: build: %w", err)
	}

	// Upload to S3.
	key := fmt.Sprintf("notes/%s/%s.pdf", note.ClinicID, noteID)
	size := int64(buf.Len())
	if err := w.store.Upload(ctx, key, "application/pdf", buf, size); err != nil {
		return fmt.Errorf("generate_note_pdf: upload: %w", err)
	}

	// Store key on note record.
	if err := w.notes.UpdatePDFKey(ctx, noteID, key); err != nil {
		return fmt.Errorf("generate_note_pdf: update key: %w", err)
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
