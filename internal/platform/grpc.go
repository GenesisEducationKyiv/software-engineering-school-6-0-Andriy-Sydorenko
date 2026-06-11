package platform

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability"
)

// NewServer builds a gRPC server whose interceptor chain runs outermost to
// innermost: recovery (catches a panic from any inner stage) -> correlation (so
// every later stage logs under the ID) -> metrics -> auth.
func NewServer(token string) *grpc.Server {
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			RecoveryServerInterceptor(),
			CorrelationServerInterceptor(),
			observability.GRPCServerMetricsInterceptor(),
			AuthServerInterceptor(token),
		),
	)
}

// Dial builds a client connection to addr with correlation + auth interceptors.
// Plaintext (no TLS): internal network only.
func Dial(addr, token string) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			CorrelationClientInterceptor(),
			AuthClientInterceptor(token),
		),
	)
}
