package notifications

import (
	"encoding/json"
	"fmt"
	"net/http"

	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler serves the SSE events endpoint.
type Handler struct {
	broker *Broker
}

// NewHandler creates a new notifications Handler.
func NewHandler(broker *Broker) *Handler {
	return &Handler{broker: broker}
}

// ServeSSE handles GET /api/v1/events.
// Streams note_events as Server-Sent Events to the connected client.
// The client must send a valid Bearer JWT — enforced by the Chi middleware wrapping this handler.
func (h *Handler) ServeSSE(w http.ResponseWriter, r *http.Request) {
	clinicID := mw.ClinicIDFromContext(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)

	// Send initial comment to confirm the stream is open.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	clientID, ch := h.broker.Subscribe(clinicID)
	defer h.broker.Unsubscribe(clinicID, clientID)

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
