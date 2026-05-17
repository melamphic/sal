package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/extraction"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// jobEnqueuer is the subset of river.Client used by the service.
type jobEnqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// maxNotesPerRecording is the maximum number of notes (form fills) per recording.
const maxNotesPerRecording = 3

// NoteCapEnforcer is the cross-domain hook the notecap module wires in
// to gate `CreateNote` against the per-period (or trial) note cap.
//
// CheckCanCreate returns domain.ErrForbidden when the clinic is at or
// above 150% of cap, or when a trial clinic has exhausted its 100-note
// quota. Returns nil otherwise.
//
// Evaluate runs after a successful create and fires the 80% warning /
// 110% CS notification cascade. Implementations must swallow non-fatal
// errors (e.g. mailer failures) internally — only DB read/write errors
// that should 500 the request bubble up here.
type NoteCapEnforcer interface {
	CheckCanCreate(ctx context.Context, clinicID uuid.UUID) error
	Evaluate(ctx context.Context, clinicID uuid.UUID) error
}

// DrugConfirmChecker checks whether any drug op linked to the note still
// needs explicit clinician confirmation. Implemented by drugs.Service.
// Set via SetDrugConfirmChecker; nil means the gate is skipped (used in
// tests and clinics without controlled-drug capture).
type DrugConfirmChecker interface {
	HasPendingConfirmForNote(ctx context.Context, noteID, clinicID uuid.UUID) (bool, error)
}

// Service handles business logic for the notes module.
type Service struct {
	repo          repo
	enqueue       jobEnqueuer
	events        EventEmitter
	fields        FormFieldProvider                // nil = skip field validation on submit
	policyChecker extraction.PolicyDetailedChecker // nil = skip policy check
	policyClauses PolicyClauseProvider             // nil = skip policy check
	verticals     VerticalProvider                 // nil = generic (vertical-neutral) prompts
	noteCap       NoteCapEnforcer                  // nil = cap not enforced (tests, local dev)
	pdf           *PDFRenderer                     // nil = skip sync PDF render (defer to worker)
	drugConfirm   DrugConfirmChecker               // nil = skip drug-confirm gate
	// System widget materialisers — wired via SetSystemMaterialisers
	// from app.go. Each adapter forwards into the relevant entity
	// service. nil = that materialiser path is unavailable; submit
	// gate still rejects unmaterialised fields.
	consentMat  ConsentMaterialiser
	drugOpMat   DrugOpMaterialiser
	incidentMat IncidentMaterialiser
	painMat     PainMaterialiser
	// Read-side summarisers — wired via SetSystemSummarisers. Used by
	// GetNote to enrich materialised system fields with a short
	// labelled summary the FE card + PDF render. Nil = no summary, the
	// surfaces fall back to a "linked" pill without details.
	consentSum  ConsentSummariser
	drugOpSum   DrugOpSummariser
	incidentSum IncidentSummariser
	painSum     PainSummariser
	// Attachments (photos / documents). uploader stores bytes; signer
	// mints short-lived download URLs on every GetNote so URLs never
	// expire from the client's perspective. Both wired by app.go;
	// either nil = that path is disabled.
	attachUploader AttachmentUploader
	attachSigner   AttachmentSigner
}

// NewService constructs a notes Service.
// Pass nil for events to discard all lifecycle events (tests, local dev without timeline).
func NewService(r repo, riverClient jobEnqueuer, events EventEmitter, fields FormFieldProvider) *Service {
	if events == nil {
		events = noopEmitter{}
	}
	return &Service{repo: r, enqueue: riverClient, events: events, fields: fields}
}

// SetPolicyChecker configures the detailed policy checker and clause provider.
// Called from app.go after constructing the service — avoids bloating the NewService signature.
func (s *Service) SetPolicyChecker(checker extraction.PolicyDetailedChecker, clauses PolicyClauseProvider) {
	s.policyChecker = checker
	s.policyClauses = clauses
}

// SetVerticalProvider wires the clinic-vertical resolver so the policy check
// prompt can be framed for the right discipline. Optional — without it, the
// check still runs with a generic "clinic type not specified" preamble.
func (s *Service) SetVerticalProvider(v VerticalProvider) {
	s.verticals = v
}

// SetDrugConfirmChecker wires the drug-op confirm-gate. When set, SubmitNote
// rejects submission if any system.drug_op widget on the form still has an
// unconfirmed (pending_confirm) drug operation linked to the note.
//
// The drug op confirm-gate is the regulator-binding rail on system.drug_op
// widgets — AI pre-fills the dose / route / witness, but the row stays
// pending_confirm until the clinician explicitly taps Confirm via the
// drugs /confirm endpoint.
func (s *Service) SetDrugConfirmChecker(c DrugConfirmChecker) {
	s.drugConfirm = c
}

// OnRecordingTranscribeFailed satisfies audio.TranscribeFailedListener.
// Called when the transcription worker permanently fails (last attempt or
// non-retryable error). Marks every note bound to the recording as failed
// with the humanized error so the FE banner appears immediately and the
// user can act (retry once quota recovers / switch provider). Without this
// hook the note stays in `extracting` forever from the FE's perspective —
// transcribe failures don't otherwise emit a `note.*` event for the
// realtime stream to surface.
func (s *Service) OnRecordingTranscribeFailed(ctx context.Context, recordingID uuid.UUID, errorMessage string) error {
	ids, err := s.repo.ListExtractingNoteIDsByRecording(ctx, recordingID)
	if err != nil {
		return fmt.Errorf("notes.service.OnRecordingTranscribeFailed: %w", err)
	}
	msg := "Transcription failed: " + humanizeExtractionError(fmt.Errorf("%s", errorMessage))
	for _, id := range ids {
		note, gerr := s.repo.GetNoteByID(ctx, id, uuid.Nil)
		if gerr != nil {
			continue
		}
		// Skip notes already marked failed — avoid clobbering a more
		// specific extraction error with the transcription one.
		if note.Status == domain.NoteStatusFailed {
			continue
		}
		_, _ = s.repo.UpdateNoteStatus(ctx, id, note.ClinicID, domain.NoteStatusFailed, &msg)
		s.events.Emit(ctx, NoteEvent{
			NoteID:    id,
			SubjectID: note.SubjectID,
			ClinicID:  note.ClinicID,
			EventType: NoteEventExtractionFailed,
			ActorID:   note.CreatedBy,
			ActorRole: "system",
		})
	}
	return nil
}

