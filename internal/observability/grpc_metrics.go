package observability

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var grpcServerDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "grpc_server_handling_seconds",
		Help:    "gRPC server handler latency in seconds, labeled by method and status code.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"method", "code"},
)

func GRPCServerMetricsInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context, req any,
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		grpcServerDuration.
			WithLabelValues(info.FullMethod, status.Code(err).String()).
			Observe(time.Since(start).Seconds())
		return resp, err
	}
}
