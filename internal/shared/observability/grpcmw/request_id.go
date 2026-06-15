package grpcmw

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/google/uuid"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"
)

const requestIDMetadataKey = "x-request-id"

func RequestIDClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		if id := logging.RequestIDFromContext(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, requestIDMetadataKey, id)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// RequestIDServerInterceptor pulls the request ID from incoming gRPC metadata
// into the context so the handler's logs carry the same correlation ID. When no
// ID was propagated (direct call, or a client that didn't set one), it mints a
// fresh one so every server-side log line is still correlatable.
func RequestIDServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any,
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		id := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(requestIDMetadataKey); len(vals) > 0 {
				id = vals[0]
			}
		}
		if id == "" {
			id = uuid.NewString()
		}
		ctx = logging.WithRequestID(ctx, id)
		return handler(ctx, req)
	}
}
