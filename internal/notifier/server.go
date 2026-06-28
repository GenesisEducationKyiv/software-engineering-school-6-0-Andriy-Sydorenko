package notifier

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"syscall"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notifierpb"
)

type GRPCServer struct {
	notifierpb.UnimplementedNotifierServiceServer
	mailer Mailer
}

func NewGRPCServer(mailer Mailer) *GRPCServer {
	return &GRPCServer{mailer: mailer}
}

func (s *GRPCServer) SendEmail(
	ctx context.Context, req *notifierpb.SendEmailRequest,
) (*notifierpb.SendEmailResponse, error) {
	if req.GetRecipientEmail() == "" || req.GetSubject() == "" || req.GetHtmlBody() == "" {
		return nil, status.Error(codes.InvalidArgument, "recipient_email, subject and html_body are required")
	}
	if err := s.mailer.Send(
		ctx,
		req.GetRecipientEmail(),
		req.GetSubject(),
		req.GetHtmlBody(),
	); err != nil {
		slog.ErrorContext(
			ctx, "send email failed",
			"recipient", maskEmail(req.GetRecipientEmail()), "err", err,
		)
		if isTransientSMTP(err) {
			return nil, status.Error(codes.Unavailable, "mailer temporarily unavailable")
		}
		return nil, status.Error(codes.Internal, "failed to send email")
	}
	slog.InfoContext(ctx, "email sent", "recipient", maskEmail(req.GetRecipientEmail()))
	return &notifierpb.SendEmailResponse{}, nil
}

func isTransientSMTP(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}
