// Package dashboard powers the clinic home dashboard. Single endpoint
// (GET /api/v1/clinic/dashboard/snapshot) returns everything the home
// page needs in one round-trip: watchcards, KPI strip, vertical-specific
// action card, drafts count, AI-seat usage, and a recent-activity feed.
//
// The endpoint is cached in-process (sync.Map + TTL) keyed on clinic_id,
// so 5 staff hitting the dashboard at once still costs one SQL pass per
// 60-second window. Writes elsewhere in the app (note submission, drug
// op, incident, etc.) call Invalidate(clinicID) so the next read is
// fresh — no WebSockets, no SSE, no polling-the-DB infrastructure.
package dashboard

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// DefaultTTL is the cache freshness window. 60s = "real-time enough"
// for clinical dashboards while costing 1 query/clinic/minute under
// steady load. Tunable per-call via Service.WithTTL but every caller
// today uses this.
const DefaultTTL = 60 * time.Second

// Cache is the in-process snapshot cache. Keyed by clinic UUID; one
// entry per clinic. Safe for concurrent use. Pre-serialised JSON bytes
// are stored so cache hits return without re-marshalling.
//
// singleflight de-duplicates concurrent misses: if 10 staff hit the
// dashboard at the same moment for one clinic, exactly one DB pass
// runs and all 10 get the same result.
type Cache struct {
	mu      sync.RWMutex
	entries map[uuid.UUID]cacheEntry
	flight  singleflight.Group
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// NewCache builds a fresh cache. Call once at app startup; pass the
// pointer to dashboard.Service.
func NewCache() *Cache {
	return &Cache{entries: make(map[uuid.UUID]cacheEntry)}
}

// Get returns the cached bytes for clinicID if present and not yet
// expired. ok=false on miss or expiry.
func (c *Cache) Get(clinicID uuid.UUID) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.entries[clinicID]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.data, true
}

// Set records freshly-computed bytes for clinicID with the supplied
// TTL. Safe to call concurrently.
func (c *Cache) Set(clinicID uuid.UUID, data []byte, ttl time.Duration) {
	c.mu.Lock()
	c.entries[clinicID] = cacheEntry{data: data, expiresAt: time.Now().Add(ttl)}
	c.mu.Unlock()
}

// Invalidate forgets the cached snapshot for clinicID so the next Get
// misses and re-runs the underlying query. Cheap (just a map delete);
// safe to call from write paths on every mutation. No-op when the
// clinic has no cached entry.
func (c *Cache) Invalidate(clinicID uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, clinicID)
	c.mu.Unlock()
}

// Do runs fn at most once per concurrent request for clinicID — the
// singleflight pattern. Use this around the build-snapshot path so
// only one goroutine hits the DB even when 10 dashboard requests
// arrive in the same window.
func (c *Cache) Do(clinicID uuid.UUID, fn func() ([]byte, error)) ([]byte, error) {
	v, err, _ := c.flight.Do(clinicID.String(), func() (any, error) {
		return fn()
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}
