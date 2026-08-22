package middleware

import (
	"context"
	"math"
	"sync"
	"time"
)

// Limiter is an in-process token bucket keyed by an arbitrary string — an IP for
// one instance, an email address for another.
//
// Two things about it are deliberate:
//
//   - Tokens are spent by Fail, not by Allowed. The contract counts *failed*
//     login attempts, not requests, so a user typing their password correctly
//     the first time never touches a bucket.
//   - It is in-process, which means the budget is per instance and the
//     effective limit is the configured limit MULTIPLIED by the number of live
//     instances. On the serverless host this project deploys to (DECISIONS
//     D-20) that count is not something the application sets, so the multiplier
//     is unknown rather than merely large. At one or two instances this is a
//     real limit; at fifty it is not, and the honest fix then is a shared store
//     (Redis, or a Postgres table) or an edge rate-limit rule — not a bigger
//     map. Recorded here, and in docs/SECURITY.md section 6, so nobody assumes
//     otherwise.
type Limiter struct {
	capacity float64
	// refillPerSecond spreads a full bucket across the window, so 5 failures in
	// 15 minutes recovers gradually rather than all at once on a boundary.
	refillPerSecond float64
	window          time.Duration

	now func() time.Time // injectable for tests

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewLimiter builds a limiter allowing capacity failures per window.
func NewLimiter(capacity int, window time.Duration) *Limiter {
	if capacity < 1 {
		capacity = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &Limiter{
		capacity:        float64(capacity),
		refillPerSecond: float64(capacity) / window.Seconds(),
		window:          window,
		now:             time.Now,
		buckets:         make(map[string]*bucket),
	}
}

// Allowed reports whether key still has budget, and if not, how long until it
// does. It does not consume anything: call Fail for that.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	if key == "" {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refill(key)
	if b.tokens >= 1 {
		return true, 0
	}

	// Time for the bucket to reach one whole token.
	deficit := 1 - b.tokens
	seconds := deficit / l.refillPerSecond
	return false, time.Duration(math.Ceil(seconds)) * time.Second
}

// Fail spends one token for key. Called after a rejected attempt.
func (l *Limiter) Fail(key string) {
	if key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refill(key)
	b.tokens--
	// Clamping stops a sustained attack from digging an arbitrarily deep hole
	// that would keep a legitimate user out long after the window.
	if b.tokens < -l.capacity {
		b.tokens = -l.capacity
	}
}

// Reset clears the budget for key. A successful login calls this so a user who
// mistyped four times is not punished afterwards (contract section 4).
func (l *Limiter) Reset(key string) {
	if key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// refill must be called with the mutex held.
func (l *Limiter) refill(key string) *bucket {
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, lastSeen: now}
		l.buckets[key] = b
		return b
	}

	if elapsed := now.Sub(b.lastSeen); elapsed > 0 {
		b.tokens = math.Min(l.capacity, b.tokens+elapsed.Seconds()*l.refillPerSecond)
	}
	b.lastSeen = now
	return b
}

// sweep drops buckets that have had time to refill completely. Without it the
// map is an unbounded memory leak keyed by attacker-supplied strings.
func (l *Limiter) sweep() {
	cutoff := l.now().Add(-2 * l.window)

	l.mu.Lock()
	defer l.mu.Unlock()
	for key, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// StartJanitor sweeps stale buckets until ctx is cancelled. Safe to call once
// per limiter at startup.
func (l *Limiter) StartJanitor(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(l.window)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.sweep()
			}
		}
	}()
}

// Size reports the number of tracked keys. Exported for tests and for a future
// metric; not used in a request path.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