// OnRecordingTranscribed satisfies audio.TranscriptListener. Called by
// the audio TranscribeAudioWorker the instant a transcript lands on a
// recording row. Looks up every note in `extracting` or `failed` status
// bound to the recording and re-enqueues ExtractNoteArgs for each.
// `failed` notes get their status reset to `extracting` first so the UI
// flips to a working state immediately instead of staying on the stale
// failure banner while the new extraction runs.
//
// UniqueOpts ByArgs collapses with the immediate enqueue from
// CreateNote so the worker only runs once per (kind, NoteID) — the
// listener is the trigger for the in-flight job to wake up rather
// than a parallel run.
func (s *Service) OnRecordingTranscribed(ctx context.Context, recordingID uuid.UUID) error {
	ids, err := s.repo.ListExtractingNoteIDsByRecording(ctx, recordingID)
	if err != nil {
		return fmt.Errorf("notes.service.OnRecordingTranscribed: %w", err)
	}
	for _, id := range ids {
		// Reset status (and clear stale error) so the UI doesn't flicker
		// "failed" while the fresh extraction is running. GetNoteByID with
		// uuid.Nil clinic skips the tenant guard — listener is internal.
		if note, gerr := s.repo.GetNoteByID(ctx, id, uuid.Nil); gerr == nil &&
			note.Status == domain.NoteStatusFailed {
			_, _ = s.repo.UpdateNoteStatus(ctx, id, note.ClinicID, domain.NoteStatusExtracting, nil)
		}
		opts := &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		}
		if _, err := s.enqueue.Insert(ctx, ExtractNoteArgs{NoteID: id}, opts); err != nil {
			// Log via wrapper, don't fail the whole batch — other notes
			// for this recording still deserve a chance.
			_ = err
		}
	}
	return nil
}

// SetNoteCapEnforcer wires the note-cap pre-check + cascade evaluator.
// Optional — leaving it nil disables enforcement (used by unit tests
// that don't care about billing).
func (s *Service) SetNoteCapEnforcer(c NoteCapEnforcer) {
	s.noteCap = c
}

// SetPDFRenderer wires the synchronous PDF renderer used inside SubmitNote.
// When set, submit produces the canonical PDF inline so the response carries
// `pdf_storage_key` and the review page never has to wait. If render fails the
// submit still succeeds — a River fallback job ensures the artifact lands
// eventually, and the retry-pdf endpoint is available for manual nudges.
func (s *Service) SetPDFRenderer(r *PDFRenderer) {
	s.pdf = r
}

// LookupFormNamesByNoteIDs batches a (note → form.name) fetch for the
// given note ids. Used by cross-domain feeds (staff activity) that need
// to decorate notes with a readable label instead of a raw UUID.
func (s *Service) LookupFormNamesByNoteIDs(ctx context.Context, clinicID uuid.UUID, noteIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out, err := s.repo.LookupFormNamesByNoteIDs(ctx, clinicID, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("notes.service.LookupFormNamesByNoteIDs: %w", err)
	}
	return out, nil
}

// AttachmentUploader stores note-attachment bytes in object storage and
// returns the persisted key (durable id for re-signing) plus a short-
// lived URL the frontend can render immediately. Mirrors the patient-
// photo pattern. Implemented in app.go by a storage adapter; nil in
// tests / local dev that skip the upload path.
type AttachmentUploader interface {
	UploadNoteAttachment(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (key, url string, err error)
}

// AttachmentSigner mints a short-lived signed download URL from a stored
// attachment key. Called every time the service builds a NoteResponse
// so attachment URLs never expire from the client's perspective.
type AttachmentSigner interface {
	SignNoteAttachment(ctx context.Context, key string) (string, error)
}

// SetAttachmentUploader wires the object-storage adapter that backs
// `POST /api/v1/notes/{id}/upload-attachment`. Optional — without it,
// the upload endpoint returns 503.
func (s *Service) SetAttachmentUploader(u AttachmentUploader) {
	s.attachUploader = u
}

// SetAttachmentSigner wires the read-side adapter that turns a stored
// attachment key into a freshly signed URL on every note read.
func (s *Service) SetAttachmentSigner(p AttachmentSigner) {
	s.attachSigner = p
}

// resolveAttachmentURL returns the URL the client should render for an
// attachment. Falls back to empty string when the signer is unwired
// (test/local dev). Errors are swallowed so a transient signer failure
// can't bring the whole note read down — the row still surfaces with
// metadata, just without a renderable URL until the next read.
func (s *Service) resolveAttachmentURL(ctx context.Context, key string) string {
	if s.attachSigner == nil || key == "" {
		return ""
	}
	url, err := s.attachSigner.SignNoteAttachment(ctx, key)
	if err != nil {
		return ""
	}
	return url
}

// UploadNoteAttachment streams [body] into object storage and inserts
// the metadata row pointing at the persisted key. Returns the summary
// shape the FE renders into the attachments gallery.
//
// The handler enforces content-type + size; this method does no
// validation beyond storage / DB plumbing.
func (s *Service) UploadNoteAttachment(
	ctx context.Context,
	noteID, clinicID, uploaderID uuid.UUID,
	contentType string,
	body io.Reader,
	size int64,
) (*NoteAttachmentSummary, error) {
	if s.attachUploader == nil {
		return nil, fmt.Errorf("notes.service.UploadNoteAttachment: storage not configured")
	}
	// Note must exist + belong to this clinic. GetNoteByID already
	// scopes by clinic; a missing row yields ErrNotFound which the
	// handler maps to 404.
	if _, err := s.repo.GetNoteByID(ctx, noteID, clinicID); err != nil {
		return nil, fmt.Errorf("notes.service.UploadNoteAttachment: lookup: %w", err)
	}
	key, _, err := s.attachUploader.UploadNoteAttachment(ctx, clinicID, contentType, body, size)
	if err != nil {
		return nil, fmt.Errorf("notes.service.UploadNoteAttachment: upload: %w", err)
	}
	rec, err := s.repo.InsertAttachment(ctx, CreateAttachmentParams{
		ID:          uuid.Must(uuid.NewV7()),
		ClinicID:    clinicID,
		NoteID:      noteID,
		Kind:        attachmentKindFor(contentType),
		S3Key:       key,
		ContentType: contentType,
		Bytes:       size,
		UploadedBy:  uploaderID,
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.UploadNoteAttachment: insert: %w", err)
	}
	return s.toAttachmentSummary(ctx, rec), nil
}

// ListNoteAttachments returns the gallery for a note. Each summary
// carries a freshly signed URL.
func (s *Service) ListNoteAttachments(ctx context.Context, noteID, clinicID uuid.UUID) ([]*NoteAttachmentSummary, error) {
	if _, err := s.repo.GetNoteByID(ctx, noteID, clinicID); err != nil {
		return nil, fmt.Errorf("notes.service.ListNoteAttachments: lookup: %w", err)
	}
	rows, err := s.repo.ListAttachmentsByNote(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.ListNoteAttachments: %w", err)
	}
	out := make([]*NoteAttachmentSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toAttachmentSummary(ctx, r))
	}
	return out, nil
}

// ArchiveNoteAttachment soft-deletes an attachment. Caller-permission
// gate (uploader-pre-submit vs manageNotes-post-submit) is enforced at
// the handler layer.
func (s *Service) ArchiveNoteAttachment(ctx context.Context, attachmentID, clinicID uuid.UUID) error {
	if err := s.repo.ArchiveAttachment(ctx, attachmentID, clinicID); err != nil {
		return fmt.Errorf("notes.service.ArchiveNoteAttachment: %w", err)
	}
	return nil
}

// GetNoteAttachment fetches a single attachment for permission checks
// before archive. Surfaces ErrNotFound when missing.
func (s *Service) GetNoteAttachment(ctx context.Context, attachmentID, clinicID uuid.UUID) (*NoteAttachmentRecord, error) {
	rec, err := s.repo.GetAttachment(ctx, attachmentID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.GetNoteAttachment: %w", err)
	}
	return rec, nil
}

// attachmentKindFor maps content-type to the kind enum stored in the
// table. Conservative — anything not obviously a photo or document
// falls into "other" so a typo can't violate the CHECK constraint.
func attachmentKindFor(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "photo"
	case contentType == "application/pdf",
		strings.HasPrefix(contentType, "application/vnd.openxmlformats"),
		contentType == "application/msword",
		contentType == "text/plain":
		return "document"
	default:
		return "other"
	}
}

