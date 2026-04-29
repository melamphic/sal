package aidrafts

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers the AI draft endpoints. Permission gating: any
// authenticated staff with manage_patients (the only role that can
// create incidents / capture consent today). The drafts themselves
// don't apply the values to the target domain — that still goes
// through the regular create endpoint after the clinician reviews —
// so the bar for requesting one is low.
func (h *Handler) Mount(_ chi.Router, api huma.API, jwtSecret []byte) {
	auth := mw.AuthenticateHuma(api, jwtSecret)
	manage := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool {
		return p.ManagePatients
	})
	security := []map[string][]string{{"bearerAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID:   "create-ai-draft",
		Method:        http.MethodPost,
		Path:          "/api/v1/ai-drafts",
		Summary:       "Start an AI draft from a recording",
		Description:   "Returns a draft row in `pending_transcript` status. The audio TranscribeAudioWorker fans out to the aidrafts listener when transcription completes; the worker then runs the relevant aigen.*DraftService and flips status to `done`. Poll GET /api/v1/ai-drafts/{id} until status is done or failed. Drafts are never auto-applied — the client reads `draft_payload` and prefills the create-incident / create-consent form for the clinician to review.",
		Tags:          []string{"AI Drafts"},
		Security:      security,
		Middlewares:   huma.Middlewares{auth, manage},
		DefaultStatus: http.StatusAccepted,
	}, h.createDraft)

	huma.Register(api, huma.Operation{
		OperationID: "get-ai-draft",
		Method:      http.MethodGet,
		Path:        "/api/v1/ai-drafts/{id}",
		Summary:     "Get a single AI draft (poll for status)",
		Tags:        []string{"AI Drafts"},
		Security:    security,
		Middlewares: huma.Middlewares{auth, manage},
	}, h.getDraft)
}
