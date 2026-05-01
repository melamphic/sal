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
	managePatients := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients
	})
	dispense := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.Dispense
	})
	managePatientsOrDispense := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients || p.Dispense
	})
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

	// ── System widget materialisation ───────────────────────────────────
	// Each system.* form field can carry an AI-extracted JSON payload
	// in note_fields.value. The clinician reviews the typed card and
	// taps Confirm — that POST hits one of the four endpoints below.
	// The endpoint is per-field-type so:
	//   - permissions are precise (Dispense for drug op, ManagePatients
	//     for consent / incident, either for pain)
	//   - request bodies are typed (no any-of dispatch)
	//   - regulator-binding rails (drug witness, incident classifier,
	//     consent expiry default) live in the right service
	// All four are idempotent — calling on an already-materialised
	// field returns the existing entity reference.

	huma.Register(api, huma.Operation{
		OperationID: "materialise-consent-field",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/fields/{field_id}/materialise-consent",
		Summary:     "Materialise a system.consent field",
		Description: "Creates a consent_records row for a system.consent form field and writes the id-pointer into note_fields.value. Idempotent.",
		Tags:        []string{"Notes", "Consent"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.materialiseConsent)

	huma.Register(api, huma.Operation{
		OperationID: "materialise-drug-op-field",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/fields/{field_id}/materialise-drug-op",
		Summary:     "Confirm and log a system.drug_op field",
		Description: "Creates a drug_operations_log row (status='confirmed') for a system.drug_op form field. The clinician's tap on this endpoint IS the regulator-binding action — AI suggestions never auto-create CD ledger rows.",
		Tags:        []string{"Notes", "Drugs"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, dispense},
	}, h.materialiseDrugOp)

	huma.Register(api, huma.Operation{
		OperationID: "materialise-incident-field",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/fields/{field_id}/materialise-incident",
		Summary:     "Materialise a system.incident field",
		Description: "Creates an incident_events row for a system.incident form field. The SIRS / CQC classifier runs server-side as today; the regulator deadline starts on creation.",
		Tags:        []string{"Notes", "Incidents"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.materialiseIncident)

	huma.Register(api, huma.Operation{
		OperationID: "materialise-pain-score-field",
		Method:      http.MethodPost,
		Path:        "/api/v1/notes/{note_id}/fields/{field_id}/materialise-pain-score",
		Summary:     "Materialise a system.pain_score field",
		Description: "Creates a pain_scores row for a system.pain_score form field. Adds to the patient's pain trend.",
		Tags:        []string{"Notes", "Pain"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatientsOrDispense},
	}, h.materialisePain)
}
