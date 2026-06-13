package bench

import (
	"context"
	"fmt"
	"testing"
)

// runParallel drives fn from many goroutines at once, all sharing the harness's
// single persistent client (one gRPC channel / one pooled HTTP client). Unlike
// the sequential benchmarks (one call at a time = per-call LATENCY), this
// measures THROUGHPUT under concurrency: how many in-flight requests each
// transport sustains. It is the request-efficiency / multiplexing test — gRPC
// multiplexes many concurrent RPCs over one HTTP/2 connection; HTTP/1.1 falls
// back to a pool of TCP connections (capped at MaxIdleConnsPerHost) and queues
// beyond that.
//
// Sweep the concurrency level with -cpu (RunParallel spawns GOMAXPROCS goroutines):
//
//	go test -bench='Parallel' -benchmem -run='^$' -cpu=1,8,64 ./bench/...
//
// Under RunParallel, ns/op is wall-time per op aggregated across all goroutines,
// so throughput is req/s = 1e9 / ns_op — lower ns/op means more requests/sec.
func runParallel(b *testing.B, fn func(ctx context.Context) (uint32, uint32, error)) {
	b.Helper()
	ctx := context.Background()

	for range warmupCalls {
		if _, _, err := fn(ctx); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := fn(ctx); err != nil {
				b.Fatalf("call: %v", err)
			}
		}
	})
}

func BenchmarkSendConfirmation_gRPC_Parallel(b *testing.B) {
	h := newHarness(b)
	runParallel(b, h.grpcConfirmation)
}

func BenchmarkSendConfirmation_HTTP_Parallel(b *testing.B) {
	h := newHarness(b)
	runParallel(b, h.httpConfirmation)
}

func BenchmarkSendReleaseNotifications_gRPC_Parallel(b *testing.B) {
	h := newHarness(b)
	for _, n := range recipientCounts {
		recipients := makeRecipients(n)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			runParallel(b, func(ctx context.Context) (uint32, uint32, error) {
				return h.grpcRelease(ctx, recipients)
			})
		})
	}
}

func BenchmarkSendReleaseNotifications_HTTP_Parallel(b *testing.B) {
	h := newHarness(b)
	for _, n := range recipientCounts {
		recipients := makeRecipients(n)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			runParallel(b, func(ctx context.Context) (uint32, uint32, error) {
				return h.httpRelease(ctx, recipients)
			})
		})
	}
}
