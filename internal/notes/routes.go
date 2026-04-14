package notes

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all notes routes onto the provided Chi router.
// All routes require a valid JWT. Creating/reviewing notes requires SubmitForms.
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	authMw := mw.Authenticate(jwtSecret)
	submitForms := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.SubmitForms })
	security := []map[string][]string{{"bearerAuth": {}}}

	r.Group(func(r chi.Router) {
		r.Use(authMw)

		huma.Register(api, huma.Operation{
			OperationID: "create-note",
			Method:      http.MethodPost,
			Path:        "/api/v1/notes",
			Summary:     "Create a note",
			Description: "Creates a clinical note by pairing a recording with a published form version. Immediately enqueues the AI extraction job. Maximum 3 notes per recording.",
			Tags:        []string{"Notes"},
			Security:    security,
			Middlewares: huma.Middlewares{submitForms},
		}, h.createNote)

		huma.Register(api, huma.Operation{
			OperationID: "list-notes",
			Method:      http.MethodGet,
			Path:        "/api/v1/notes",
			Summary:     "List notes",
			Description: "Returns a paginated list of notes for the clinic. Filter by recording_id, subject_id, or status.",
			Tags:        []string{"Notes"},
			Security:    security,
			Middlewares: huma.Middlewares{submitForms},
		}, h.listNotes)

		huma.Register(api, huma.Operation{
			OperationID: "get-note",
			Method:      http.MethodGet,
			Path:        "/api/v1/notes/{note_id}",
			Summary:     "Get a note",
			Description: "Returns the note with all extracted field values, confidence scores, and source quotes. Poll this endpoint after creating a note to check extraction progress.",
			Tags:        []string{"Notes"},
			Security:    security,
			Middlewares: huma.Middlewares{submitForms},
		}, h.getNote)

		huma.Register(api, huma.Operation{
			OperationID: "update-note-field",
			Method:      http.MethodPatch,
			Path:        "/api/v1/notes/{note_id}/fields/{field_id}",
			Summary:     "Override a field value",
			Description: "Records a staff override for a single extracted field. The note must be in 'draft' status. Sets overridden_by and overridden_at for audit purposes.",
			Tags:        []string{"Notes"},
			Security:    security,
			Middlewares: huma.Middlewares{submitForms},
		}, h.updateField)

		huma.Register(api, huma.Operation{
			OperationID: "submit-note",
			Method:      http.MethodPost,
			Path:        "/api/v1/notes/{note_id}/submit",
			Summary:     "Submit a note",
			Description: "Transitions the note from draft to submitted. The note is locked after submission. Policy evaluation and timeline writing will be added in Phase 2.",
			Tags:        []string{"Notes"},
			Security:    security,
			Middlewares: huma.Middlewares{submitForms},
		}, h.submitNote)
	})
}
