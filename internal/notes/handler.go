package notes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
	"github.com/melamphic/sal/internal/platform/storage"
)

// Handler wires notes HTTP endpoints to the Service.
type Handler struct {
	svc   *Service
	store *storage.Store
}

// NewHandler creates a new notes Handler.
func NewHandler(svc *Service, store *storage.Store) *Handler {
	return &Handler{svc: svc, store: store}
}

// ── Shared input types ────────────────────────────────────────────────────────

type noteIDInput struct {
	NoteID string `path:"note_id" doc:"The note's UUID."`
}

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0"   doc:"Number of results to skip."`
}

// ── Notes ─────────────────────────────────────────────────────────────────────

type createNoteBodyInput struct {
	Body struct {
		RecordingID    *string `json:"recording_id,omitempty"   doc:"UUID of the recording to fill from. Omit for manual notes."`
		FormVersionID  string  `json:"form_version_id"          doc:"UUID of the published form version to use."`
		SubjectID      *string `json:"subject_id,omitempty"     doc:"Optional patient UUID."`
		SkipExtraction bool    `json:"skip_extraction,omitempty" doc:"If true, creates a manual note without AI extraction. recording_id may be omitted."`
	}
}

type noteHTTPResponse struct {
	Body *NoteResponse
}

type noteListHTTPResponse struct {
	Body *NoteListResponse
}

type listNotesInput struct {
	paginationInput
	RecordingID     string `query:"recording_id"     doc:"Filter notes by recording UUID."`
	SubjectID       string `query:"subject_id"       doc:"Filter notes by patient UUID."`
	Status          string `query:"status"           enum:"extracting,draft,submitted,failed" doc:"Filter by status."`
	IncludeArchived bool   `query:"include_archived" doc:"Include archived notes in results. Default false."`
}

