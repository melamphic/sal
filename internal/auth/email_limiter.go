package auth

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// emailLimiter rate-limits magic-link sends per email address. Per-IP rate
// limiting is enforced upstream by the HTTP middleware; this guards against
// the orthogonal attack: a distributed botnet sending magic-link requests for
// a single victim's address to flood their inbox or trigger our mailer
// provider's spam guard.
//
// The limit is intentionally tight (rate.Every(2 * time.Minute), burst=3) —
// in normal use a staff member rarely re-requests within minutes, and the
// silently-dropped excess preserves enumeration resistance (the caller still
// returns 200 to the client).
type emailLimiter struct {
	limiters sync.Map // emailHash → *emailLimiterEntry
	rate     rate.Limit
	burst    int
	stop     chan struct{}
}

type emailLimiterEntry struct {
	limiter      *rate.Limiter
	lastSeenUnix atomic.Int64
}

// newEmailLimiter constructs an email-keyed limiter and starts a background
// sweeper that evicts entries unused for the cleanup window. Pass the
// returned *emailLimiter to Service.SetEmailLimiter; tests can pass nil to
// disable the check entirely.
func newEmailLimiter(r rate.Limit, burst int) *emailLimiter {
	l := &emailLimiter{
		rate:  r,
		burst: burst,
		stop:  make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Stop halts the background cleanup goroutine.
func (l *emailLimiter) Stop() {
	close(l.stop)
}

// allow reports whether a request keyed on emailHash is permitted. Callers
// should treat false as a silent drop, not an error response — surfacing
// rate-limit hits would let an attacker probe whether an address is being
// throttled (and thus exists).
func (l *emailLimiter) allow(emailHash string) bool {
	nowUnix := time.Now().UnixNano()
	if v, ok := l.limiters.Load(emailHash); ok {
		entry := v.(*emailLimiterEntry)
		entry.lastSeenUnix.Store(nowUnix)
		return entry.limiter.Allow()
	}
	entry := &emailLimiterEntry{
		limiter: rate.NewLimiter(l.rate, l.burst),
	}
	entry.lastSeenUnix.Store(nowUnix)
	actual, _ := l.limiters.LoadOrStore(emailHash, entry)
	stored := actual.(*emailLimiterEntry)
	stored.lastSeenUnix.Store(nowUnix)
	return stored.limiter.Allow()
}

func (l *emailLimiter) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-ticker.C:
			cutoff := now.Add(-30 * time.Minute).UnixNano()
			l.limiters.Range(func(key, value any) bool {
				entry := value.(*emailLimiterEntry)
				if entry.lastSeenUnix.Load() < cutoff {
					l.limiters.Delete(key)
				}
				return true
			})
		}
	}
}
