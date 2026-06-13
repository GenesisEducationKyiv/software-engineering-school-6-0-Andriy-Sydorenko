package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIPRateLimiter_BurstThenDeny(t *testing.T) {
	l := newIPRateLimiter(5)
	now := time.Unix(0, 0)

	for i := 0; i < 5; i++ {
		assert.True(t, l.allow("1.2.3.4", now), "burst request %d should pass", i)
	}
	assert.False(t, l.allow("1.2.3.4", now), "6th request within burst must be denied")
}

func TestIPRateLimiter_RefillsOverTime(t *testing.T) {
	l := newIPRateLimiter(5)
	now := time.Unix(0, 0)

	for i := 0; i < 5; i++ {
		l.allow("1.2.3.4", now)
	}
	assert.False(t, l.allow("1.2.3.4", now))

	// One token regenerates after a full second at 1/sec.
	assert.True(t, l.allow("1.2.3.4", now.Add(time.Second)))
	assert.False(t, l.allow("1.2.3.4", now.Add(time.Second)))
}

func TestIPRateLimiter_PerKeyIsolation(t *testing.T) {
	l := newIPRateLimiter(1)
	now := time.Unix(0, 0)

	assert.True(t, l.allow("a", now))
	assert.False(t, l.allow("a", now))
	assert.True(t, l.allow("b", now), "a separate key keeps its own bucket")
}

func TestIPRateLimiter_SweepEvictsIdle(t *testing.T) {
	l := newIPRateLimiter(1)
	now := time.Unix(0, 0)

	l.allow("stale", now)
	assert.Len(t, l.buckets, 1)

	// A later request past the TTL triggers a sweep that drops the idle bucket;
	// only the active key remains.
	l.allow("fresh", now.Add(bucketIdleTTL+time.Second))
	assert.Len(t, l.buckets, 1)
	_, ok := l.buckets["stale"]
	assert.False(t, ok, "idle bucket should be evicted")
}
