//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
)

// TestRequestReplyRoundTrip exercises the Core NATS request-reply helpers over a real
// broker: RespondJSON serves a handler, RequestJSON calls it and decodes the reply.
func TestRequestReplyRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	url := startNATS(t, ctx)
	nc, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	type req struct {
		N int `json:"n"`
	}
	type resp struct {
		Doubled int `json:"doubled"`
	}

	sub, err := natsbus.RespondJSON(nc, "test.double", "q", func(_ context.Context, data []byte) (any, error) {
		var in req
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, err
		}
		return resp{Doubled: in.N * 2}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReq()

	var out resp
	require.NoError(t, natsbus.RequestJSON(reqCtx, nc, "test.double", req{N: 21}, &out))
	require.Equal(t, 42, out.Doubled)
}