func (s *Service) toAttachmentSummary(ctx context.Context, r *NoteAttachmentRecord) *NoteAttachmentSummary {
	return &NoteAttachmentSummary{
		ID:          r.ID.String(),
		Kind:        r.Kind,
		PhotoURL:    s.resolveAttachmentURL(ctx, r.S3Key),
		ContentType: r.ContentType,
		Bytes:       r.Bytes,
		UploadedBy:  r.UploadedBy.String(),
		UploadedAt:  r.UploadedAt.Format(time.RFC3339),
	}
}

// ── Response types ────────────────────────────────────────────────────────────

// NoteFieldResponse is the API-safe representation of a single note field.
type NoteFieldResponse struct {
	FieldID            string   `json:"field_id"`
	Value              *string  `json:"value,omitempty"`
	Confidence         *float64 `json:"confidence,omitempty"`
	SourceQuote        *string  `json:"source_quote,omitempty"`
	TransformationType *string  `json:"transformation_type,omitempty"`
	OverriddenBy       *string  `json:"overridden_by,omitempty"`
	OverriddenAt       *string  `json:"overridden_at,omitempty"`
	// SystemSummary — populated for materialised system widgets only.
	// A short labelled list of the entity's key fields (drug name +
	// quantity for a drug op, score + scale for pain, etc) so the FE
	// card / PDF can render what was captured instead of just an id.
	// Resolved server-side from the typed entity table on GetNote.
	SystemSummary []NoteFieldSystemSummaryItem `json:"system_summary,omitempty"`

	// SystemReviewStatus — populated for materialised system widgets that
	// participate in the compliance approval queue. Values:
	// "not_required" / "pending" / "approved" / "challenged". Empty for
	// non-system or non-reviewable fields. Drives the witness pill on
	// the FE card and the equivalent PDF footer line.
	SystemReviewStatus string `json:"system_review_status,omitempty"`
}

