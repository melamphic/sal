package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/time/rate"
)

// RateLimiterStore holds per-IP rate limiters with periodic cleanup of stale entries.
type RateLimiterStore struct {
	limiters sync.Map
	rate     rate.Limit
	burst    int
	stop     chan struct{}
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiterStore creates a store with the given rate (requests per second) and burst.
// A background goroutine sweeps entries unused for 5 minutes.
func NewRateLimiterStore(r rate.Limit, burst int) *RateLimiterStore {
	s := &RateLimiterStore{
		rate:  r,
		burst: burst,
		stop:  make(chan struct{}),
	}
	go s.cleanup()
	return s
}

// Stop halts the background cleanup goroutine.
func (s *RateLimiterStore) Stop() {
	close(s.stop)
}

func (s *RateLimiterStore) getLimiter(ip string) *rate.Limiter {
	now := time.Now()
	if v, ok := s.limiters.Load(ip); ok {
		entry := v.(*limiterEntry)
		entry.lastSeen = now
		return entry.limiter
	}
	entry := &limiterEntry{
		limiter:  rate.NewLimiter(s.rate, s.burst),
		lastSeen: now,
	}
	actual, _ := s.limiters.LoadOrStore(ip, entry)
	return actual.(*limiterEntry).limiter
}

func (s *RateLimiterStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.limiters.Range(func(key, value any) bool {
				entry := value.(*limiterEntry)
				if now.Sub(entry.lastSeen) > 5*time.Minute {
					s.limiters.Delete(key)
				}
				return true
			})
		}
	}
}

// RateLimitHuma returns a Huma middleware that rejects requests exceeding the per-IP rate.
func RateLimitHuma(api huma.API, store *RateLimiterStore) func(huma.Context, func(huma.Context)) {
	return func(hctx huma.Context, next func(huma.Context)) {
		ip := extractIP(hctx)
		if !store.getLimiter(ip).Allow() {
			_ = huma.WriteErr(api, hctx, http.StatusTooManyRequests, "rate limit exceeded — try again shortly")
			return
		}
		next(hctx)
	}
}

// extractIP reads the client IP from standard proxy headers.
func extractIP(hctx huma.Context) string {
	if ip := hctx.Header("X-Real-Ip"); ip != "" {
		return ip
	}
	if xff := hctx.Header("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return xff
	}
	return "unknown"
}
