package notifier

import (
	"context"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMIMEStructure(t *testing.T) {
	raw := string(buildMIME(
		"notify@example.com",
		"user@example.com",
		"Confirm your subscription",
		"<p>hello html</p>",
	))

	// SMTP receiver gets these exact bytes — net/mail parsing is the contract.
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	require.NoErrorf(t, err, "mail.ReadMessage failed; raw output:\n%s", raw)

	wantHeaders := map[string]string{
		"From":         "notify@example.com",
		"To":           "user@example.com",
		"Subject":      "Confirm your subscription",
		"MIME-Version": "1.0",
	}
	for k, want := range wantHeaders {
		assert.Equal(t, want, parsed.Header.Get(k), "header %s", k)
	}

	ct := parsed.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/html"), "Content-Type=%q", ct)
	assert.Contains(t, raw, "<p>hello html</p>")
}

func TestBuildMIMEUsesCRLFLineEndings(t *testing.T) {
	// RFC 5322 wire format is CRLF; bare LF gets rejected/mangled by some receivers.
	raw := string(buildMIME("from@x", "to@x", "subject", "<p>html</p>"))
	assert.NotContains(t, strings.ReplaceAll(raw, "\r\n", ""), "\n", "found bare LF")
}

func TestSMTPMailerSendHonoursCancelledContext(t *testing.T) {
	// Pre-cancelled ctx must not even dial — otherwise a stuck SMTP app holds the goroutine.
	m := NewSMTPMailer(&Config{Host: "127.0.0.1", Port: "1", Username: "u", Password: "p"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.Send(ctx, "a@b.com", "subject", "<p>body</p>")
	assert.ErrorIs(t, err, context.Canceled)
}
