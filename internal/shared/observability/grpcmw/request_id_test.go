package grpcmw

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"
)

func TestRequestIDServerInterceptor_MintsWhenAbsent(t *testing.T) {
	var seen string
	handler := func(ctx context.Context, req any) (any, error) {
		seen = logging.RequestIDFromContext(ctx)
		return nil, nil
	}
	_, err := RequestIDServerInterceptor()(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	assert.NotEmpty(t, seen, "expected a minted request_id when none was sent")
}

func TestRequestIDServerInterceptor_UsesIncoming(t *testing.T) {
	var seen string
	handler := func(ctx context.Context, req any) (any, error) {
		seen = logging.RequestIDFromContext(ctx)
		return nil, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-id", "abc-123"))
	_, err := RequestIDServerInterceptor()(ctx, nil, &grpc.UnaryServerInfo{}, handler)
	require.NoError(t, err)
	assert.Equal(t, "abc-123", seen)
}
