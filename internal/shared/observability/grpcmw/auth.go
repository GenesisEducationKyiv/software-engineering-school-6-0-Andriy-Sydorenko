package grpcmw

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	authMetadataKey = "authorization"
	bearerPrefix    = "Bearer "
)

// AuthClientInterceptor attaches the service token to every outgoing call.
func AuthClientInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		ctx = metadata.AppendToOutgoingContext(ctx, authMetadataKey, bearerPrefix+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// AuthServerInterceptor rejects calls whose bearer token does not match, using a
// constant-time compare. Only mount it when the configured token is non-empty.
func AuthServerInterceptor(token string) grpc.UnaryServerInterceptor {
	want := []byte(token)
	return func(
		ctx context.Context, req any,
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		got := []byte(bearerFromContext(ctx))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid or missing token")
		}
		return handler(ctx, req)
	}
}

func bearerFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(authMetadataKey)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimPrefix(vals[0], bearerPrefix)
}
