//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifierclient"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

const internalToken = "integration-token"

// mailpitContainer starts Mailpit and returns its SMTP host:port and HTTP base URL.
func mailpitContainer(t *testing.T) (smtpHost, smtpPort, httpURL string) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("8025/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err)
	smtpMapped, err := c.MappedPort(ctx, "1025")
	require.NoError(t, err)
	httpMapped, err := c.MappedPort(ctx, "8025")
	require.NoError(t, err)
	return host, smtpMapped.Port(), fmt.Sprintf("http://%s:%s", host, httpMapped.Port())
}

// startNotifier stands up a real notifier gRPC server over Core and returns the dial addr.
func startNotifier(t *testing.T, core *notifier.Core) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := platform.NewServer(internalToken)
	pb.RegisterNotifierServiceServer(srv, notifier.NewGRPCServer(core))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return lis.Addr().String()
}

func TestNotifierGRPC_SendConfirmation_deliversToMailpit(t *testing.T) {
	smtpHost, smtpPort, httpURL := mailpitContainer(t)

	core := notifier.NewCore(&notifier.Config{
		Host:     smtpHost,
		Port:     smtpPort,
		Username: "notify@example.com", // Mailpit accepts any creds
		Password: "x",
		BaseURL:  "https://notify.example.com",
	})
	addr := startNotifier(t, core)

	conn, err := platform.Dial(addr, internalToken)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := notifierclient.NewAdapter(pb.NewNotifierServiceClient(conn))

	to := "subscriber@example.com"
	require.NoError(t, client.SendConfirmation(context.Background(), to, "golang/go", "ctok", "utok"))

	body := waitForMailpit(t, httpURL, to, 10*time.Second)
	assert.Contains(t, body, "/api/confirm/ctok")
	assert.Contains(t, body, "golang/go")
}

func TestNotifierGRPC_SendReleaseNotifications_batchCountsRoundTrip(t *testing.T) {
	// Stubbed mailer: assert the batch wire round-trip + ack mapping, not SMTP.
	core := notifier.NewCoreWithMailer("https://notify.example.com", stubOKMailer{})
	addr := startNotifier(t, core)

	conn, err := platform.Dial(addr, internalToken)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := notifierclient.NewAdapter(pb.NewNotifierServiceClient(conn))

	err = client.SendReleaseNotifications(context.Background(), "golang/go", "v1.24.0", "https://n",
		[]notifierclient.Recipient{
			{Email: "a@x.com", UnsubscribeToken: "u1"},
			{Email: "b@x.com", UnsubscribeToken: "u2"},
		})
	require.NoError(t, err)
}

// stubOKMailer always succeeds — exercises transport, not SMTP.
type stubOKMailer struct{}

func (stubOKMailer) Send(_ context.Context, _ notifier.Message) error { return nil }

func waitForMailpit(t *testing.T, httpURL, to string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		searchURL := fmt.Sprintf("%s/api/v1/search?query=%s", httpURL, url.QueryEscape("to:"+to))
		resp, err := http.Get(searchURL)
		require.NoError(t, err)
		var search struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&search))
		_ = resp.Body.Close()
		if len(search.Messages) > 0 {
			id := search.Messages[0].ID
			mr, err := http.Get(httpURL + "/api/v1/message/" + id)
			require.NoError(t, err)
			var full struct {
				Text string `json:"Text"`
				HTML string `json:"HTML"`
			}
			require.NoError(t, json.NewDecoder(mr.Body).Decode(&full))
			_ = mr.Body.Close()
			if full.Text != "" {
				return full.Text
			}
			return full.HTML
		}
		if time.Now().After(deadline) {
			t.Fatalf("mailpit: no message to %s within %s", to, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
