package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

const warmupCalls = 5

// runReleaseBench is the shared body for both release-notification transports.
// fn performs one call; it's invoked warmupCalls times before ResetTimer, then
// b.N times under measurement.
func runReleaseBench(b *testing.B, fn func(ctx context.Context) (uint32, uint32, error)) {
	b.Helper()
	ctx := context.Background()

	for range warmupCalls {
		if _, _, err := fn(ctx); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := fn(ctx); err != nil {
			b.Fatalf("call: %v", err)
		}
	}
}

func BenchmarkSendReleaseNotifications_gRPC(b *testing.B) {
	h := newHarness(b)
	for _, n := range recipientCounts {
		recipients := makeRecipients(n)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			runReleaseBench(b, func(ctx context.Context) (uint32, uint32, error) {
				return h.grpcRelease(ctx, recipients)
			})
		})
	}
}

func BenchmarkSendReleaseNotifications_HTTP(b *testing.B) {
	h := newHarness(b)
	for _, n := range recipientCounts {
		recipients := makeRecipients(n)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			runReleaseBench(b, func(ctx context.Context) (uint32, uint32, error) {
				return h.httpRelease(ctx, recipients)
			})
		})
	}
}

func BenchmarkSendConfirmation_gRPC(b *testing.B) {
	h := newHarness(b)
	ctx := context.Background()
	for range warmupCalls {
		if _, _, err := h.grpcConfirmation(ctx); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := h.grpcConfirmation(ctx); err != nil {
			b.Fatalf("call: %v", err)
		}
	}
}

func BenchmarkSendConfirmation_HTTP(b *testing.B) {
	h := newHarness(b)
	ctx := context.Background()
	for range warmupCalls {
		if _, _, err := h.httpConfirmation(ctx); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := h.httpConfirmation(ctx); err != nil {
			b.Fatalf("call: %v", err)
		}
	}
}

// TestTransportsEquivalent proves both transports return the same SendAck for
// identical input, for every benchmarked operation and payload size. If this
// passes, the ns/op and wire-byte deltas measured by the benchmarks are due to
// transport alone — the behavior is provably identical (apples-to-apples).
func TestTransportsEquivalent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	t.Run("SendConfirmation", func(t *testing.T) {
		gSent, gFailed, err := h.grpcConfirmation(ctx)
		require.NoError(t, err)
		hSent, hFailed, err := h.httpConfirmation(ctx)
		require.NoError(t, err)

		assert.Equal(t, gSent, hSent, "sent must match across transports")
		assert.Equal(t, gFailed, hFailed, "failed must match across transports")
		assert.Equal(t, uint32(1), gSent)
		assert.Equal(t, uint32(0), gFailed)
	})

	t.Run("SendReleaseNotifications", func(t *testing.T) {
		for _, n := range recipientCounts {
			recipients := makeRecipients(n)
			t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
				gSent, gFailed, err := h.grpcRelease(ctx, recipients)
				require.NoError(t, err)
				hSent, hFailed, err := h.httpRelease(ctx, recipients)
				require.NoError(t, err)

				assert.Equal(t, gSent, hSent, "sent must match across transports")
				assert.Equal(t, gFailed, hFailed, "failed must match across transports")
				assert.Equal(t, uint32(n), gSent, "all recipients sent (stub mailer never fails)")
				assert.Equal(t, uint32(0), gFailed)
			})
		}
	})
}

// TestWireSize prints the on-the-wire serialized payload size of each request
// under Protobuf vs JSON. Not an assertion test — it emits the table the README
// reports (Protobuf vs JSON bytes). Run with -v to see it:
//
//	go test -run TestWireSize -v ./bench/...
func TestWireSize(t *testing.T) {
	email, repo, ct, ut := confirmationFields()
	rRepo, rTag, rNotes := releaseFields()

	type row struct {
		op       string
		n        int
		protoLen int
		jsonLen  int
	}
	var rows []row

	// SendConfirmation (tiny, fixed).
	{
		pbReq := &pb.SendConfirmationRequest{Email: email, Repo: repo, ConfirmToken: ct, UnsubscribeToken: ut}
		pbBytes, err := proto.Marshal(pbReq)
		require.NoError(t, err)
		jsonBytes, err := json.Marshal(jsonSendConfirmationRequest{
			Email: email, Repo: repo, ConfirmToken: ct, UnsubscribeToken: ut,
		})
		require.NoError(t, err)
		rows = append(rows, row{"SendConfirmation", 1, len(pbBytes), len(jsonBytes)})
	}

	// SendReleaseNotifications across the payload-scaling axis.
	for _, n := range recipientCounts {
		recipients := makeRecipients(n)

		pbr := make([]*pb.Recipient, len(recipients))
		jr := make([]jsonRecipient, len(recipients))
		for i, r := range recipients {
			pbr[i] = &pb.Recipient{Email: r.Email, UnsubscribeToken: r.UnsubscribeToken}
			jr[i] = jsonRecipient{Email: r.Email, UnsubscribeToken: r.UnsubscribeToken}
		}
		pbBytes, err := proto.Marshal(&pb.SendReleaseNotificationsRequest{
			Repo: rRepo, Tag: rTag, NotesUrl: rNotes, Recipients: pbr,
		})
		require.NoError(t, err)
		jsonBytes, err := json.Marshal(jsonSendReleaseNotificationsRequest{
			Repo: rRepo, Tag: rTag, NotesURL: rNotes, Recipients: jr,
		})
		require.NoError(t, err)
		rows = append(rows, row{"SendReleaseNotifications", n, len(pbBytes), len(jsonBytes)})
	}

	t.Log("\n=== On-the-wire request payload size (bytes) ===")
	t.Logf("%-28s %8s %12s %12s %10s", "op", "N", "protobuf", "json", "json/proto")
	for _, r := range rows {
		ratio := float64(r.jsonLen) / float64(r.protoLen)
		t.Logf("%-28s %8d %12d %12d %9.2fx", r.op, r.n, r.protoLen, r.jsonLen, ratio)
	}

	// Sanity: Protobuf is never larger than JSON for these messages.
	for _, r := range rows {
		assert.LessOrEqual(t, r.protoLen, r.jsonLen, "protobuf should be <= json for %s N=%d", r.op, r.n)
	}
}
