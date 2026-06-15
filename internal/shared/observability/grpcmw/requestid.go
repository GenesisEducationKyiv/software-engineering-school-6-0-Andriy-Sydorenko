package grpcmw

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

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

// RequestIDServerInterceptor extracts the request ID from incoming gRPC metadata
// into the context, so the handler's logs carry the same correlation ID.
func RequestIDServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any,
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(requestIDMetadataKey); len(vals) > 0 && vals[0] != "" {
				ctx = logging.WithRequestID(ctx, vals[0])
			}
		}
		return handler(ctx, req)
	}
}
