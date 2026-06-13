package bench

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

// harness holds both live transports over the same Core plus their persistent
// clients. One gRPC channel and one pooled HTTP client are established once and
// reused across all iterations, so neither transport pays per-call connection
// setup (spec §8 fairness). Both are plaintext (no TLS); auth is enabled on both.
type harness struct {
	core *notifier.Core

	grpcSrv  *grpc.Server
	grpcConn *grpc.ClientConn
	grpc     pb.NotifierServiceClient

	httpSrv    *http.Server
	httpClient *httpClient
}

// newHarness stands up both servers on loopback random ports and dials them. tb
// is used only for fatal setup errors and cleanup registration.
func newHarness(tb testing.TB) *harness {
	tb.Helper()
	core := newCore()

	// --- gRPC server (real platform server + Phase 2 notifier grpcserver) ---
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("grpc listen: %v", err)
	}
	grpcSrv := platform.NewServer(token)
	pb.RegisterNotifierServiceServer(grpcSrv, notifier.NewGRPCServer(core))
	go func() { _ = grpcSrv.Serve(grpcLis) }()

	grpcConn, err := platform.Dial(grpcLis.Addr().String(), token)
	if err != nil {
		tb.Fatalf("grpc dial: %v", err)
	}

	// --- HTTP server (idiomatic net/http + encoding/json over the same core) ---
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("http listen: %v", err)
	}
	httpSrv := &http.Server{Handler: newHTTPHandler(core), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = httpSrv.Serve(httpLis) }()

	h := &harness{
		core:       core,
		grpcSrv:    grpcSrv,
		grpcConn:   grpcConn,
		grpc:       pb.NewNotifierServiceClient(grpcConn),
		httpSrv:    httpSrv,
		httpClient: newHTTPClient(fmt.Sprintf("http://%s", httpLis.Addr().String())),
	}
	tb.Cleanup(h.Close)
	return h
}

// Close tears down both servers and the gRPC channel.
func (h *harness) Close() {
	_ = h.grpcConn.Close()
	h.grpcSrv.GracefulStop()
	_ = h.httpSrv.Shutdown(context.Background())
}

// --- thin per-transport call wrappers, so benchmarks read identically across
//     transports and the equivalence test can pair them up.

func (h *harness) grpcConfirmation(ctx context.Context) (sent, failed uint32, err error) {
	email, repo, ct, ut := confirmationFields()
	ack, err := h.grpc.SendConfirmation(ctx, &pb.SendConfirmationRequest{
		Email: email, Repo: repo, ConfirmUrl: ct, UnsubscribeUrl: ut,
	})
	if err != nil {
		return 0, 0, err
	}
	return ack.GetSent(), ack.GetFailed(), nil
}

func (h *harness) httpConfirmation(ctx context.Context) (sent, failed uint32, err error) {
	email, repo, ct, ut := confirmationFields()
	ack, err := h.httpClient.sendConfirmation(ctx, email, repo, ct, ut)
	if err != nil {
		return 0, 0, err
	}
	return ack.Sent, ack.Failed, nil
}

func (h *harness) grpcRelease(ctx context.Context, recipients []notifier.Recipient) (sent, failed uint32, err error) {
	repo, tag, notesURL := releaseFields()
	pbr := make([]*pb.Recipient, len(recipients))
	for i, r := range recipients {
		pbr[i] = &pb.Recipient{Email: r.Email, UnsubscribeUrl: r.UnsubscribeURL}
	}
	ack, err := h.grpc.SendReleaseNotifications(ctx, &pb.SendReleaseNotificationsRequest{
		Repo: repo, Tag: tag, NotesUrl: notesURL, Recipients: pbr,
	})
	if err != nil {
		return 0, 0, err
	}
	return ack.GetSent(), ack.GetFailed(), nil
}

func (h *harness) httpRelease(ctx context.Context, recipients []notifier.Recipient) (sent, failed uint32, err error) {
	repo, tag, notesURL := releaseFields()
	ack, err := h.httpClient.sendRelease(ctx, repo, tag, notesURL, recipients)
	if err != nil {
		return 0, 0, err
	}
	return ack.Sent, ack.Failed, nil
}
