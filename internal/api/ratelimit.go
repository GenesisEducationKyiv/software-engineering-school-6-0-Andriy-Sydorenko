package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	subscribeRatePerSec = 1
	subscribeBurst      = 5
	bucketIdleTTL       = 10 * time.Minute
)

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// ipRateLimiter is a per-key token bucket: `burst` capacity, refilling at
// `rate` tokens/sec. Idle buckets are swept lazily on access (no background
// goroutine), bounding memory without lifecycle management.
type ipRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	rate      float64
	burst     float64
	ttl       time.Duration
	lastSweep time.Time
}

func newIPRateLimiter(burst float64) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    subscribeRatePerSec,
		burst:   burst,
		ttl:     bucketIdleTTL,
	}
}

func (l *ipRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &tokenBucket{tokens: l.burst - 1, last: now}
		return true
	}

	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep evicts buckets untouched for longer than ttl. Runs at most once per ttl.
func (l *ipRateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.ttl {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > l.ttl {
			delete(l.buckets, k)
		}
	}
	l.lastSweep = now
}

func (l *ipRateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.allow(c.ClientIP(), time.Now()) {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(
				http.StatusTooManyRequests,
				gin.H{"error": "too many requests, slow down"},
			)
			return
		}
		c.Next()
	}
}
