package bench

import (
	"context"
	"testing"
)

// Sequential benchmarks: one call at a time = per-call LATENCY, swept over
// payload size. Lower ns/op is faster.
//
//	go test ./bench/ -bench '_Send$' -benchmem -run='^$' -benchtime=2s

func BenchmarkGRPC_Send(b *testing.B) {
	benchTransport(b, newHarness(b).grpcSend)
}

func BenchmarkHTTP_Send(b *testing.B) {
	benchTransport(b, newHarness(b).httpSend)
}

func benchTransport(b *testing.B, send func(context.Context, string) error) {
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
			for range b.N {
				if err := send(ctx, html); err != nil {
					b.Fatalf("send: %v", err)
				}
			}
		})
	}
}
