package bench

import (
	"context"
	"testing"
)

// Parallel benchmarks: the same calls driven concurrently over the SAME
// persistent client (one gRPC channel vs one pooled HTTP transport) = THROUGHPUT
// under concurrency. gRPC multiplexes many RPCs over one HTTP/2 connection; HTTP/1.1
// falls back to a TCP pool (capped at MaxIdleConnsPerHost) and queues beyond it.
// Sweep concurrency with -cpu (RunParallel spawns GOMAXPROCS goroutines):
//
//	go test ./bench/ -bench '_Send_Parallel$' -benchmem -run='^$' -cpu=1,8,64

func BenchmarkGRPC_Send_Parallel(b *testing.B) {
	benchParallel(b, newHarness(b).grpcSend)
}

func BenchmarkHTTP_Send_Parallel(b *testing.B) {
	benchParallel(b, newHarness(b).httpSend)
}

func benchParallel(b *testing.B, send func(context.Context, string) error) {
	ctx := context.Background()
	for _, p := range payloads {
		html := makePayload(p.size)
		b.Run(p.name, func(b *testing.B) {
			for range warmupCalls {
				if err := send(ctx, html); err != nil {
					b.Fatalf("warmup: %v", err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := send(ctx, html); err != nil {
						b.Fatalf("send: %v", err)
					}
				}
			})
		})
	}
}
