package notifier

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

// mimeBoundary delimits the multipart/alternative parts. It must never appear in
// a body; our templated emails never contain it.
const mimeBoundary = "rrn_boundary_4d6f8a2e9b"

type Mailer interface {
	Send(ctx context.Context, msg notify.EmailCommand) error
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

func (m *SMTPMailer) Send(ctx context.Context, msg notify.EmailCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	to := msg.RecipientEmail
	body := buildMIME(m.username, msg)
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

func buildMIME(from string, msg notify.EmailCommand) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.RecipientEmail)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	for k, v := range msg.Headers {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	fmt.Fprint(&b, "MIME-Version: 1.0\r\n")

	// HTML-only when there's no plaintext alternative; otherwise
	// multipart/alternative (plain + HTML), which clients prefer.
	if msg.PlainBody == "" {
		fmt.Fprint(&b, "Content-Type: text/html; charset=UTF-8\r\n")
		fmt.Fprint(&b, "Content-Transfer-Encoding: 8bit\r\n\r\n")
		fmt.Fprintf(&b, "%s\r\n", msg.HTMLBody)
		return []byte(b.String())
	}

	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mimeBoundary)
	writePart := func(contentType, body string) {
		fmt.Fprintf(&b, "--%s\r\n", mimeBoundary)
		fmt.Fprintf(&b, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		fmt.Fprint(&b, "Content-Transfer-Encoding: 8bit\r\n\r\n")
		fmt.Fprintf(&b, "%s\r\n", body)
	}
	writePart("text/plain", msg.PlainBody)
	writePart("text/html", msg.HTMLBody)
	fmt.Fprintf(&b, "--%s--\r\n", mimeBoundary)
	return []byte(b.String())
}
