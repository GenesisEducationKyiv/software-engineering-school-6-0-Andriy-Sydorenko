package bench

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTransportsEquivalent guards the benchmark's apples-to-apples premise: both
// transports accept the same input and report success across every payload size.
func TestTransportsEquivalent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, p := range payloads {
		html := makePayload(p.size)
		require.NoError(t, h.grpcSend(ctx, html), "grpc %s", p.name)
		require.NoError(t, h.httpSend(ctx, html), "http %s", p.name)
	}
}
