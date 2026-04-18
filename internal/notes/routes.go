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
		Description: "Transitions the note from draft to submitted. Sets reviewed_by and submitted_by to the authenticated staff member. If the linked form version has been decommissioned, form_version_context is set automatically.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.submitNote)

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
		OperationID: "archive-note",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/archive",
		Summary:     "Archive a note",
		Description: "Soft-deletes a note. Archived notes are excluded from list results unless include_archived=true is set. Notes cannot be hard-deleted.",
		Tags:        []string{"Notes"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, submitForms},
	}, h.archiveNote)
}
