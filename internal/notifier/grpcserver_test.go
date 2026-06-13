package notifier_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

// fakeSender lets the test drive the server without a real Core.
type fakeSender struct {
	conf func(ctx context.Context, email, repo, ct, ut string) (uint32, uint32, error)
	rel  func(ctx context.Context, repo, tag, notes string, rs []notifier.Recipient) (uint32, uint32, error)
}

func (f fakeSender) SendConfirmation(ctx context.Context, email, repo, ct, ut string) (sent, failed uint32, err error) {
	return f.conf(ctx, email, repo, ct, ut)
}

func (f fakeSender) SendReleaseNotifications(ctx context.Context, repo, tag, notes string, rs []notifier.Recipient) (sent, failed uint32, err error) {
	return f.rel(ctx, repo, tag, notes, rs)
}

func TestGRPCServer_SendConfirmation_mapsArgsAndAck(t *testing.T) {
	var gotEmail, gotRepo, gotCT, gotUT string
	srv := notifier.NewGRPCServer(fakeSender{
		conf: func(_ context.Context, email, repo, ct, ut string) (uint32, uint32, error) {
			gotEmail, gotRepo, gotCT, gotUT = email, repo, ct, ut
			return 1, 0, nil
		},
	})

	ack, err := srv.SendConfirmation(context.Background(), &pb.SendConfirmationRequest{
		Email: "a@b.com", Repo: "o/r", ConfirmUrl: "ct", UnsubscribeUrl: "ut",
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(1), ack.GetSent())
	assert.Equal(t, uint32(0), ack.GetFailed())
	assert.Equal(t, "a@b.com", gotEmail)
	assert.Equal(t, "o/r", gotRepo)
	assert.Equal(t, "ct", gotCT)
	assert.Equal(t, "ut", gotUT)
}

func TestGRPCServer_SendReleaseNotifications_mapsRecipients(t *testing.T) {
	var gotRepo, gotTag, gotNotes string
	var gotRecipients []notifier.Recipient
	srv := notifier.NewGRPCServer(fakeSender{
		rel: func(_ context.Context, repo, tag, notes string, rs []notifier.Recipient) (uint32, uint32, error) {
			gotRepo, gotTag, gotNotes, gotRecipients = repo, tag, notes, rs
			return 2, 1, nil
		},
	})

	ack, err := srv.SendReleaseNotifications(context.Background(), &pb.SendReleaseNotificationsRequest{
		Repo: "o/r", Tag: "v1", NotesUrl: "https://n",
		Recipients: []*pb.Recipient{
			{Email: "x@y.com", UnsubscribeUrl: "u1"},
			{Email: "z@y.com", UnsubscribeUrl: "u2"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(2), ack.GetSent())
	assert.Equal(t, uint32(1), ack.GetFailed())
	assert.Equal(t, "o/r", gotRepo)
	assert.Equal(t, "v1", gotTag)
	assert.Equal(t, "https://n", gotNotes)
	require.Len(t, gotRecipients, 2)
	assert.Equal(t, notifier.Recipient{Email: "x@y.com", UnsubscribeURL: "u1"}, gotRecipients[0])
}

func TestGRPCServer_SendConfirmation_propagatesError(t *testing.T) {
	srv := notifier.NewGRPCServer(fakeSender{
		conf: func(_ context.Context, _, _, _, _ string) (uint32, uint32, error) {
			return 0, 0, errors.New("render failed")
		},
	})
	_, err := srv.SendConfirmation(context.Background(), &pb.SendConfirmationRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.NotContains(t, err.Error(), "render failed") // internal detail must not leak to clients
}
