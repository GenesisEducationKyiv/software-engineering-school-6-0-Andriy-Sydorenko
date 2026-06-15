package notifierclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notifierpb"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/grpcmw"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  notifierpb.NotifierServiceClient
}

func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcmw.RequestIDClientInterceptor()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial notifier %q: %w", addr, err)
	}
	return &Client{conn: conn, rpc: notifierpb.NewNotifierServiceClient(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) SendEmail(ctx context.Context, recipientEmail, subject, htmlBody string) error {
	_, err := c.rpc.SendEmail(
		ctx, &notifierpb.SendEmailRequest{
			RecipientEmail: recipientEmail,
			Subject:        subject,
			HtmlBody:       htmlBody,
		},
	)
	return err
}
