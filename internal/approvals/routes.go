package approvals

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers approval routes on the router. Every route requires
// a valid JWT; the per-kind permission gate runs in service.Decide so
// individual route middleware doesn't have to know which kind of
// approval the caller is acting on.
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "list-pending-approvals",
		Method:      http.MethodGet,
		Path:        "/api/v1/approvals",
		Summary:     "List pending approvals",
		Description: "Returns the queue of pending second-pair-of-eyes approvals for the authenticated staff member's clinic, excluding rows they submitted themselves. Filter by `kind` (drug_op | consent | incident | pain_score) to scope the queue to a single system widget.",
		Tags:        []string{"Approvals"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listPending)

	huma.Register(api, huma.Operation{
		OperationID: "list-subject-pending-approvals",
		Method:      http.MethodGet,
		Path:        "/api/v1/approvals/subjects/{subject_id}/pending",
		Summary:     "List pending approvals for a subject",
		Description: "Returns the pending second-pair-of-eyes rows scoped to one subject. Powers the subject hub's 'Pending compliance' card so a clinician opening a patient sees what is still waiting on someone — across drug ops, consents, incidents, and pain scores.",
		Tags:        []string{"Approvals"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.listSubjectPending)

	huma.Register(api, huma.Operation{
		OperationID: "count-pending-approvals",
		Method:      http.MethodGet,
		Path:        "/api/v1/approvals/count",
		Summary:     "Count pending approvals",
		Description: "Returns the count of pending approvals the caller could act on. Drives the dashboard 'N approvals waiting for you' chip.",
		Tags:        []string{"Approvals"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.countPending)

	huma.Register(api, huma.Operation{
		OperationID: "approve-approval",
		Method:      http.MethodPost,
		Path:        "/api/v1/approvals/{approval_id}/approve",
		Summary:     "Approve a pending row",
		Description: "Records a positive sign-off. The entity's snapshot status flips to approved and a `compliance.approval_approved` timeline event is emitted on the patient (if subject-bound) and clinic audit logs.",
		Tags:        []string{"Approvals"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.approve)

	huma.Register(api, huma.Operation{
		OperationID: "challenge-approval",
		Method:      http.MethodPost,
		Path:        "/api/v1/approvals/{approval_id}/challenge",
		Summary:     "Challenge a pending row",
		Description: "Records a rejection. The entity's snapshot status flips to challenged so the original signer is notified to file an addendum. A non-empty `comment` is required so the original signer knows what to fix.",
		Tags:        []string{"Approvals"},
		Security:    security,
		Middlewares: huma.Middlewares{auth},
	}, h.challenge)
}