// NoteFieldSystemSummaryItem is one row in the system widget summary —
// a labelled value rendered on the materialised card / PDF.
type NoteFieldSystemSummaryItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// NoteAttachmentSummary is the API-safe representation of a single
// note attachment. PhotoURL is freshly signed on every read so the
// client never holds a stale URL. Kind drives the FE icon (photo vs
// document) and the gallery layout (image grid vs file list).
//
//nolint:revive
type NoteAttachmentSummary struct {
	ID          string `json:"id"`
	Kind        string `json:"kind" enum:"photo,document,other"`
	PhotoURL    string `json:"photo_url"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	UploadedBy  string `json:"uploaded_by"`
	UploadedAt  string `json:"uploaded_at"`
}

// NoteResponse is the API-safe representation of a clinical note.
//
//nolint:revive
type NoteResponse struct {
	ID                 string               `json:"id"`
	ClinicID           string               `json:"clinic_id"`
	RecordingID        *string              `json:"recording_id,omitempty"` // nil for manual notes
	FormVersionID      string               `json:"form_version_id"`
	SubjectID          *string              `json:"subject_id,omitempty"`
	CreatedBy          string               `json:"created_by"`
	Status             domain.NoteStatus    `json:"status"`
	ErrorMessage       *string              `json:"error_message,omitempty"`
	ReviewedBy         *string              `json:"reviewed_by,omitempty"`
	ReviewedAt         *string              `json:"reviewed_at,omitempty"`
	SubmittedAt        *string              `json:"submitted_at,omitempty"`
	SubmittedBy        *string              `json:"submitted_by,omitempty"`
	ArchivedAt         *string              `json:"archived_at,omitempty"`
	FormVersionContext *string              `json:"form_version_context,omitempty"`
	// FormVersionLabel is the human-readable semver of the form version this
	// note was filed against, e.g. "v1.2" or "Draft". Populated on list
	// responses; nil on single-note GET (formVersionId is already present).
	FormVersionLabel *string `json:"form_version_label,omitempty"`
	PolicyAlignmentPct *float64             `json:"policy_alignment_pct,omitempty"`
	// OverrideReason/By/At are populated when the submitter overrode a
	// high-parity policy violation at submit time.
	OverrideReason *string `json:"override_reason,omitempty"`
	OverrideBy     *string `json:"override_by,omitempty"`
	OverrideAt     *string `json:"override_at,omitempty"`
	// OverrideUnlocked* are populated when a privileged staff re-opens
	// a submitted note for correction. Persisted across re-submits so
	// the audit trail survives. OverrideCount increments each commit.
	OverrideUnlockedAt     *string              `json:"override_unlocked_at,omitempty"`
	OverrideUnlockedBy     *string              `json:"override_unlocked_by,omitempty"`
	OverrideUnlockedReason *string              `json:"override_unlocked_reason,omitempty"`
	OverrideCount          int                  `json:"override_count"`
	PDFStorageKey          *string              `json:"pdf_storage_key,omitempty"`
	CreatedAt              string               `json:"created_at"`
	UpdatedAt              string               `json:"updated_at"`
	Fields                 []*NoteFieldResponse `json:"fields,omitempty"`
	// Attachments — populated only by GetNote (and submit/retry paths
	// that re-emit the full response). ListNotes leaves it nil to keep
	// the list payload small; the FE calls GetNote on detail open.
	Attachments []*NoteAttachmentSummary `json:"attachments,omitempty"`
}

// NoteListResponse is a paginated list of notes.
//
//nolint:revive
type NoteListResponse struct {
	Items  []*NoteResponse `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// ── Input types ───────────────────────────────────────────────────────────────

// CreateNoteInput holds validated input for creating a new note.
type CreateNoteInput struct {
	ClinicID       uuid.UUID
	StaffID        uuid.UUID
	ActorRole      string     // JWT role string — recorded in the audit event
	RecordingID    *uuid.UUID // nil for manual notes
	FormVersionID  uuid.UUID
	SubjectID      *uuid.UUID
	SkipExtraction bool // true = manual note; skip AI extraction job
}

// ListNotesInput holds filter and pagination parameters.
// Must stay structurally identical to ListNotesParams for direct type conversion.
type ListNotesInput struct {
	Limit           int
	Offset          int
	RecordingID     *uuid.UUID
	SubjectID       *uuid.UUID
	Status          *domain.NoteStatus
	IncludeArchived bool
}

// UpdateFieldInput holds validated input for a staff override of a single field.
type UpdateFieldInput struct {
	NoteID    uuid.UUID
	ClinicID  uuid.UUID
	StaffID   uuid.UUID
	ActorRole string // JWT role string — recorded in the audit event
	FieldID   uuid.UUID
	Value     *string // JSON-encoded
}

// ── Service methods ───────────────────────────────────────────────────────────

// CreateNote creates a note and (unless SkipExtraction) enqueues the extraction job.
// Enforces the 3-notes-per-recording cap when a recording is provided.
func (s *Service) CreateNote(ctx context.Context, input CreateNoteInput) (*NoteResponse, error) {
	if s.noteCap != nil {
		if err := s.noteCap.CheckCanCreate(ctx, input.ClinicID); err != nil {
			return nil, fmt.Errorf("notes.service.CreateNote: %w", err)
		}
	}

	if input.RecordingID != nil {
		count, err := s.repo.CountNotesByRecording(ctx, input.ClinicID, *input.RecordingID)
		if err != nil {
			return nil, fmt.Errorf("notes.service.CreateNote: count: %w", err)
		}
		if count >= maxNotesPerRecording {
			return nil, fmt.Errorf("notes.service.CreateNote: max %d notes per recording: %w",
				maxNotesPerRecording, domain.ErrConflict)
		}
	}

	status := domain.NoteStatusExtracting
	if input.SkipExtraction {
		status = domain.NoteStatusDraft
	}

	noteID := domain.NewID()
	note, err := s.repo.CreateNote(ctx, CreateNoteParams{
		ID:            noteID,
		ClinicID:      input.ClinicID,
		RecordingID:   input.RecordingID,
		FormVersionID: input.FormVersionID,
		SubjectID:     input.SubjectID,
		CreatedBy:     input.StaffID,
		Status:        status,
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.CreateNote: %w", err)
	}

	// Seed an empty note_fields row per form field so the FE can PATCH
	// any field by id without hitting a 404. Required for manual notes
	// (AI never runs); harmless for AI notes (the extraction worker's
	// UpsertNoteFields fills the same rows via INSERT...ON CONFLICT).
	// Best-effort: a failure here logs but does not block note creation
	// — the user can still resume by re-trying field PATCH after the
	// extraction worker writes the rows.
	if s.fields != nil {
		formFields, ferr := s.fields.GetFieldsByVersionID(ctx, input.FormVersionID)
		if ferr != nil {
			return nil, fmt.Errorf("notes.service.CreateNote: get fields: %w", ferr)
		}
		fieldIDs := make([]uuid.UUID, len(formFields))
		for i, f := range formFields {
			fieldIDs[i] = f.ID
		}
		if err := s.repo.SeedNoteFields(ctx, noteID, fieldIDs); err != nil {
			return nil, fmt.Errorf("notes.service.CreateNote: seed fields: %w", err)
		}
	}

	if !input.SkipExtraction {
		// Enqueue extraction immediately. Two things keep this from
		// burning a 60-second River retry on missing transcripts:
		//   1. The audio TranscribeAudioWorker calls our
		//      OnRecordingTranscribed listener the moment the transcript
		//      lands → fires a UniqueOpts-deduplicated re-enqueue.
		//   2. ExtractNoteWorker uses river.JobSnoozeError(3s) when the
		//      transcript is missing rather than failing — backstop for
		//      cases where the listener didn't fire (e.g. transcribe
		//      worker crashed).
		// UniqueOpts ByArgs collapses both enqueue paths to a single job
		// per (kind, NoteID) so the worker only runs once per outcome.
		opts := &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		}
		if _, err := s.enqueue.Insert(ctx, ExtractNoteArgs{NoteID: noteID}, opts); err != nil {
			return nil, fmt.Errorf("notes.service.CreateNote: enqueue: %w", err)
		}
	}

	s.events.Emit(ctx, NoteEvent{
		NoteID:    noteID,
		SubjectID: input.SubjectID,
		ClinicID:  input.ClinicID,
		EventType: NoteEventCreated,
		ActorID:   input.StaffID,
		ActorRole: input.ActorRole,
	})

	// Best-effort cap evaluation — fires the 80% warning email or 110%
	// CS notification when this note crosses a threshold for the first
	// time in the period. Never blocks create; the enforcer logs its
	// own failures (mailer errors, etc.) so we don't surface them here.
	if s.noteCap != nil {
		if err := s.noteCap.Evaluate(ctx, input.ClinicID); err != nil {
			return nil, fmt.Errorf("notes.service.CreateNote: cap evaluate: %w", err)
		}
	}

	return toNoteResponse(note, nil), nil
}

// GetNote fetches a note with its current field values.
func (s *Service) GetNote(ctx context.Context, id, clinicID uuid.UUID) (*NoteResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, id, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.GetNote: %w", err)
	}

	fields, err := s.repo.GetNoteFields(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("notes.service.GetNote: fields: %w", err)
	}

	resp := toNoteResponse(note, fields)
	s.enrichSystemSummaries(ctx, clinicID, resp.Fields)

	// Attach the photo / document gallery. Best-effort: a list failure
	// shouldn't bring the whole read down — leaves attachments nil and
	// the FE renders the note without the gallery section.
	if rows, err := s.repo.ListAttachmentsByNote(ctx, id, clinicID); err == nil {
		out := make([]*NoteAttachmentSummary, 0, len(rows))
		for _, r := range rows {
			out = append(out, s.toAttachmentSummary(ctx, r))
		}
		resp.Attachments = out
	}
	return resp, nil
}

// enrichSystemSummaries fills NoteFieldResponse.SystemSummary for any
// system.* field whose value is an id-pointer. Failures are non-fatal —
// the field stays without a summary so the FE shows "linked" without
// details rather than failing the whole GetNote call.
func (s *Service) enrichSystemSummaries(
	ctx context.Context,
	clinicID uuid.UUID,
	fields []*NoteFieldResponse,
) {
	for _, f := range fields {
		if f.Value == nil {
			continue
		}
		entityID, kind := decodeIDPointer(*f.Value)
		if kind == "" {
			continue
		}
		summary, err := s.summariseByKind(ctx, kind, entityID, clinicID)
		if err != nil || summary == nil {
			continue
		}
		f.SystemSummary = make([]NoteFieldSystemSummaryItem, len(summary.Items))
		for i, it := range summary.Items {
			f.SystemSummary[i] = NoteFieldSystemSummaryItem(it)
		}
		f.SystemReviewStatus = summary.ReviewStatus
	}
}

