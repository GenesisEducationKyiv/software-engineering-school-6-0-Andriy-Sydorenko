package observability_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability"
)

func TestGRPCServerMetricsInterceptor_passesThroughSuccess(t *testing.T) {
	interceptor := observability.GRPCServerMetricsInterceptor()

	handler := func(_ context.Context, _ any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/notifier.v1.NotifierService/SendConfirmation"}

	resp, err := interceptor(context.Background(), "req", info, handler)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)
}

func TestGRPCServerMetricsInterceptor_passesThroughError(t *testing.T) {
	interceptor := observability.GRPCServerMetricsInterceptor()

	wantErr := errors.New("boom")
	handler := func(_ context.Context, _ any) (any, error) { return nil, wantErr }
	info := &grpc.UnaryServerInfo{FullMethod: "/notifier.v1.NotifierService/SendReleaseNotifications"}

	resp, err := interceptor(context.Background(), "req", info, handler)
	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, resp)
}
