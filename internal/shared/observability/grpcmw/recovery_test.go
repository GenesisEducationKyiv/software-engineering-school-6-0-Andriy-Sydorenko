package grpcmw

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRecoveryServerInterceptor_RecoversPanic(t *testing.T) {
	interceptor := RecoveryServerInterceptor()
	panicking := func(ctx context.Context, req any) (any, error) { panic("boom") }

	resp, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/test/Method"}, panicking)

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestRecoveryServerInterceptor_PassesThrough(t *testing.T) {
	interceptor := RecoveryServerInterceptor()
	ok := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	resp, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/test/Method"}, ok)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}
