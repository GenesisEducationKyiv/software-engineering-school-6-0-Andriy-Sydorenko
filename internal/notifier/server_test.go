package notifier

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notifierpb"
)

type stubMailer struct {
	err   error
	calls int
}

func (m *stubMailer) Send(_ context.Context, _, _, _ string) error {
	m.calls++
	return m.err
}

// timeoutErr satisfies net.Error with Timeout()==true (a transient failure).
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestGRPCServer_SendEmail(t *testing.T) {
	validReq := &notifierpb.SendEmailRequest{
		RecipientEmail: "user@example.com",
		Subject:        "hello",
		HtmlBody:       "<p>hi</p>",
	}

	tests := []struct {
		name      string
		req       *notifierpb.SendEmailRequest
		mailerErr error
		wantCode  codes.Code
	}{
		{"ok", validReq, nil, codes.OK},
		{"missing recipient", &notifierpb.SendEmailRequest{Subject: "s", HtmlBody: "b"}, nil, codes.InvalidArgument},
		{"missing subject", &notifierpb.SendEmailRequest{RecipientEmail: "u@e.com", HtmlBody: "b"}, nil, codes.InvalidArgument},
		{"missing html_body", &notifierpb.SendEmailRequest{RecipientEmail: "u@e.com", Subject: "s"}, nil, codes.InvalidArgument},
		{"transient smtp", validReq, timeoutErr{}, codes.Unavailable},
		{"transient connrefused", validReq, fmt.Errorf("dial: %w", syscall.ECONNREFUSED), codes.Unavailable},
		{"transient connreset", validReq, fmt.Errorf("write: %w", syscall.ECONNRESET), codes.Unavailable},
		{"transient epipe", validReq, fmt.Errorf("write: %w", syscall.EPIPE), codes.Unavailable},
		{"permanent smtp", validReq, errors.New("550 mailbox unavailable"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewGRPCServer(&stubMailer{err: tt.mailerErr})
			_, err := srv.SendEmail(context.Background(), tt.req)
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("status code = %v, want %v (err=%v)", got, tt.wantCode, err)
			}
		})
	}
}
