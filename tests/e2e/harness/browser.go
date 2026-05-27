//go:build e2e

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	chromiumImage   = "chromedp/headless-shell:stable"
	browserStartTTL = 60 * time.Second
	chromiumCDPPort = "9222"
)

func startBrowser(t testing.TB, ctx context.Context, appPort int) (testcontainers.Container, string) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:           chromiumImage,
		ExposedPorts:    []string{chromiumCDPPort + "/tcp"},
		HostAccessPorts: []int{appPort},
		WaitingFor: wait.ForHTTP("/json/version").
			WithPort(chromiumCDPPort + "/tcp").
			WithStartupTimeout(browserStartTTL),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, chromiumCDPPort)
	require.NoError(t, err)

	wsURL, err := discoverCDPWebSocket(host, port.Port())
	require.NoError(t, err)
	return c, wsURL
}

// discoverCDPWebSocket fetches /json/version and rewrites the host:port
// in webSocketDebuggerUrl (Chromium advertises its container-internal
// 127.0.0.1:9222, which the host can't dial).
func discoverCDPWebSocket(host, port string) (string, error) {
	endpoint := host + ":" + port
	resp, err := http.Get("http://" + endpoint + "/json/version")
	if err != nil {
		return "", fmt.Errorf("get /json/version: %w", err)
	}
	defer resp.Body.Close()
	var v struct {
		WebSocketDebuggerUrl string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", fmt.Errorf("decode /json/version: %w", err)
	}
	const wsPrefix = "ws://"
	if !strings.HasPrefix(v.WebSocketDebuggerUrl, wsPrefix) {
		return "", fmt.Errorf("unexpected ws url: %s", v.WebSocketDebuggerUrl)
	}
	rest := v.WebSocketDebuggerUrl[len(wsPrefix):]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[i:]
	}
	return wsPrefix + endpoint + rest, nil
}