// createNote handles POST /api/v1/notes.
func (h *Handler) createNote(ctx context.Context, input *createNoteBodyInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	// recording_id required unless skip_extraction is set.
	if !input.Body.SkipExtraction && input.Body.RecordingID == nil {
		return nil, huma.Error400BadRequest("recording_id is required when skip_extraction is false")
	}

	formVersionID, err := uuid.Parse(input.Body.FormVersionID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid form_version_id")
	}

	svcInput := CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		ActorRole:      role,
		FormVersionID:  formVersionID,
		SkipExtraction: input.Body.SkipExtraction,
	}

	if input.Body.RecordingID != nil {
		id, err := uuid.Parse(*input.Body.RecordingID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid recording_id")
		}
		svcInput.RecordingID = &id
	}

	if input.Body.SubjectID != nil {
		id, err := uuid.Parse(*input.Body.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		svcInput.SubjectID = &id
	}

	resp, err := h.svc.CreateNote(ctx, svcInput)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// getNote handles GET /api/v1/notes/{note_id}.
func (h *Handler) getNote(ctx context.Context, input *noteIDInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.GetNote(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// listNotes handles GET /api/v1/notes.
func (h *Handler) listNotes(ctx context.Context, input *listNotesInput) (*noteListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	svcInput := ListNotesInput{
		Limit:           input.Limit,
		Offset:          input.Offset,
		IncludeArchived: input.IncludeArchived,
	}
	if input.RecordingID != "" {
		id, err := uuid.Parse(input.RecordingID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid recording_id")
		}
		svcInput.RecordingID = &id
	}
	if input.SubjectID != "" {
		id, err := uuid.Parse(input.SubjectID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid subject_id")
		}
		svcInput.SubjectID = &id
	}
	if input.Status != "" {
		s := domain.NoteStatus(input.Status)
		svcInput.Status = &s
	}

	resp, err := h.svc.ListNotes(ctx, clinicID, svcInput)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteListHTTPResponse{Body: resp}, nil
}

// ── Field update ──────────────────────────────────────────────────────────────

type updateFieldBodyInput struct {
	NoteID  string `path:"note_id"`
	FieldID string `path:"field_id"`
	Body    struct {
		Value *string `json:"value" doc:"JSON-encoded new value. Send null to clear."`
	}
}

type fieldHTTPResponse struct {
	Body *NoteFieldResponse
}

// updateField handles PATCH /api/v1/notes/{note_id}/fields/{field_id}.
func (h *Handler) updateField(ctx context.Context, input *updateFieldBodyInput) (*fieldHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}
	fieldID, err := uuid.Parse(input.FieldID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid field_id")
	}

	resp, err := h.svc.UpdateField(ctx, UpdateFieldInput{
		NoteID:    noteID,
		ClinicID:  clinicID,
		StaffID:   staffID,
		ActorRole: role,
		FieldID:   fieldID,
		Value:     input.Body.Value,
	})
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &fieldHTTPResponse{Body: resp}, nil
}

// ── Submit ────────────────────────────────────────────────────────────────────

// submitNoteInput carries the note ID in the path and an optional override
// justification in the body. When override_reason is present and non-blank the
// server skips the high-parity policy gate and persists the justification.
type submitNoteInput struct {
	NoteID string `path:"note_id" doc:"The note's UUID."`
	Body   struct {
		OverrideReason *string `json:"override_reason,omitempty" doc:"Written justification for submitting despite a high-parity policy violation. Persisted on the note when present."`
	}
}

// submitNote handles POST /api/v1/notes/{note_id}/submit.
func (h *Handler) submitNote(ctx context.Context, input *submitNoteInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.SubmitNote(ctx, noteID, clinicID, staffID, role, input.Body.OverrideReason)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// ── Override unlock ──────────────────────────────────────────────────────────

// unlockOverrideInput carries the note ID and the required justification for
// re-opening a submitted note.
type unlockOverrideInput struct {
	NoteID string `path:"note_id" doc:"The note's UUID."`
	Body   struct {
		Reason string `json:"reason" minLength:"10" doc:"Written justification for re-opening this submitted note. Persisted on the note for audit and surfaced in the patient timeline."`
	}
}

// unlockNoteOverride handles POST /api/v1/notes/{note_id}/override-unlock.
// Allowed for the note's original creator OR any staff with manage_staff.
func (h *Handler) unlockNoteOverride(ctx context.Context, input *unlockOverrideInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))
	perms := mw.PermissionsFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	allowed, err := h.svc.CanUnlockForOverride(ctx, noteID, clinicID, staffID, perms.ManageStaff)
	if err != nil {
		return nil, mapNoteError(err)
	}
	if !allowed {
		return nil, huma.Error403Forbidden("only the note creator or a staff manager can unlock this note for override")
	}

	resp, err := h.svc.UnlockForOverride(ctx, noteID, clinicID, staffID, role, input.Body.Reason)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// ── Policy check ─────────────────────────────────────────────────────────────

type policyCheckHTTPResponse struct {
	Body *NotePolicyCheckResponse
}

// checkPolicy handles POST /api/v1/notes/{note_id}/check-policy.
func (h *Handler) checkPolicy(ctx context.Context, input *noteIDInput) (*policyCheckHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.CheckPolicy(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &policyCheckHTTPResponse{Body: resp}, nil
}

// ── Archive ───────────────────────────────────────────────────────────────────

// archiveNote handles POST /api/v1/notes/{note_id}/archive.
func (h *Handler) archiveNote(ctx context.Context, input *noteIDInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.ArchiveNote(ctx, noteID, clinicID, staffID, role)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// ── PDF download ─────────────────────────────────────────────────────────────

type notePDFHTTPResponse struct {
	Body struct {
		URL string `json:"url" doc:"Pre-signed download URL for the note PDF. Expires in 1 hour."`
	}
}

// getNotePDF handles GET /api/v1/notes/{note_id}/pdf.
func (h *Handler) getNotePDF(ctx context.Context, input *noteIDInput) (*notePDFHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	key, err := h.svc.GetNotePDFKey(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}

	url, err := h.store.PresignDownload(ctx, key, 1*time.Hour)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to generate download URL")
	}

	resp := &notePDFHTTPResponse{}
	resp.Body.URL = url
	return resp, nil
}

type retryPDFInput struct {
	NoteID string `path:"note_id" doc:"The note's UUID."`
	Force  bool   `query:"force" doc:"Set true to clear the existing pdf_storage_key and re-render from scratch — used by the in-app refresh button so backend renderer fixes (Unicode, system widget cards, theme tweaks) reach already-rendered notes."`
}

// retryNotePDF handles POST /api/v1/notes/{note_id}/retry-pdf.
// PDF render is the most opaque failure mode in the notes flow — fpdf
// build errors, MinIO upload errors, doc-theme JSON parse errors, system
// widget summarisation errors all bubble up here. The default
// `mapNoteError` collapses these to a generic 500 "internal server
// error" body, which is useless for the operator. Surface the leaf
// message instead so the UI can show "PDF render failed: {actual
// reason}" and the user can tell us what's broken without grepping
// container logs.
func (h *Handler) retryNotePDF(ctx context.Context, input *retryPDFInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.RetryPDF(ctx, noteID, clinicID, input.Force)
	if err != nil {
		return nil, mapPDFError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// mapPDFError surfaces the leaf message from PDF render failures so the
// UI can show the actual cause. Sentinel errors still take their normal
// 4xx mapping; everything else returns 500 with the leaf message in the
// body (vs. an opaque "internal server error").
func mapPDFError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(leafMessage(err))
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("PDF not ready")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict(leafMessage(err))
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		slog.Error("notes: pdf render error", "error", err.Error())
		return huma.Error500InternalServerError(
			"pdf render failed: " + leafMessage(err),
		)
	}
}

// retryNoteExtraction handles POST /api/v1/notes/{note_id}/retry-extraction.
func (h *Handler) retryNoteExtraction(ctx context.Context, input *noteIDInput) (*noteHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	resp, err := h.svc.RetryExtraction(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &noteHTTPResponse{Body: resp}, nil
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func mapNoteError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(leafMessage(err))
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("operation not allowed in current state")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	default:
		slog.Error("notes: unmapped service error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

// leafMessage extracts the cause text from a wrapped error chain, strip-
// ping the "pkg.layer.func: " prefixes added by fmt.Errorf("...: %w").
// The full chain stays in logs (via slog) — this is purely the
// user-facing string returned in the HTTP error body.
//
// Example: "notes.service.materialiseTyped: app.drugOpAdapter: drugs.
// service.LogOperation: witness required for controlled drug: validation
// error" → "witness required for controlled drug".
func leafMessage(err error) string {
	msg := err.Error()
	// Drop trailing sentinel — domain.ErrValidation prints "validation error"
	// after the last colon. Keep what comes before it.
	const sentinel = ": validation error"
	if idx := strings.LastIndex(msg, sentinel); idx > 0 {
		msg = msg[:idx]
	}
	// Take everything after the last "<pkg>.<layer>.<func>: " prefix.
	// The shape is `lower.lower.lower: ` or `lower.lower: ` — at least one dot.
	// We split on ": " and walk forward until a segment doesn't look like
	// a wrapper prefix.
	parts := strings.Split(msg, ": ")
	for i := 0; i < len(parts)-1; i++ {
		p := parts[i]
		if !looksLikeWrapPrefix(p) {
			return strings.Join(parts[i:], ": ")
		}
	}
	return parts[len(parts)-1]
}

// looksLikeWrapPrefix returns true for tokens that look like Go package
// path qualifiers (e.g. "notes.service.materialiseTyped"). Used to
// decide where the user-facing portion of an error chain starts.
func looksLikeWrapPrefix(s string) bool {
	if s == "" || strings.ContainsAny(s, " ") {
		return false
	}
	return strings.Contains(s, ".")
}

// ── Note attachment upload ────────────────────────────────────────────────────
//
// Mirrors POST /api/v1/patients/upload-photo: multipart/form-data,
// single "file" field, 8 MiB cap (larger than patient photos since
// clinical photos can be higher-resolution wound shots). Allowed types:
// images + PDF. Returns the freshly-built summary so the FE can append
// to its in-memory list without a follow-up GetNote.

const maxNoteAttachmentBytes int64 = 8 << 20

type uploadNoteAttachmentInput struct {
	NoteID  string `path:"note_id" doc:"The note's UUID."`
	RawBody multipart.Form
}

type uploadNoteAttachmentResponse struct {
	Body *NoteAttachmentSummary
}

func isAllowedNoteAttachmentType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/heic",
		"application/pdf":
		return true
	}
	return false
}

// uploadNoteAttachment handles POST /api/v1/notes/{note_id}/upload-attachment.
func (h *Handler) uploadNoteAttachment(ctx context.Context, input *uploadNoteAttachmentInput) (*uploadNoteAttachmentResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}

	files := input.RawBody.File["file"]
	if len(files) == 0 {
		return nil, huma.Error400BadRequest("missing form field \"file\"")
	}
	hdr := files[0]
	if hdr.Size > maxNoteAttachmentBytes {
		return nil, huma.Error400BadRequest(fmt.Sprintf("attachment too large (max %d bytes)", maxNoteAttachmentBytes))
	}

	contentType := hdr.Header.Get("Content-Type")
	if !isAllowedNoteAttachmentType(contentType) {
		return nil, huma.Error415UnsupportedMediaType("attachment must be png, jpeg, webp, heic or pdf")
	}

	f, err := hdr.Open()
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read uploaded file")
	}
	defer func() { _ = f.Close() }()

	summary, err := h.svc.UploadNoteAttachment(ctx, noteID, clinicID, staffID, contentType, f, hdr.Size)
	if err != nil {
		return nil, mapNoteError(err)
	}
	return &uploadNoteAttachmentResponse{Body: summary}, nil
}

// ── Note attachment list + delete ────────────────────────────────────────────

type listNoteAttachmentsResponse struct {
	Body *struct {
		Items []*NoteAttachmentSummary `json:"items"`
	}
}

// listNoteAttachments handles GET /api/v1/notes/{note_id}/attachments.
// Most callers read attachments via GetNote (which embeds them), but
// the dedicated endpoint exists for re-polling without the rest of the
// note payload (e.g. after a delete).
func (h *Handler) listNoteAttachments(ctx context.Context, input *noteIDInput) (*listNoteAttachmentsResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}
	items, err := h.svc.ListNoteAttachments(ctx, noteID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}
	out := &listNoteAttachmentsResponse{}
	out.Body = &struct {
		Items []*NoteAttachmentSummary `json:"items"`
	}{Items: items}
	return out, nil
}

type deleteNoteAttachmentInput struct {
	NoteID       string `path:"note_id"       doc:"The note's UUID."`
	AttachmentID string `path:"attachment_id" doc:"The attachment's UUID."`
}

type deleteNoteAttachmentResponse struct {
	Body *struct {
		Archived bool `json:"archived"`
	}
}

// deleteNoteAttachment handles DELETE /api/v1/notes/{note_id}/attachments/{attachment_id}.
// Permission rule: pre-submit, the uploader can delete; post-submit
// (or for any other uploader's row) the caller must hold manageNotes —
// matched against the staff's permission set. Note that
// SubmittedAt-presence is the gate, not Status alone, because a
// note can transition into 'overriding' after submit and still has
// SubmittedAt set.
func (h *Handler) deleteNoteAttachment(ctx context.Context, input *deleteNoteAttachmentInput) (*deleteNoteAttachmentResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	noteID, err := uuid.Parse(input.NoteID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid note_id")
	}
	attachmentID, err := uuid.Parse(input.AttachmentID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid attachment_id")
	}

	// Permission gate. The handler does the check, not the service,
	// because perms are HTTP-layer concerns (manageNotes vs uploader).
	rec, err := h.svc.GetNoteAttachment(ctx, attachmentID, clinicID)
	if err != nil {
		return nil, mapNoteError(err)
	}
	if rec.NoteID != noteID {
		// Don't leak existence across notes.
		return nil, huma.Error404NotFound("attachment not found")
	}

	perms := mw.PermissionsFromContext(ctx)
	if rec.UploadedBy != staffID && !perms.ManageStaff {
		// Other people's attachments require admin perms.
		return nil, huma.Error403Forbidden("only the uploader or an admin can delete this attachment")
	}

	if err := h.svc.ArchiveNoteAttachment(ctx, attachmentID, clinicID); err != nil {
		return nil, mapNoteError(err)
	}
	out := &deleteNoteAttachmentResponse{}
	out.Body = &struct {
		Archived bool `json:"archived"`
	}{Archived: true}
	return out, nil
}
