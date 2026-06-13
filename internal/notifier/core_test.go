package notifier

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubMailer records sends and can fail for a specific recipient.
type stubMailer struct {
	mu      sync.Mutex
	sent    []Message
	failFor string // recipient address that should error
	sendErr error
}

func (s *stubMailer) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFor != "" && msg.To == s.failFor {
		return errors.New("smtp boom")
	}
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, msg)
	return nil
}

const testConfirmURL = "https://notify.example.com/api/confirm/ctok"
const testUnsubURL = "https://notify.example.com/api/unsubscribe/utok"

func newTestCore(m Mailer) *Core {
	return &Core{composer: NewComposer(), mailer: m}
}

func TestCore_SendConfirmation_ok(t *testing.T) {
	m := &stubMailer{}
	c := newTestCore(m)

	sent, failed, err := c.SendConfirmation(context.Background(), "a@b.com", "golang/go", testConfirmURL, testUnsubURL)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), sent)
	assert.Equal(t, uint32(0), failed)

	require.Len(t, m.sent, 1)
	assert.Equal(t, "a@b.com", m.sent[0].To)
	assert.Contains(t, m.sent[0].PlainBody, testConfirmURL)
}

func TestCore_SendConfirmation_mailerError(t *testing.T) {
	c := newTestCore(&stubMailer{failFor: "a@b.com"})
	sent, failed, err := c.SendConfirmation(context.Background(), "a@b.com", "golang/go", testConfirmURL, testUnsubURL)
	require.NoError(t, err) // failures are counted, not returned
	assert.Equal(t, uint32(0), sent)
	assert.Equal(t, uint32(1), failed)
}

func TestCore_SendReleaseNotifications_batch(t *testing.T) {
	m := &stubMailer{failFor: "bad@x.com"}
	c := newTestCore(m)

	recipients := []Recipient{
		{Email: "good1@x.com", UnsubscribeURL: "u1"},
		{Email: "bad@x.com", UnsubscribeURL: "u2"},
		{Email: "good2@x.com", UnsubscribeURL: "u3"},
	}
	sent, failed, err := c.SendReleaseNotifications(context.Background(), "golang/go", "v1.24.0", "", recipients)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), sent)
	assert.Equal(t, uint32(1), failed)

	require.Len(t, m.sent, 2)
	for _, msg := range m.sent {
		assert.Contains(t, msg.Subject, "v1.24.0")
		assert.Contains(t, msg.Subject, "golang/go")
	}
}

func TestCore_SendReleaseNotifications_empty(t *testing.T) {
	m := &stubMailer{}
	c := newTestCore(m)
	sent, failed, err := c.SendReleaseNotifications(context.Background(), "golang/go", "v1", "", nil)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), sent)
	assert.Equal(t, uint32(0), failed)
	assert.Empty(t, m.sent)
}
