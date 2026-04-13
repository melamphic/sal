package audio

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Mount registers all audio recording routes onto the provided Chi router.
// All routes require a valid JWT (enforced by the Authenticate middleware passed in).
func (h *Handler) Mount(r chi.Router, api huma.API, jwtSecret []byte) {
	authMw := mw.Authenticate(jwtSecret)
	recordAudio := mw.RequirePermissionHuma(api, func(p domain.Permissions) bool { return p.RecordAudio })
	security := []map[string][]string{{"bearerAuth": {}}}

	r.Group(func(r chi.Router) {
		r.Use(authMw)

		huma.Register(api, huma.Operation{
			OperationID: "create-recording",
			Method:      http.MethodPost,
			Path:        "/api/v1/recordings",
			Summary:     "Create a recording",
			Description: "Initiates a new audio recording. Returns a pre-signed upload URL that the client uses to PUT the audio file directly to object storage. Call confirm-upload after the upload completes to trigger transcription.",
			Tags:        []string{"Recordings"},
			Security:    security,
			Middlewares: huma.Middlewares{recordAudio},
		}, h.createRecording)

		huma.Register(api, huma.Operation{
			OperationID: "list-recordings",
			Method:      http.MethodGet,
			Path:        "/api/v1/recordings",
			Summary:     "List recordings",
			Description: "Returns a paginated list of recordings for the authenticated clinic. Supports optional filters: subject_id, staff_id, status.",
			Tags:        []string{"Recordings"},
			Security:    security,
			Middlewares: huma.Middlewares{recordAudio},
		}, h.listRecordings)

		huma.Register(api, huma.Operation{
			OperationID: "get-recording",
			Method:      http.MethodGet,
			Path:        "/api/v1/recordings/{recording_id}",
			Summary:     "Get a recording",
			Description: "Returns recording metadata and current processing status.",
			Tags:        []string{"Recordings"},
			Security:    security,
			Middlewares: huma.Middlewares{recordAudio},
		}, h.getRecording)

		huma.Register(api, huma.Operation{
			OperationID: "confirm-upload",
			Method:      http.MethodPost,
			Path:        "/api/v1/recordings/{recording_id}/confirm-upload",
			Summary:     "Confirm audio upload",
			Description: "Called by the client after the audio file has been uploaded to the pre-signed URL. Transitions the recording from pending_upload to uploaded and enqueues a background transcription job.",
			Tags:        []string{"Recordings"},
			Security:    security,
			Middlewares: huma.Middlewares{recordAudio},
		}, h.confirmUpload)

		huma.Register(api, huma.Operation{
			OperationID: "get-recording-download-url",
			Method:      http.MethodGet,
			Path:        "/api/v1/recordings/{recording_id}/download-url",
			Summary:     "Get a download URL",
			Description: "Returns a short-lived pre-signed GET URL for streaming audio playback on the review screen. Valid for 1 hour.",
			Tags:        []string{"Recordings"},
			Security:    security,
			Middlewares: huma.Middlewares{recordAudio},
		}, h.getDownloadURL)

		huma.Register(api, huma.Operation{
			OperationID: "link-recording-subject",
			Method:      http.MethodPatch,
			Path:        "/api/v1/recordings/{recording_id}/subject",
			Summary:     "Link a patient to a recording",
			Description: "Associates an existing patient (subject) with a recording that was created without one.",
			Tags:        []string{"Recordings"},
			Security:    security,
			Middlewares: huma.Middlewares{recordAudio},
		}, h.linkSubject)
	})
}
