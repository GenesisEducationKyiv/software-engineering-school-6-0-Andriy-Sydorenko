package notifierclient_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifierclient"
	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

// stubPB is a hand-written notifierpb.NotifierServiceClient for adapter tests.
type stubPB struct {
	conf func(ctx context.Context, in *pb.SendConfirmationRequest, opts ...grpc.CallOption) (*pb.SendAck, error)
	rel  func(ctx context.Context, in *pb.SendReleaseNotificationsRequest, opts ...grpc.CallOption) (*pb.SendAck, error)
}

func (s stubPB) SendConfirmation(ctx context.Context, in *pb.SendConfirmationRequest, opts ...grpc.CallOption) (*pb.SendAck, error) {
	return s.conf(ctx, in, opts...)
}

func (s stubPB) SendReleaseNotifications(ctx context.Context, in *pb.SendReleaseNotificationsRequest, opts ...grpc.CallOption) (*pb.SendAck, error) {
	return s.rel(ctx, in, opts...)
}

func TestAdapter_SendConfirmation_mapsRequest(t *testing.T) {
	var got *pb.SendConfirmationRequest
	c := notifierclient.NewAdapter(stubPB{
		conf: func(_ context.Context, in *pb.SendConfirmationRequest, _ ...grpc.CallOption) (*pb.SendAck, error) {
			got = in
			return &pb.SendAck{Sent: 1}, nil
		},
	})

	err := c.SendConfirmation(context.Background(), "a@b.com", "o/r", "ct", "ut")
	require.NoError(t, err)
	assert.Equal(t, "a@b.com", got.GetEmail())
	assert.Equal(t, "o/r", got.GetRepo())
	assert.Equal(t, "ct", got.GetConfirmToken())
	assert.Equal(t, "ut", got.GetUnsubscribeToken())
}

func TestAdapter_SendReleaseNotifications_mapsRecipients(t *testing.T) {
	var got *pb.SendReleaseNotificationsRequest
	c := notifierclient.NewAdapter(stubPB{
		rel: func(_ context.Context, in *pb.SendReleaseNotificationsRequest, _ ...grpc.CallOption) (*pb.SendAck, error) {
			got = in
			return &pb.SendAck{Sent: 2}, nil
		},
	})

	err := c.SendReleaseNotifications(context.Background(), "o/r", "v1", "https://n",
		[]notifierclient.Recipient{
			{Email: "x@y.com", UnsubscribeToken: "u1"},
			{Email: "z@y.com", UnsubscribeToken: "u2"},
		})
	require.NoError(t, err)
	assert.Equal(t, "o/r", got.GetRepo())
	assert.Equal(t, "v1", got.GetTag())
	assert.Equal(t, "https://n", got.GetNotesUrl())
	require.Len(t, got.GetRecipients(), 2)
	assert.Equal(t, "x@y.com", got.GetRecipients()[0].GetEmail())
	assert.Equal(t, "u2", got.GetRecipients()[1].GetUnsubscribeToken())
}

func TestAdapter_propagatesError(t *testing.T) {
	c := notifierclient.NewAdapter(stubPB{
		conf: func(_ context.Context, _ *pb.SendConfirmationRequest, _ ...grpc.CallOption) (*pb.SendAck, error) {
			return nil, errors.New("rpc down")
		},
	})
	err := c.SendConfirmation(context.Background(), "a@b.com", "o/r", "ct", "ut")
	require.Error(t, err)
}
