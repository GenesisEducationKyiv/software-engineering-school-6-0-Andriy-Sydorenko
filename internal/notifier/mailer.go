package notifier

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

type Message struct {
	To        string
	Subject   string
	PlainBody string
	HTMLBody  string
	Headers   map[string]string
}

type Mailer interface {
	Send(ctx context.Context, msg Message) error
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

func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body := buildMIME(m.username, msg)
	addr := fmt.Sprintf("%s:%s", m.host, m.port)
	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	if err := smtp.SendMail(addr, auth, m.username, []string{msg.To}, body); err != nil {
		return fmt.Errorf("failed to send email to %s: %w", msg.To, err)
	}
	return nil
}

// buildMIME assembles a multipart/alternative message.
func buildMIME(from string, msg Message) []byte {
	const boundary = "boundary-repo-release-notifier"

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	for k, v := range msg.Headers {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	fmt.Fprint(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	fmt.Fprint(&b, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprint(&b, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n", msg.PlainBody)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	fmt.Fprint(&b, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprint(&b, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n", msg.HTMLBody)

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String())
}
