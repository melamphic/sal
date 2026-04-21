package notes

import (
	"context"
	"encoding/json"
	"fmt"
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

// Service handles business logic for the notes module.
type Service struct {
	repo          repo
	enqueue       jobEnqueuer
	events        EventEmitter
	fields        FormFieldProvider                // nil = skip field validation on submit
	policyChecker extraction.PolicyDetailedChecker // nil = skip policy check
	policyClauses PolicyClauseProvider             // nil = skip policy check
	verticals     VerticalProvider                 // nil = generic (vertical-neutral) prompts
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
	PolicyAlignmentPct *float64             `json:"policy_alignment_pct,omitempty"`
	// OverrideReason/By/At are populated when the submitter overrode a
	// high-parity policy violation at submit time.
	OverrideReason     *string              `json:"override_reason,omitempty"`
	OverrideBy         *string              `json:"override_by,omitempty"`
	OverrideAt         *string              `json:"override_at,omitempty"`
	PDFStorageKey      *string              `json:"pdf_storage_key,omitempty"`
	CreatedAt          string               `json:"created_at"`
	UpdatedAt          string               `json:"updated_at"`
	Fields             []*NoteFieldResponse `json:"fields,omitempty"`
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

	if !input.SkipExtraction {
		if _, err := s.enqueue.Insert(ctx, ExtractNoteArgs{NoteID: noteID}, nil); err != nil {
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

	return toNoteResponse(note, fields), nil
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
// Only allowed when the note is in 'draft' status.
func (s *Service) UpdateField(ctx context.Context, input UpdateFieldInput) (*NoteFieldResponse, error) {
	note, err := s.repo.GetNoteByID(ctx, input.NoteID, input.ClinicID)
	if err != nil {
		return nil, fmt.Errorf("notes.service.UpdateField: %w", err)
	}
	if note.Status != domain.NoteStatusDraft {
		return nil, fmt.Errorf("notes.service.UpdateField: note not in draft: %w", domain.ErrConflict)
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

	// Pre-submit validation: required fields + policy check (unless overridden).
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

	s.events.Emit(ctx, NoteEvent{
		NoteID:    noteID,
		SubjectID: note.SubjectID,
		ClinicID:  clinicID,
		EventType: NoteEventSubmitted,
		ActorID:   staffID,
		ActorRole: staffRole,
	})

	// Best-effort: recompute alignment against submitted field values.
	_, _ = s.enqueue.Insert(ctx, ComputePolicyAlignmentArgs{NoteID: noteID}, nil)

	// Best-effort: generate branded PDF asynchronously.
	_, _ = s.enqueue.Insert(ctx, GenerateNotePDFArgs{NoteID: noteID}, nil)

	return toNoteResponse(note, nil), nil
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

	archived, err := s.repo.ArchiveNote(ctx, ArchiveNoteParams{
		ID:         noteID,
		ClinicID:   clinicID,
		ArchivedAt: domain.TimeNow(),
	})
	if err != nil {
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

	return toNoteResponse(archived, nil), nil
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
	clauses, err := s.policyClauses.GetClausesForNote(ctx, note.FormVersionID)
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
	if err := s.repo.UpdatePolicyCheckResult(ctx, noteID, string(resultJSON)); err != nil {
		return nil, fmt.Errorf("notes.service.CheckPolicy: store: %w", err)
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
