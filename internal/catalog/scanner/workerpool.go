package scanner

import (
	"context"
	"sync"
)

type WorkerPool struct {
	size int
}

func NewWorkerPool(size int) *WorkerPool {
	if size < 1 {
		size = 1
	}
	return &WorkerPool{size: size}
}

// Run dispatches jobs up to p.size in flight. On ctx cancel, no new
// jobs dispatch but Run still waits for in-flight handlers to return.
func (p *WorkerPool) Run(
	ctx context.Context,
	jobs []string,
	handler func(ctx context.Context, job string),
) {
	if len(jobs) == 0 {
		return
	}

	sem := make(chan struct{}, p.size)
	var wg sync.WaitGroup

dispatch:
	for _, job := range jobs {
		// Pre-check: if ctx is cancelled and sem has capacity, select
		// would non-deterministically dispatch one more job past cancel.
		if ctx.Err() != nil {
			break
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}

		wg.Add(1)
		go func(job string) {
			defer wg.Done()
			defer func() { <-sem }()
			handler(ctx, job)
		}(job)
	}

	wg.Wait()
}
