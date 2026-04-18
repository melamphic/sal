// Package notifications provides the SSE broker that pushes note lifecycle
// events to connected clinic staff in real time.
package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is a push notification sent to SSE clients.
type Event struct {
	ClinicID  string `json:"clinic_id"`
	EventID   string `json:"event_id"`
	NoteID    string `json:"note_id"`
	EventType string `json:"event_type"`
}

// Broker manages SSE connections and fans out note_events NOTIFY payloads.
// Start must be called in a goroutine; Stop signals clean shutdown.
type Broker struct {
	db  *pgxpool.Pool
	log *slog.Logger

	mu      sync.RWMutex
	clients map[uuid.UUID]map[string]chan Event // clinic_id → client_id → chan

	done     chan struct{}
	stopOnce sync.Once
}

// NewBroker creates a Broker. Call Start(ctx) in a goroutine to begin listening.
func NewBroker(db *pgxpool.Pool, log *slog.Logger) *Broker {
	return &Broker{
		db:      db,
		log:     log,
		clients: make(map[uuid.UUID]map[string]chan Event),
		done:    make(chan struct{}),
	}
}

// Start listens on the salvia_note_events Postgres NOTIFY channel and fans out
// to subscribed clients. It reconnects automatically on transient errors.
func (b *Broker) Start(ctx context.Context) {
	for {
		select {
		case <-b.done:
			return
		default:
		}

		if err := b.listen(ctx); err != nil {
			b.log.Error("notifications: broker error, reconnecting in 5s", "error", err)
			select {
			case <-b.done:
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// Stop signals the broker to shut down after the current reconnect cycle.
// Safe to call multiple times.
func (b *Broker) Stop() {
	b.stopOnce.Do(func() { close(b.done) })
}

// Subscribe registers a client for clinicID and returns a unique clientID and
// a channel that receives events. Call Unsubscribe when the client disconnects.
func (b *Broker) Subscribe(clinicID uuid.UUID) (clientID string, ch chan Event) {
	clientID = uuid.New().String()
	ch = make(chan Event, 64)

	b.mu.Lock()
	if b.clients[clinicID] == nil {
		b.clients[clinicID] = make(map[string]chan Event)
	}
	b.clients[clinicID][clientID] = ch
	b.mu.Unlock()

	return clientID, ch
}

// Unsubscribe removes a client and closes its channel.
func (b *Broker) Unsubscribe(clinicID uuid.UUID, clientID string) {
	b.mu.Lock()
	if ch, ok := b.clients[clinicID][clientID]; ok {
		close(ch)
		delete(b.clients[clinicID], clientID)
	}
	b.mu.Unlock()
}

// listen acquires a dedicated pgx connection, issues LISTEN, and dispatches
// notifications until the connection drops or Stop is called.
func (b *Broker) listen(ctx context.Context) error {
	conn, err := b.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("notifications.broker.listen: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN salvia_note_events"); err != nil {
		return fmt.Errorf("notifications.broker.listen: listen: %w", err)
	}

	b.log.Info("notifications: broker listening on salvia_note_events")

	for {
		select {
		case <-b.done:
			return nil
		default:
		}

		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			select {
			case <-b.done:
				return nil
			default:
				return fmt.Errorf("notifications.broker.listen: wait: %w", err)
			}
		}

		// Payload: <clinic_id>:<event_id>:<note_id>:<event_type>
		parts := strings.SplitN(notification.Payload, ":", 4)
		if len(parts) != 4 {
			b.log.Warn("notifications: malformed notify payload", "payload", notification.Payload)
			continue
		}

		clinicID, err := uuid.Parse(parts[0])
		if err != nil {
			continue
		}

		evt := Event{
			ClinicID:  parts[0],
			EventID:   parts[1],
			NoteID:    parts[2],
			EventType: parts[3],
		}

		b.fanOut(clinicID, evt)
	}
}

func (b *Broker) fanOut(clinicID uuid.UUID, evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.clients[clinicID] {
		select {
		case ch <- evt:
		default:
			// Slow client — drop rather than block.
		}
	}
}