// summariseByKind dispatches to the per-kind summariser based on the
// id-pointer key (consent_id / operation_id / incident_id / pain_score_id).
func (s *Service) summariseByKind(
	ctx context.Context,
	kind string,
	entityID, clinicID uuid.UUID,
) (*SystemSummary, error) {
	switch kind {
	case "consent_id":
		if s.consentSum == nil {
			return nil, nil //nolint:nilnil
		}
		out, err := s.consentSum.SummariseConsent(ctx, entityID, clinicID)
		if err != nil {
			return nil, fmt.Errorf("notes.service.summariseByKind: %w", err)
		}
		return out, nil
	case "operation_id":
		if s.drugOpSum == nil {
			return nil, nil //nolint:nilnil
		}
		out, err := s.drugOpSum.SummariseDrugOp(ctx, entityID, clinicID)
		if err != nil {
			return nil, fmt.Errorf("notes.service.summariseByKind: %w", err)
		}
		return out, nil
	case "incident_id":
		if s.incidentSum == nil {
			return nil, nil //nolint:nilnil
		}
		out, err := s.incidentSum.SummariseIncident(ctx, entityID, clinicID)
		if err != nil {
			return nil, fmt.Errorf("notes.service.summariseByKind: %w", err)
		}
		return out, nil
	case "pain_score_id":
		if s.painSum == nil {
			return nil, nil //nolint:nilnil
		}
		out, err := s.painSum.SummarisePain(ctx, entityID, clinicID)
		if err != nil {
			return nil, fmt.Errorf("notes.service.summariseByKind: %w", err)
		}
		return out, nil
	}
	return nil, nil //nolint:nilnil
}

// decodeIDPointer reads a {"<kind>":"<uuid>"} JSON value. Returns the
// id and the kind key, or zero/empty when the value isn't a valid
// id-pointer (AI payload, plain text, null, …).
func decodeIDPointer(raw string) (uuid.UUID, string) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "null" {
		return uuid.Nil, ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return uuid.Nil, ""
	}
	for _, k := range []string{"consent_id", "operation_id", "incident_id", "pain_score_id"} {
		if v, ok := m[k]; ok && v != "" {
			id, err := uuid.Parse(v)
			if err == nil {
				return id, k
			}
		}
	}
	return uuid.Nil, ""
}

// ListNotes returns a paginated list of notes for a clinic.
// Archived notes are excluded by default; set IncludeArchived to include them.
func (s *Service) ListNotes(ctx context.Context, clinicID uuid.UUID, input ListNotesInput) (*NoteListResponse, error) {
	input.Limit = clampLimit(input.Limit)

	notes, total, err := s.repo.ListNotes(ctx, clinicID, ListNotesParams(input))
	if err != nil {
		return nil, fmt.Errorf("notes.service.ListNotes: %w", err)
	}

	items := make([]*NoteResponse, len(notes))
	for i, n := range notes {
		items[i] = toNoteResponse(n, nil)
	}

	return &NoteListResponse{
		Items:  items,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
}

// UpdateField records a staff override for a single note field.
// Allowed when the note is in 'draft' status, or when it has been
// re-opened post-submit via the override-unlock flow ('overriding').
func (s *Service) UpdateField(ctx context.Context, input UpdateFieldInput) (*NoteFieldResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, input.NoteID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.UpdateField: %w", err)
	}
	if note.Status != domain.NoteStatusDraft &&
		note.Status != domain.NoteStatusOverriding {
		return nil, fmt.Errorf("notes.service.UpdateField: note not editable: %w", domain.ErrConflict)
	}

	// Capture old value for the audit event before overwriting.
	existing, _ := s.repo.GetNoteFields(ctx, input.NoteID)
	var oldVal *string
	for _, f := range existing {
		if f.FieldID == input.FieldID {
			oldVal = f.Value
			break
		}
	}

	f, err := s.repo.UpdateNoteField(ctx, UpdateNoteFieldParams{
		NoteID:       input.NoteID,
		FieldID:      input.FieldID,
		ClinicID:     input.ClinicID,
		Value:        input.Value,
		OverriddenBy: input.StaffID,
		OverriddenAt: domain.TimeNow(),
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.UpdateField: %w", err)
	}

	fieldID := input.FieldID
	s.events.Emit(ctx, NoteEvent{
		NoteID:    input.NoteID,
		SubjectID: note.SubjectID,
		ClinicID:  input.ClinicID,
		EventType: NoteEventFieldChanged,
		FieldID:   &fieldID,
		OldValue:  oldVal,
		NewValue:  input.Value,
		ActorID:   input.StaffID,
		ActorRole: input.ActorRole,
	})

	return toFieldResponse(f), nil
}

// SubmitNote transitions a note from draft → submitted.
// Sets reviewed_by and submitted_by to staffID (same person acknowledges and submits).
// Returns domain.ErrValidation if any required fields are missing values.
// When overrideReason is non-nil (and non-empty) the submitter is asserting
// an override of the high-parity policy check — the reason is persisted on
// the note and the policy gate is skipped. A nil/empty reason with failing
// high-parity clauses still blocks submit.
func (s *Service) SubmitNote(ctx context.Context, noteID, clinicID, staffID uuid.UUID, staffRole string, overrideReason *string) (*NoteResponse, error) {
	// Normalize empty string to nil so we don't write blank justifications.
	if overrideReason != nil && strings.TrimSpace(*overrideReason) == "" {
		overrideReason = nil
	}

	// Pre-submit validation: required fields + policy check (unless overridden) +
	// drug-op confirm gate (always — never override-able).
	{
		preNote, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
		if err != nil {
			return nil, fmt.Errorf("notes.service.SubmitNote: get note: %w", err)
		}
		if s.fields != nil {
			if err := s.validateForSubmission(ctx, preNote.FormVersionID, noteID); err != nil {
				return nil, err
			}
		}
		if overrideReason == nil {
			if err := s.validatePolicyCheck(preNote); err != nil {
				return nil, err
			}
		}
		// Regulator rail #1: every drug op linked to this note via a
		// system.drug_op widget must be explicitly confirmed before
		// submission. Override does NOT bypass — the gate is for
		// regulator-binding records (CD register), not policy alignment.
		// Note: the new submit-time materialise path always creates drug
		// op rows with status='confirmed' (via the materialiser adapter),
		// so the only pending_confirm rows seen here come from any
		// remaining legacy auto-materialise paths or out-of-band creates.
		if s.drugConfirm != nil {
			pending, err := s.drugConfirm.HasPendingConfirmForNote(ctx, noteID, clinicID)
			if err != nil {
				return nil, fmt.Errorf("notes.service.SubmitNote: drug-confirm check: %w", err)
			}
			if pending {
				return nil, fmt.Errorf("notes.service.SubmitNote: cannot submit while drug operations are pending confirmation: %w", domain.ErrValidation)
			}
		}
	}

	// Regulator rail #2: walk every system.* field, parse its
	// structured payload, and materialise it into the typed ledger.
	// Drafts are never written to the regulator-binding ledgers — the
	// only ledger writes happen here, on submit, so deleting a draft
	// leaves no orphans. Direct-from-patient creates still hit the
	// entity services straight (no note involved).
	if err := s.materialiseSystemFieldsForSubmit(ctx, noteID, clinicID, staffID); err != nil {
		return nil, fmt.Errorf("notes.service.SubmitNote: %w", err)
	}

	// Capture prior status BEFORE SubmitNote — repo update transitions to
	// 'submitted', losing the signal that we came from 'overriding' which
	// drives override-committed event emission below.
	priorStatus := domain.NoteStatusDraft
	if pre, perr := s.repo.GetNoteByID(ctx, noteID, clinicID); perr == nil {
		priorStatus = pre.Status
	}

	now := domain.TimeNow()
	note, err := s.repo.SubmitNote(ctx, SubmitNoteParams{
		ID:             noteID,
		ClinicID:       clinicID,
		ReviewedBy:     staffID,
		ReviewedAt:     now,
		SubmittedBy:    staffID,
		SubmittedAt:    now,
		OverrideReason: overrideReason,
	})
	if err != nil {
		return nil, fmt.Errorf("notes.service.SubmitNote: %w", err)
	}

	// First-submit fires note.submitted. Re-submit out of an overriding
	// state fires note.override_committed instead so the timeline can
	// render "Corrected by X" rather than a duplicate "Submitted by X".
	emitType := NoteEventSubmitted
	var emitReason *string
	if priorStatus == domain.NoteStatusOverriding {
		emitType = NoteEventOverrideCommitted
		if note.OverrideUnlockedReason != nil {
			r := *note.OverrideUnlockedReason
			emitReason = &r
		}
	}
	s.events.Emit(ctx, NoteEvent{
		NoteID:    noteID,
		SubjectID: note.SubjectID,
		ClinicID:  clinicID,
		EventType: emitType,
		Reason:    emitReason,
		ActorID:   staffID,
		ActorRole: staffRole,
	})

	// Best-effort: recompute alignment against submitted field values.
	_, _ = s.enqueue.Insert(ctx, ComputePolicyAlignmentArgs{NoteID: noteID}, nil)

	// Render the canonical PDF synchronously so the submit response already
	// carries pdf_storage_key — the review page never sits on a spinner.
	// If render fails (transient storage glitch, missing dep) we log and
	// enqueue the worker as a recovery path; submit still succeeds.
	if s.pdf != nil {
		if rerr := s.pdf.Render(ctx, noteID); rerr != nil {
			slog.Error("notes: sync PDF render failed; enqueueing fallback",
				"note_id", noteID.String(), "error", rerr.Error())
			_, _ = s.enqueue.Insert(ctx, GenerateNotePDFArgs{NoteID: noteID}, nil)
		}
	} else {
		// No sync renderer wired (e.g. unit tests) — fall back to queue.
		_, _ = s.enqueue.Insert(ctx, GenerateNotePDFArgs{NoteID: noteID}, nil)
	}

	// Always re-hydrate via GetNote so the response carries field values + the
	// freshly-stored pdf_storage_key. Returning toNoteResponse(note, nil) here
	// would ship an empty fields[] and blank the cubit's form on the client.
	return s.GetNote(ctx, noteID, clinicID)
}

// ArchiveNote soft-deletes a note. Archived notes are hidden from list results
// unless include_archived is set.
func (s *Service) ArchiveNote(ctx context.Context, noteID, clinicID, staffID uuid.UUID, staffRole string) (*NoteResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.ArchiveNote: %w", err)
	}
	if note.ArchivedAt != nil {
		return nil, fmt.Errorf("notes.service.ArchiveNote: already archived: %w", domain.ErrConflict)
	}

	if _, err := s.repo.ArchiveNote(ctx, ArchiveNoteParams{
		ID:         noteID,
		ClinicID:   clinicID,
		ArchivedAt: domain.TimeNow(),
	}); err != nil {
		return nil, fmt.Errorf("notes.service.ArchiveNote: %w", err)
	}

	s.events.Emit(ctx, NoteEvent{
		NoteID:    noteID,
		SubjectID: note.SubjectID,
		ClinicID:  clinicID,
		EventType: NoteEventArchived,
		ActorID:   staffID,
		ActorRole: staffRole,
	})

	// Re-hydrate via GetNote so the response carries field values for the UI.
	return s.GetNote(ctx, noteID, clinicID)
}

