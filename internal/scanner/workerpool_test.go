package scanner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPoolRunsAllJobs(t *testing.T) {
	pool := NewWorkerPool(4)
	jobs := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}

	var seenMu sync.Mutex
	seen := map[string]bool{}

	pool.Run(context.Background(), jobs, func(_ context.Context, job string) {
		seenMu.Lock()
		seen[job] = true
		seenMu.Unlock()
	})

	if len(seen) != len(jobs) {
		t.Fatalf("ran %d jobs, want %d", len(seen), len(jobs))
	}
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

	if got := peak.Load(); got > size {
		t.Fatalf("observed %d concurrent handlers, cap was %d", got, size)
	}
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

	if int(ran.Load()) == len(jobs) {
		t.Fatalf("expected dispatch to stop on ctx cancel; ran all %d jobs", len(jobs))
	}
}

func TestWorkerPoolEmptyJobsIsNoOp(t *testing.T) {
	pool := NewWorkerPool(4)
	pool.Run(context.Background(), nil, func(_ context.Context, _ string) {
		t.Fatal("handler must not be called for empty job list")
	})
}

func TestWorkerPoolSizeBelowOneClampedToOne(t *testing.T) {
	pool := NewWorkerPool(0)
	if pool.size != 1 {
		t.Fatalf("size=%d, want 1 (clamped)", pool.size)
	}
}
