package scanner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPoolRunsAllJobs(t *testing.T) {
	pool := NewWorkerPool(4)
	jobs := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}

	var mu sync.Mutex
	seen := map[string]bool{}

	pool.Run(context.Background(), jobs, func(_ context.Context, job string) {
		mu.Lock()
		seen[job] = true
		mu.Unlock()
	})

	assert.Len(t, seen, len(jobs))
}

func TestWorkerPoolRespectsConcurrencyCap(t *testing.T) {
	const size = 3
	pool := NewWorkerPool(size)
	jobs := []string{"1", "2", "3", "4", "5", "6", "7", "8"}

	var inFlight, peak atomic.Int32

	pool.Run(context.Background(), jobs, func(_ context.Context, _ string) {
		cur := inFlight.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
	})

	assert.LessOrEqual(t, peak.Load(), int32(size))
}

func TestWorkerPoolStopsDispatchOnContextCancel(t *testing.T) {
	pool := NewWorkerPool(2)
	jobs := make([]string, 50)
	for i := range jobs {
		jobs[i] = "j"
	}

	ctx, cancel := context.WithCancel(context.Background())

	var ran atomic.Int32
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	pool.Run(ctx, jobs, func(_ context.Context, _ string) {
		ran.Add(1)
		time.Sleep(5 * time.Millisecond)
	})

	assert.Less(t, int(ran.Load()), len(jobs), "dispatch should stop on ctx cancel")
}

func TestWorkerPoolEmptyJobsIsNoOp(t *testing.T) {
	pool := NewWorkerPool(4)
	pool.Run(context.Background(), nil, func(_ context.Context, _ string) {
		require.Fail(t, "handler must not be called for empty job list")
	})
}

func TestWorkerPoolSizeBelowOneClampedToOne(t *testing.T) {
	pool := NewWorkerPool(0)
	assert.Equal(t, 1, pool.size)
}