// UnlockForOverride re-opens a submitted note for post-submit correction.
// Only the note's original creator OR a manage_staff member may unlock —
// caller must enforce the permission gate before invoking. The note must
// be in 'submitted' status (not draft, not extracting, not archived);
// other states return ErrConflict. The reason is required and persisted
// on the note for audit. A note.override_unlocked event is emitted so
// the patient timeline carries an explicit "Re-opened by X: reason" row.
func (s *Service) UnlockForOverride(ctx context.Context, noteID, clinicID, staffID uuid.UUID, staffRole, reason string) (*NoteResponse, error) {
	r := strings.TrimSpace(reason)
	if r == "" {
		return nil, fmt.Errorf("notes.service.UnlockForOverride: reason required: %w", domain.ErrValidation)
	}
	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.UnlockForOverride: %w", err)
	}
	if note.ArchivedAt != nil {
		return nil, fmt.Errorf("notes.service.UnlockForOverride: archived note: %w", domain.ErrConflict)
	}
	if note.Status != domain.NoteStatusSubmitted {
		return nil, fmt.Errorf("notes.service.UnlockForOverride: not submitted: %w", domain.ErrConflict)
	}
	if _, err := s.repo.OverrideUnlock(ctx, OverrideUnlockParams{
		ID:         noteID,
		ClinicID:   clinicID,
		UnlockedBy: staffID,
		UnlockedAt: domain.TimeNow(),
		Reason:     r,
	}); err != nil {
		return nil, fmt.Errorf("notes.service.UnlockForOverride: %w", err)
	}
	reasonCopy := r
	s.events.Emit(ctx, NoteEvent{
		NoteID:    noteID,
		SubjectID: note.SubjectID,
		ClinicID:  clinicID,
		EventType: NoteEventOverrideUnlocked,
		Reason:    &reasonCopy,
		ActorID:   staffID,
		ActorRole: staffRole,
	})
	return s.GetNote(ctx, noteID, clinicID)
}

// CanUnlockForOverride reports whether a staff member is authorised to
// re-open the note for correction: the note's original creator OR any
// staff member with manage_staff permission. Routes use this to gate the
// UnlockForOverride call without coupling to the staff package directly.
func (s *Service) CanUnlockForOverride(ctx context.Context, noteID, clinicID, staffID uuid.UUID, hasManageStaff bool) (bool, error) {
	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return false, fmt.Errorf("notes.service.CanUnlockForOverride: %w", err)
	}
	if hasManageStaff {
		return true, nil
	}
	return note.CreatedBy == staffID, nil
}

// GetNotePDFKey returns the S3 storage key for the note's PDF, if generated.
// The handler is responsible for creating a presigned download URL from this key.
func (s *Service) GetNotePDFKey(ctx context.Context, noteID, clinicID uuid.UUID) (string, error) {
	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return "", fmt.Errorf("notes.service.GetNotePDFKey: %w", err)
	}
	if note.PDFStorageKey == nil {
		return "", fmt.Errorf("notes.service.GetNotePDFKey: pdf not ready: %w", domain.ErrNotFound)
	}
	return *note.PDFStorageKey, nil
}

