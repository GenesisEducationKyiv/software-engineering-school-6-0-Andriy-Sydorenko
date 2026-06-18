//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/natspublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

// recordingMailer is a notifier.Mailer that records recipients (or fails on demand).
type recordingMailer struct {
	mu   sync.Mutex
	sent []string
	fail bool
}

func (m *recordingMailer) Send(_ context.Context, to, _, _ string) error {
	if m.fail {
		return errors.New("smtp down")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to)
	return nil
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func startNATS(t *testing.T, ctx context.Context) string {
	t.Helper()
	c, err := testcontainers.GenericContainer(
		ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "nats:2.10-alpine",
				Cmd:          []string{"-js", "-m", "8222"},
				ExposedPorts: []string{"4222/tcp"},
				WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(30 * time.Second),
			},
			Started: true,
		},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "4222")
	require.NoError(t, err)
	return "nats://" + host + ":" + port.Port()
}

func TestConsumerSendsDedupsAndDLQs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	url := startNATS(t, ctx)
	nc, js, err := natsbus.Connect(url)
	require.NoError(t, err)
	defer nc.Drain() //nolint:errcheck
	require.NoError(t, natsbus.EnsureStreams(ctx, js))

	mailer := &recordingMailer{}
	cc, err := notifier.Subscribe(ctx, js, 2, time.Second, notifier.NewHandler(mailer))
	require.NoError(t, err)
	defer cc.Stop()

	pub := natspublisher.New(js)
	cmd := notify.EmailCommand{RecipientEmail: "a@x.com", Subject: "S", HTMLBody: "<p>b</p>"}

	// Happy path + dedup: the SAME dedup id published twice → exactly one send.
	require.NoError(t, pub.Publish(ctx, notify.SubjectConfirmation, "dup-1", cmd))
	require.NoError(t, pub.Publish(ctx, notify.SubjectConfirmation, "dup-1", cmd))
	require.Eventually(
		t,
		func() bool { return mailer.count() == 1 },
		5*time.Second,
		50*time.Millisecond,
	)
	require.Never(
		t,
		func() bool { return mailer.count() > 1 },
		500*time.Millisecond,
		50*time.Millisecond,
		"dedup should prevent a second send",
	)

	// DLQ: a malformed payload is permanent → dead-lettered, never sent.
	_, err = js.Publish(
		ctx,
		notify.SubjectRelease,
		[]byte("{bad json"),
		jetstream.WithMsgID("bad-1"),
	)
	require.NoError(t, err)

	dlq, err := js.CreateOrUpdateConsumer(
		ctx, notify.DLQStreamName, jetstream.ConsumerConfig{
			AckPolicy: jetstream.AckExplicitPolicy,
		},
	)
	require.NoError(t, err)
	msgs, err := dlq.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	require.NoError(t, err)
	got := 0
	for range msgs.Messages() {
		got++
	}
	require.Equal(t, 1, got, "malformed message should land in the DLQ")
	require.Equal(t, 1, mailer.count(), "malformed message must not be sent")
}
