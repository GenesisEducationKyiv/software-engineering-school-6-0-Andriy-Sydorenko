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
	msg := Message{
		To:        "user@example.com",
		Subject:   "Confirm your subscription",
		PlainBody: "hello plain",
		HTMLBody:  "<p>hello html</p>",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://x.example/u/tok>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}

	raw := string(buildMIME("notify@example.com", msg))

	// SMTP receiver gets these exact bytes — net/mail parsing is the contract.
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	require.NoErrorf(t, err, "mail.ReadMessage failed; raw output:\n%s", raw)

	wantHeaders := map[string]string{
		"From":                  "notify@example.com",
		"To":                    "user@example.com",
		"Subject":               "Confirm your subscription",
		"MIME-Version":          "1.0",
		"List-Unsubscribe":      "<https://x.example/u/tok>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
	for k, want := range wantHeaders {
		assert.Equal(t, want, parsed.Header.Get(k), "header %s", k)
	}

	ct := parsed.Header.Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "multipart/alternative;"), "Content-Type=%q", ct)
	assert.Contains(t, ct, `boundary="boundary-repo-release-notifier"`)

	assert.Contains(t, raw, "text/plain; charset=UTF-8")
	assert.Contains(t, raw, "text/html; charset=UTF-8")
	assert.Contains(t, raw, "hello plain")
	assert.Contains(t, raw, "<p>hello html</p>")
	// Closing form `--boundary--` — MUAs treat trailing bytes after this as out-of-message.
	assert.Contains(t, raw, "--boundary-repo-release-notifier--\r\n")
}

func TestBuildMIMEUsesCRLFLineEndings(t *testing.T) {
	// RFC 5322 wire format is CRLF; bare LF gets rejected/mangled by some receivers.
	raw := string(
		buildMIME(
			"from@x",
			Message{To: "to@x", Subject: "s", PlainBody: "p", HTMLBody: "h"},
		),
	)
	assert.NotContains(t, strings.ReplaceAll(raw, "\r\n", ""), "\n", "found bare LF")
}

func TestSMTPMailerSendHonoursCancelledContext(t *testing.T) {
	// Pre-cancelled ctx must not even dial — otherwise a stuck SMTP app holds the goroutine.
	m := NewSMTPMailer(&Config{Host: "127.0.0.1", Port: "1", Username: "u", Password: "p"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.Send(ctx, Message{To: "a@b.com"})
	assert.ErrorIs(t, err, context.Canceled)
}
