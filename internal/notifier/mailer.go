package notifier

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

type Mailer interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

type SMTPMailer struct {
	host     string
	port     string
	username string
	password string
}

func NewSMTPMailer(cfg *Config) *SMTPMailer {
	return &SMTPMailer{
		host:     cfg.Host,
		port:     cfg.Port,
		username: cfg.Username,
		password: cfg.Password,
	}
}

func (m *SMTPMailer) Send(ctx context.Context, to, subject, htmlBody string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body := buildMIME(m.username, to, subject, htmlBody)
	addr := fmt.Sprintf("%s:%s", m.host, m.port)
	auth := smtp.PlainAuth("", m.username, m.password, m.host)

	// net/smtp can't be cancelled, so run it off-thread and let the caller
	// return on ctx expiry. The orphaned goroutine drains on the TCP timeout —
	// this frees the handler, it doesn't abort the in-flight send.
	done := make(chan error, 1)
	go func() { done <- smtp.SendMail(addr, auth, m.username, []string{to}, body) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to send email: %w", err)
		}
		return nil
	}
}

func buildMIME(from, to, subject, htmlBody string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprint(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprint(&b, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprint(&b, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n", htmlBody)
	return []byte(b.String())
}
