package grpcmw

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthServerInterceptor(t *testing.T) {
	const token = "s3cr3t"
	srv := AuthServerInterceptor(token)
	ok := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	t.Run("valid token passes", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer "+token))
		_, err := srv(ctx, nil, &grpc.UnaryServerInfo{}, ok)
		require.NoError(t, err)
	})
	t.Run("wrong token rejected", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("authorization", "Bearer nope"))
		_, err := srv(ctx, nil, &grpc.UnaryServerInfo{}, ok)
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
	t.Run("missing token rejected", func(t *testing.T) {
		_, err := srv(context.Background(), nil, &grpc.UnaryServerInfo{}, ok)
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}
