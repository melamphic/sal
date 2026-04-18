package patient

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all patient and contact routes onto the provided Chi router.
// All routes require a valid JWT (enforced by AuthenticateHuma).
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	managePatients := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.ManagePatients })
	security := []map[string][]string{{"bearerAuth": {}}}

	// ── Contacts ──────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "create-contact",
		Method:      http.MethodPost,
		Path:        "/api/v1/contacts",
		Summary:     "Create a contact",
		Description: "Creates a new contact (owner or client). All PII fields are encrypted at rest. A contact can be linked to one or more subjects.",
		Tags:        []string{"Contacts"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.createContact)

	huma.Register(api, huma.Operation{
		OperationID: "list-contacts",
		Method:      http.MethodGet,
		Path:        "/api/v1/contacts",
		Summary:     "List contacts",
		Description: "Returns a paginated list of all contacts for the authenticated clinic.",
		Tags:        []string{"Contacts"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.listContacts)

	huma.Register(api, huma.Operation{
		OperationID: "get-contact",
		Method:      http.MethodGet,
		Path:        "/api/v1/contacts/{contact_id}",
		Summary:     "Get a contact",
		Description: "Returns a contact by ID with all of their linked subjects inline.",
		Tags:        []string{"Contacts"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.getContact)

	huma.Register(api, huma.Operation{
		OperationID: "update-contact",
		Method:      http.MethodPatch,
		Path:        "/api/v1/contacts/{contact_id}",
		Summary:     "Update a contact",
		Description: "Partially updates a contact's details. Only provided fields are changed. All PII fields are re-encrypted on update.",
		Tags:        []string{"Contacts"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.updateContact)

	// ── Patients (Subjects) ────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "create-patient",
		Method:      http.MethodPost,
		Path:        "/api/v1/patients",
		Summary:     "Create a patient",
		Description: "Creates a new patient (subject). For the veterinary vertical, vet_details must be provided. A contact can optionally be linked at creation time or added later via the link-contact endpoint.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.createSubject)

	huma.Register(api, huma.Operation{
		OperationID: "list-patients",
		Method:      http.MethodGet,
		Path:        "/api/v1/patients",
		Summary:     "List patients",
		Description: "Returns a paginated list of patients. Staff with view_all_patients see all clinic patients. Staff with view_own_patients only see patients they created. Supports optional filters: status, species, contact_id.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listSubjects)

	huma.Register(api, huma.Operation{
		OperationID: "get-patient",
		Method:      http.MethodGet,
		Path:        "/api/v1/patients/{subject_id}",
		Summary:     "Get a patient",
		Description: "Returns a patient by ID with their linked contact and vertical-specific details. Respects view_own_patients scope.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.getSubject)

	huma.Register(api, huma.Operation{
		OperationID: "update-patient",
		Method:      http.MethodPatch,
		Path:        "/api/v1/patients/{subject_id}",
		Summary:     "Update a patient",
		Description: "Partially updates a patient's details and/or their veterinary details. Only provided fields are changed.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.updateSubject)

	huma.Register(api, huma.Operation{
		OperationID: "archive-patient",
		Method:      http.MethodDelete,
		Path:        "/api/v1/patients/{subject_id}",
		Summary:     "Archive a patient",
		Description: "Soft-deletes a patient by setting archived_at. The record is preserved for audit trail integrity and is not recoverable via the API.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.archiveSubject)

	huma.Register(api, huma.Operation{
		OperationID: "link-patient-contact",
		Method:      http.MethodPost,
		Path:        "/api/v1/patients/{subject_id}/contact",
		Summary:     "Link a contact to a patient",
		Description: "Links an existing contact as the owner of a patient. Use this when a patient was created without a contact and the owner is registered separately.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.linkContact)

	huma.Register(api, huma.Operation{
		OperationID: "unmask-patient-pii",
		Method:      http.MethodPost,
		Path:        "/api/v1/patients/{subject_id}/reveal",
		Summary:     "Reveal an encrypted patient field",
		Description: "Returns the plaintext of a single encrypted PHI/PII field (e.g. insurance_policy_number, allergies) and appends an 'unmask_pii' entry to the subject access log. Requires manage_patients. Pair every tap-to-reveal UI with this endpoint.",
		Tags:        []string{"Patients"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, managePatients},
	}, h.unmaskPII)
}
