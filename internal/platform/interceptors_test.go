package platform_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/correlation"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
)

func TestAuthServerInterceptor(t *testing.T) {
	intc := platform.AuthServerInterceptor("s3cret")
	info := &grpc.UnaryServerInfo{FullMethod: "/x/Y"}
	ok := func(_ context.Context, _ any) (any, error) { return "ok", nil }

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer s3cret"))
	_, err := intc(ctx, nil, info, ok)
	require.NoError(t, err)

	_, err = intc(context.Background(), nil, info, ok)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	bad := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer nope"))
	_, err = intc(bad, nil, info, ok)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestRecoveryServerInterceptor(t *testing.T) {
	intc := platform.RecoveryServerInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/x/Y"}
	boom := func(_ context.Context, _ any) (any, error) { panic("boom") }

	_, err := intc(context.Background(), nil, info, boom)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestCorrelationServerInterceptor_readsMetadata(t *testing.T) {
	intc := platform.CorrelationServerInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/x/Y"}
	var seen string
	capture := func(ctx context.Context, _ any) (any, error) {
		seen = correlation.FromContext(ctx)
		return nil, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-correlation-id", "cid-1"))
	_, err := intc(ctx, nil, info, capture)
	require.NoError(t, err)
	assert.Equal(t, "cid-1", seen)
}

func TestCorrelationServerInterceptor_generatesWhenAbsent(t *testing.T) {
	intc := platform.CorrelationServerInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/x/Y"}
	var seen string
	capture := func(ctx context.Context, _ any) (any, error) {
		seen = correlation.FromContext(ctx)
		return nil, nil
	}
	_, err := intc(context.Background(), nil, info, capture)
	require.NoError(t, err)
	assert.NotEmpty(t, seen)
}
