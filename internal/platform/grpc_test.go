package platform_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/correlation"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

type fakeNotifier struct {
	pb.UnimplementedNotifierServiceServer
	gotCorrelation string
	panicNext      bool
}

func (f *fakeNotifier) SendConfirmation(ctx context.Context, _ *pb.SendConfirmationRequest) (*pb.SendAck, error) {
	if f.panicNext {
		panic("boom")
	}
	f.gotCorrelation = correlation.FromContext(ctx)
	return &pb.SendAck{Sent: 1}, nil
}

func startServer(t *testing.T, token string, fake *fakeNotifier) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := platform.NewServer(token)
	pb.RegisterNotifierServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return lis.Addr().String()
}

func TestServerClient_authSuccessAndCorrelationPropagation(t *testing.T) {
	fake := &fakeNotifier{}
	addr := startServer(t, "s3cret", fake)

	conn, err := platform.Dial(addr, "s3cret")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewNotifierServiceClient(conn)

	ctx := correlation.WithID(context.Background(), "cid-42")
	ack, err := client.SendConfirmation(ctx, &pb.SendConfirmationRequest{Email: "a@b.c", Repo: "o/r"})
	require.NoError(t, err)
	assert.Equal(t, uint32(1), ack.GetSent())
	assert.Equal(t, "cid-42", fake.gotCorrelation)
}

func TestServerClient_authRejectsWrongToken(t *testing.T) {
	addr := startServer(t, "s3cret", &fakeNotifier{})
	conn, err := platform.Dial(addr, "WRONG")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewNotifierServiceClient(conn)

	_, err = client.SendConfirmation(context.Background(), &pb.SendConfirmationRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestServerClient_recoversFromPanic(t *testing.T) {
	addr := startServer(t, "s3cret", &fakeNotifier{panicNext: true})
	conn, err := platform.Dial(addr, "s3cret")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewNotifierServiceClient(conn)

	_, err = client.SendConfirmation(context.Background(), &pb.SendConfirmationRequest{})
	assert.Equal(t, codes.Internal, status.Code(err))
}
