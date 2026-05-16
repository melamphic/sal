package notes

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all notes routes onto the provided Chi router.
// All routes require a valid JWT and the SubmitForms permission.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	submitForms := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.SubmitForms })
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "create-note",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes",
		Summary:     "Create a note",
		Description: "Creates a clinical note by pairing a recording with a published form version. Immediately enqueues the AI extraction job. Set skip_extraction=true for manual notes (no AI). Maximum 3 notes per recording.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.createNote)

	huma.Register(api, huma.Operation{
		OperationID: "list-notes",
		Method:      http.MethodGet,
		Path:        "/api/v1/notes",
		Summary:     "List notes",
		Description: "Returns a paginated list of notes for the clinic. Filter by recording_id, subject_id, or status. Archived notes are excluded by default; set include_archived=true to include them.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.listNotes)

	huma.Register(api, huma.Operation{
		OperationID: "get-note",
		Method:      http.MethodGet,
		Path:        "/api/v1/notes/{note_id}",
		Summary:     "Get a note",
		Description: "Returns the note with all extracted field values, confidence scores, source quotes, and transformation types. Poll this endpoint after creating a note to check extraction progress.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.getNote)

	huma.Register(api, huma.Operation{
		OperationID: "update-note-field",
		Method:      http.MethodPatch,
		Path:        "/api/v1/notes/{note_id}/fields/{field_id}",
		Summary:     "Override a field value",
		Description: "Records a staff override for a single extracted field. The note must be in 'draft' status. Sets overridden_by and overridden_at for audit purposes.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.updateField)

	huma.Register(api, huma.Operation{
		OperationID: "submit-note",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/submit",
		Summary:     "Submit a note",
		Description: "Transitions the note from draft to submitted. Sets reviewed_by and submitted_by to the authenticated staff member. If the linked form version has been decommissioned, form_version_context is set automatically. Re-submit is also accepted from a note that's been re-opened via override-unlock — in that case override_count increments and the timeline records a `note.override_committed` event instead of `note.submitted`.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.submitNote)

	huma.Register(api, huma.Operation{
		OperationID: "unlock-note-override",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/override-unlock",
		Summary:     "Re-open a submitted note for correction",
		Description: "Transitions a submitted note to 'overriding' so its fields can be edited again, then re-submitted. Permission gate: the note's original creator OR any staff member with manage_staff. The required `reason` is persisted on the note and surfaced in the patient timeline as a `note.override_unlocked` event for audit. Returns 409 if the note isn't in 'submitted' status.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.unlockNoteOverride)

	huma.Register(api, huma.Operation{
		OperationID: "check-note-policy",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/check-policy",
		Summary:     "Check policy compliance",
		Description: "Runs a per-clause policy compliance check on the note. Returns pass/fail status and reasoning for each linked policy clause. High-parity violations block submission. Results are stored on the note for submit-time validation.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.checkPolicy)

	huma.Register(api, huma.Operation{
		OperationID: "get-note-pdf",
		Method:      http.MethodGet,
		Path:        "/api/v1/notes/{note_id}/pdf",
		Summary:     "Get note PDF",
		Description: "Returns a pre-signed download URL for the note's branded PDF. The PDF is generated asynchronously after submission. Returns 404 if the PDF is not yet ready.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.getNotePDF)

	huma.Register(api, huma.Operation{
		OperationID: "retry-note-pdf",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/retry-pdf",
		Summary:     "Retry note PDF render",
		Description: "Re-enqueues the PDF generation job for a submitted note whose PDF has not been produced yet (River job exhausted retries or never ran). Idempotent — returns success when the PDF is already ready.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.retryNotePDF)

	huma.Register(api, huma.Operation{
		OperationID: "retry-note-extraction",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/retry-extraction",
		Summary:     "Retry note extraction",
		Description: "Re-enqueues the AI extraction job for a note in the failed state (e.g. extractor returned a transient 5xx). Resets status to extracting and clears the prior error. Returns 409 if the note is not in the failed state.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.retryNoteExtraction)

	huma.Register(api, huma.Operation{
		OperationID: "archive-note",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/archive",
		Summary:     "Archive a note",
		Description: "Soft-deletes a note. Archived notes are excluded from list results unless include_archived=true is set. Notes cannot be hard-deleted.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.archiveNote)

	huma.Register(api, huma.Operation{
		OperationID: "upload-note-attachment",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/upload-attachment",
		Summary:     "Upload a photo/document to a note",
		Description: "Multipart upload of a single image (png/jpeg/webp/heic) or PDF, max 8 MiB. The attachment is persisted in storage with a re-signable key; the response carries a short-lived signed URL the FE can render immediately. Permission: submit_forms.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.uploadNoteAttachment)

	huma.Register(api, huma.Operation{
		OperationID: "list-note-attachments",
		Method:      http.MethodGet,
		Path:        "/api/v1/notes/{note_id}/attachments",
		Summary:     "List a note's attachments",
		Description: "Returns the active (non-archived) attachments for the note, newest-first. Each summary carries a freshly signed download URL. Most callers read attachments via GetNote which embeds the same list; this endpoint exists for re-polling after a delete without the rest of the note payload.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.listNoteAttachments)

	huma.Register(api, huma.Operation{
		OperationID: "delete-note-attachment",
		Method:      http.MethodDelete,
		Path:        "/api/v1/notes/{note_id}/attachments/{attachment_id}",
		Summary:     "Archive a note attachment",
		Description: "Soft-deletes a single attachment. Permission rule: the original uploader can always delete; any other staff must hold manage_staff. The row stays in the DB for audit retention but is hidden from subsequent list calls.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.deleteNoteAttachment)

	// System widget materialisation: writes to the typed compliance
	// ledgers (consent_records, drug_operations_log, incident_events,
	// pain_scores) happen at note Submit, not when the user taps Confirm
	// on the widget. Confirm just PATCHes a structured payload into
	// note_fields.value via the standard update-note-field endpoint
	// above; Submit walks every system.* field, parses the payload, and
	// calls the entity service. Direct-from-patient creates use the
	// standalone POST endpoints on /api/v1/{drugs,incidents,consent,
	// pain-scores} and are unaffected.
}