// RetryPDF re-renders the PDF for a submitted note. When [force] is
// false this is the historical recovery path: render only if no key
// exists yet (e.g. the worker exhausted retries). When [force] is true
// the existing key is cleared first, so a backend renderer change
// (Unicode handling, system widget cards, theme tweak) reaches an
// already-rendered submitted note without making the user re-submit.
// Rejects unsubmitted notes with ErrConflict.
func (s *Service) RetryPDF(ctx context.Context, noteID, clinicID uuid.UUID, force bool) (*NoteResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.RetryPDF: %w", err)
	}
	if note.Status != domain.NoteStatusSubmitted {
		return nil, fmt.Errorf("notes.service.RetryPDF: not submitted: %w", domain.ErrConflict)
	}
	if note.PDFStorageKey != nil {
		if !force {
			return s.GetNote(ctx, noteID, clinicID)
		}
		// Force path — null the key so the renderer's idempotence
		// check (note.PDFStorageKey != nil → no-op) doesn't short
		// circuit. The actual artifact in object storage is left in
		// place; the next render overwrites it under the same key.
		if err := s.repo.ClearPDFKey(ctx, noteID, clinicID); err != nil {
			return nil, fmt.Errorf("notes.service.RetryPDF: clear key: %w", err)
		}
	}

	// Prefer a synchronous re-render so the response carries the new key.
	// On failure: enqueue the worker as a recovery path AND surface the error
	// to the caller so the UI can show what went wrong rather than sitting on
	// a stale "PDF not ready" state.
	if s.pdf != nil {
		if rerr := s.pdf.Render(ctx, noteID); rerr != nil {
			slog.Error("notes: sync retry render failed; enqueueing fallback",
				"note_id", noteID.String(), "error", rerr.Error())
			_, _ = s.enqueue.Insert(ctx, GenerateNotePDFArgs{NoteID: noteID}, nil)
			return nil, fmt.Errorf("notes.service.RetryPDF: render: %w", rerr)
		}
	} else {
		if _, err := s.enqueue.Insert(ctx, GenerateNotePDFArgs{NoteID: noteID}, nil); err != nil {
			return nil, fmt.Errorf("notes.service.RetryPDF: enqueue: %w", err)
		}
	}
	return s.GetNote(ctx, noteID, clinicID)
}

// RetryExtraction re-enqueues the AI extraction job for a note whose previous
// extraction failed (status=failed). Clears the prior error and resets status
// to extracting so the worker re-runs cleanly. Rejects notes that are not in
// the failed state with ErrConflict — there's no value in re-running on a
// note that already has fields, and submitted notes must not be perturbed.
func (s *Service) RetryExtraction(ctx context.Context, noteID, clinicID uuid.UUID) (*NoteResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.RetryExtraction: %w", err)
	}
	if note.Status != domain.NoteStatusFailed {
		return nil, fmt.Errorf("notes.service.RetryExtraction: not failed: %w", domain.ErrConflict)
	}
	if _, err := s.repo.UpdateNoteStatus(ctx, noteID, clinicID, domain.NoteStatusExtracting, nil); err != nil {
		return nil, fmt.Errorf("notes.service.RetryExtraction: reset status: %w", err)
	}
	// UniqueOpts ByArgs collapses with any in-flight retry from River's
	// own backoff path so the user's manual click and the background retry
	// can't race two extractions for the same note. The user-triggered run
	// wakes the existing job rather than spawning a parallel one.
	opts := &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
	if _, err := s.enqueue.Insert(ctx, ExtractNoteArgs{NoteID: noteID}, opts); err != nil {
		return nil, fmt.Errorf("notes.service.RetryExtraction: enqueue: %w", err)
	}
	return s.GetNote(ctx, noteID, clinicID)
}

// ── Policy check ─────────────────────────────────────────────────────────────

// NoteClauseCheckResponse is a single clause result in the policy check response.
//
//nolint:revive
type NoteClauseCheckResponse struct {
	BlockID   string `json:"block_id"`
	Status    string `json:"status"` // "satisfied" | "violated"
	Reasoning string `json:"reasoning"`
	Parity    string `json:"parity"` // "high" | "medium" | "low"
}

// NotePolicyCheckResponse is the full policy check response for a note.
//
//nolint:revive
type NotePolicyCheckResponse struct {
	NoteID  string                    `json:"note_id"`
	Results []NoteClauseCheckResponse `json:"results"`
	Blocked bool                      `json:"blocked"` // true if any high-parity clause is violated
}

// CheckPolicy runs a per-clause policy compliance check on a note.
// Stores results as JSONB on the note for later retrieval and submit-time validation.
func (s *Service) CheckPolicy(ctx context.Context, noteID, clinicID uuid.UUID) (*NotePolicyCheckResponse, error) {
	if s.policyChecker == nil || s.policyClauses == nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: policy checker not configured: %w", domain.ErrConflict)
	}

	note, err := s.repo.GetNoteByID(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: get note: %w", err)
	}

	// Get policy clauses for this form version.
	clauses, err := s.policyClauses.GetClausesForNote(ctx, note.FormVersionID, note.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: get clauses: %w", err)
	}
	if len(clauses) == 0 {
		return &NotePolicyCheckResponse{
			NoteID:  noteID.String(),
			Results: []NoteClauseCheckResponse{},
		}, nil
	}

	// Build note content from field values.
	fields, err := s.repo.GetNoteFields(ctx, noteID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: get fields: %w", err)
	}
	noteContent := buildNoteContent(fields)

	// Convert to extraction types.
	extClauses := make([]extraction.PolicyClause, len(clauses))
	for i, c := range clauses {
		extClauses[i] = extraction.PolicyClause{
			BlockID: c.BlockID,
			Title:   c.Title,
			Parity:  c.Parity,
		}
	}

	vertical := ""
	if s.verticals != nil {
		v, vErr := s.verticals.GetClinicVertical(ctx, clinicID)
		if vErr == nil {
			vertical = v
		}
	}

	results, err := s.policyChecker.CheckPolicyClauses(ctx, vertical, noteContent, extClauses)
	if err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: check: %w", err)
	}

	// Store results as JSONB.
	resultJSON, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: marshal: %w", err)
	}
	if err := s.repo.UpdatePolicyCheckResult(ctx, noteID, clinicID, string(resultJSON)); err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: store: %w", err)
	}

	// Append to history — non-fatal; a history write failure must not block the check response.
	if err := s.repo.InsertPolicyCheck(ctx, noteID, clinicID, string(resultJSON)); err != nil {
		_ = err
	}

	// Build response.
	resp := &NotePolicyCheckResponse{
		NoteID:  noteID.String(),
		Results: make([]NoteClauseCheckResponse, len(results)),
	}
	for i, r := range results {
		resp.Results[i] = NoteClauseCheckResponse{
			BlockID:   r.BlockID,
			Status:    r.Status,
			Reasoning: r.Reasoning,
			Parity:    r.Parity,
		}
		if r.Parity == "high" && r.Status == "violated" {
			resp.Blocked = true
		}
	}

	return resp, nil
}

// PolicyCheckRun is one historical policy check run returned to the API.
type PolicyCheckRun struct {
	ID        string                    `json:"id"`
	Results   []NoteClauseCheckResponse `json:"results"`
	Blocked   bool                      `json:"blocked"`
	CheckedAt time.Time                 `json:"checked_at"`
}

