package notifier

import (
	"context"

	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

// sender is the transport-agnostic surface the gRPC server adapts. Core
// satisfies it; tests substitute a fake. Defined here (consumer side) so the
// server depends on a narrow interface, not the concrete *Core.
type sender interface {
	SendConfirmation(ctx context.Context, email, repo, confirmToken, unsubscribeToken string) (sent, failed uint32, err error)
	SendReleaseNotifications(ctx context.Context, repo, tag, notesURL string, recipients []Recipient) (sent, failed uint32, err error)
}

// GRPCServer adapts notifierpb requests to the notifier Core. It is the only
// proto-aware type in the service-core; the interceptor chain is supplied by
// platform.NewServer.
type GRPCServer struct {
	pb.UnimplementedNotifierServiceServer
	core sender
}

func NewGRPCServer(core sender) *GRPCServer {
	return &GRPCServer{core: core}
}

func (s *GRPCServer) SendConfirmation(
	ctx context.Context, req *pb.SendConfirmationRequest,
) (*pb.SendAck, error) {
	sent, failed, err := s.core.SendConfirmation(
		ctx, req.GetEmail(), req.GetRepo(), req.GetConfirmToken(), req.GetUnsubscribeToken(),
	)
	if err != nil {
		return nil, err
	}
	return &pb.SendAck{Sent: sent, Failed: failed}, nil
}

func (s *GRPCServer) SendReleaseNotifications(
	ctx context.Context, req *pb.SendReleaseNotificationsRequest,
) (*pb.SendAck, error) {
	recipients := make([]Recipient, 0, len(req.GetRecipients()))
	for _, r := range req.GetRecipients() {
		recipients = append(recipients, Recipient{
			Email:            r.GetEmail(),
			UnsubscribeToken: r.GetUnsubscribeToken(),
		})
	}
	sent, failed, err := s.core.SendReleaseNotifications(
		ctx, req.GetRepo(), req.GetTag(), req.GetNotesUrl(), recipients,
	)
	if err != nil {
		return nil, err
	}
	return &pb.SendAck{Sent: sent, Failed: failed}, nil
}

// Core satisfies the sender surface (keeps notesURL part of the contract).
var _ sender = (*Core)(nil)
