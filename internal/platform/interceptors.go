package platform

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/correlation"
)

const (
	authMDKey        = "authorization"
	bearerPrefix     = "Bearer "
	correlationMDKey = "x-correlation-id"
)

func AuthServerInterceptor(token string) grpc.UnaryServerInterceptor {
	want := []byte(token)
	return func(
		ctx context.Context, req any,
		_ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		got := []byte(bearerFromContext(ctx))
		// ConstantTimeCompare also returns 0 on length mismatch, so a missing or
		// short token is rejected without leaking timing.
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
	vals := md.Get(authMDKey)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimPrefix(vals[0], bearerPrefix)
}

func AuthClientInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		ctx = metadata.AppendToOutgoingContext(ctx, authMDKey, bearerPrefix+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func RecoveryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any,
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.ErrorContext(ctx, "grpc handler panic",
					"method", info.FullMethod, "panic", r)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func CorrelationServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any,
		_ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		id := correlationFromMD(ctx)
		if id == "" {
			id = correlation.NewID()
		}
		return handler(correlation.WithID(ctx, id), req)
	}
}

func correlationFromMD(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if vals := md.Get(correlationMDKey); len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func CorrelationClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		if id := correlation.FromContext(ctx); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, correlationMDKey, id)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