// ListPolicyChecks returns all historical policy check runs for a note, newest first.
func (s *Service) ListPolicyChecks(ctx context.Context, noteID, clinicID uuid.UUID) ([]PolicyCheckRun, error) {
	if _, err := s.repo.GetNoteByID(ctx, noteID, clinicID); err != nil {
		return nil, fmt.Errorf("notes.service.ListPolicyChecks: get note: %w", err)
	}
	records, err := s.repo.ListPolicyChecks(ctx, noteID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.ListPolicyChecks: list: %w", err)
	}
	runs := make([]PolicyCheckRun, 0, len(records))
	for _, rec := range records {
		var clauseResults []extraction.ClauseCheckResult
		if err := json.Unmarshal([]byte(rec.Result), &clauseResults); err != nil {
			continue // skip malformed rows
		}
		blocked := false
		apiResults := make([]NoteClauseCheckResponse, len(clauseResults))
		for i, r := range clauseResults {
			apiResults[i] = NoteClauseCheckResponse{
				BlockID:   r.BlockID,
				Status:    r.Status,
				Parity:    r.Parity,
				Reasoning: r.Reasoning,
			}
			if r.Parity == "high" && r.Status == "violated" {
				blocked = true
			}
		}
		runs = append(runs, PolicyCheckRun{
			ID:        rec.ID.String(),
			Results:   apiResults,
			Blocked:   blocked,
			CheckedAt: rec.CheckedAt,
		})
	}
	return runs, nil
}

// buildNoteContent creates a plain-text summary of note field values for policy checking.
func buildNoteContent(fields []*NoteFieldRecord) string {
	var sb strings.Builder
	for _, f := range fields {
		if f.Value != nil && *f.Value != "" && *f.Value != "null" {
			sb.WriteString(f.FieldID.String())
			sb.WriteString(": ")
			sb.WriteString(*f.Value)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// validatePolicyCheck checks stored policy check results for high-parity violations.
// Returns domain.ErrValidation if submission should be blocked.
func (s *Service) validatePolicyCheck(note *NoteRecord) error {
	if note.PolicyCheckResult == nil {
		return nil // no check run yet — allow submit
	}

	var results []extraction.ClauseCheckResult
	if err := json.Unmarshal([]byte(*note.PolicyCheckResult), &results); err != nil {
		return nil // malformed results — don't block
	}

	var violations []string
	for _, r := range results {
		if r.Parity == "high" && r.Status == "violated" {
			violations = append(violations, r.BlockID)
		}
	}

	if len(violations) > 0 {
		return fmt.Errorf("notes.service.SubmitNote: high-parity policy violations: %s: %w",
			strings.Join(violations, ", "), domain.ErrValidation)
	}
	return nil
}

// validateForSubmission checks that every required form field has a non-null value.
// Returns domain.ErrValidation listing the missing field titles on failure.
func (s *Service) validateForSubmission(ctx context.Context, formVersionID uuid.UUID, noteID uuid.UUID) error {
	formFields, err := s.fields.GetFieldsByVersionID(ctx, formVersionID)
	if err != nil {
		return fmt.Errorf("notes.service.validateForSubmission: get fields: %w", err)
	}

	noteFields, err := s.repo.GetNoteFields(ctx, noteID)
	if err != nil {
		return fmt.Errorf("notes.service.validateForSubmission: get note fields: %w", err)
	}

	// Index note field values by field ID.
	valueByFieldID := make(map[uuid.UUID]*string, len(noteFields))
	for _, nf := range noteFields {
		valueByFieldID[nf.FieldID] = nf.Value
	}

	var missing []string
	for _, ff := range formFields {
		if !ff.Required {
			continue
		}
		val, exists := valueByFieldID[ff.ID]
		if !exists || val == nil || *val == "" || *val == "null" {
			missing = append(missing, ff.Title)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("notes.service.SubmitNote: missing required fields: %s: %w",
			strings.Join(missing, ", "), domain.ErrValidation)
	}

	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func clampLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func toNoteResponse(n *NoteRecord, fields []*NoteFieldRecord) *NoteResponse {
	r := &NoteResponse{
		ID:            n.ID.String(),
		ClinicID:      n.ClinicID.String(),
		FormVersionID: n.FormVersionID.String(),
		CreatedBy:     n.CreatedBy.String(),
		Status:        n.Status,
		ErrorMessage:  n.ErrorMessage,
		CreatedAt:     n.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     n.UpdatedAt.Format(time.RFC3339),
	}
	if n.RecordingID != nil {
		s := n.RecordingID.String()
		r.RecordingID = &s
	}
	if n.SubjectID != nil {
		s := n.SubjectID.String()
		r.SubjectID = &s
	}
	if n.ReviewedBy != nil {
		s := n.ReviewedBy.String()
		r.ReviewedBy = &s
	}
	if n.ReviewedAt != nil {
		s := n.ReviewedAt.Format(time.RFC3339)
		r.ReviewedAt = &s
	}
	if n.SubmittedAt != nil {
		s := n.SubmittedAt.Format(time.RFC3339)
		r.SubmittedAt = &s
	}
	if n.SubmittedBy != nil {
		s := n.SubmittedBy.String()
		r.SubmittedBy = &s
	}
	if n.ArchivedAt != nil {
		s := n.ArchivedAt.Format(time.RFC3339)
		r.ArchivedAt = &s
	}
	r.FormVersionContext = n.FormVersionContext
	r.FormVersionLabel = n.FormVersionLabel
	r.PolicyAlignmentPct = n.PolicyAlignmentPct
	r.OverrideReason = n.OverrideReason
	if n.OverrideBy != nil {
		s := n.OverrideBy.String()
		r.OverrideBy = &s
	}
	if n.OverrideAt != nil {
		s := n.OverrideAt.Format(time.RFC3339)
		r.OverrideAt = &s
	}
	if n.OverrideUnlockedAt != nil {
		s := n.OverrideUnlockedAt.Format(time.RFC3339)
		r.OverrideUnlockedAt = &s
	}
	if n.OverrideUnlockedBy != nil {
		s := n.OverrideUnlockedBy.String()
		r.OverrideUnlockedBy = &s
	}
	r.OverrideUnlockedReason = n.OverrideUnlockedReason
	r.OverrideCount = n.OverrideCount
	r.PDFStorageKey = n.PDFStorageKey
	if fields != nil {
		r.Fields = make([]*NoteFieldResponse, len(fields))
		for i, f := range fields {
			r.Fields[i] = toFieldResponse(f)
		}
	}
	return r
}

func toFieldResponse(f *NoteFieldRecord) *NoteFieldResponse {
	r := &NoteFieldResponse{
		FieldID:            f.FieldID.String(),
		Value:              f.Value,
		Confidence:         f.Confidence,
		SourceQuote:        f.SourceQuote,
		TransformationType: f.TransformationType,
	}
	if f.OverriddenBy != nil {
		s := f.OverriddenBy.String()
		r.OverriddenBy = &s
	}
	if f.OverriddenAt != nil {
		s := f.OverriddenAt.Format(time.RFC3339)
		r.OverriddenAt = &s
	}
	return r
}
