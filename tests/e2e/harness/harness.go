//go:build e2e

// Package harness boots an in-process app instance against ephemeral
// Postgres + Mailpit + NATS containers, for use by e2e tests.
package harness

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/api"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/natspublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
)

const (
	// DefaultAPIKey is the value tests opt into when they want the API-key
	// middleware actually enforced (Options.APIKey). Default is empty, which
	// makes the middleware bypass — matches the unprotected dev/staging path.
	DefaultAPIKey     = "test-key"
	pgImage           = "postgres:16-alpine"
	mailpitImage      = "axllent/mailpit:v1.20"
	natsImage         = "nats:2.10-alpine"
	containerStartTTL = 90 * time.Second
)

// Harness holds the live app + its dependencies and exposes the URLs tests
// need. All cleanup is registered via t.Cleanup.
type Harness struct {
	BaseURL        string // app under test, reachable from the host (127.0.0.1)
	BrowserBaseURL string // same app, reachable from inside the browser container
	BrowserWSURL   string // CDP websocket endpoint for ConnectOverCDP
	MailpitURL     string // Mailpit HTTP API root (http://host:port)
	APIKey         string
	DB             *gorm.DB
	GitHub         *GitHubFixture // nil when Options.GHValidator overrides it

	pgC      testcontainers.Container
	mailC    testcontainers.Container
	natsC    testcontainers.Container
	browserC testcontainers.Container
	srv      *http.Server
	natsConn *nats.Conn
	consume  jetstream.ConsumeContext
}

// Options configures optional substitutions. Zero value = sensible defaults
// (real GitHub client pointed at an in-process httptest fixture; see GitHub).
type Options struct {
	// GHValidator, when set, completely replaces the default GitHub
	// fixture + real-client wiring. Tests that need the programmable
	// fixture should leave this nil and use Harness.GitHub.SetBehavior.
	GHValidator service.RepoValidator

	// APIKey, when non-empty, enables the API-key middleware on protected
	// routes. Default empty = middleware bypasses (matches unprotected
	// dev/staging behavior and lets browser-form tests work).
	APIKey string
}

// New boots Postgres + Mailpit + NATS containers and a fresh in-process app,
// returning a ready-to-use Harness. Cleanup is registered with t.
func New(t *testing.T, opts ...Options) *Harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithTimeout(context.Background(), containerStartTTL)
	defer cancel()

	pgC := startPostgres(t, ctx)
	mailC, smtpAddr, mailpitURL := startMailpit(t, ctx)
	natsC, natsURL := startNATS(t, ctx)

	dbURL, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := database.NewPostgres(&database.Config{URL: dbURL})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	// Bind to 0.0.0.0 so the sidecar browser container can reach back
	// via host.testcontainers.internal. Host-side code still uses
	// 127.0.0.1:<port> through BaseURL.
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	appPort := listener.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", appPort)
	browserBaseURL := fmt.Sprintf("http://host.testcontainers.internal:%d", appPort)

	browserC, wsURL := startBrowser(t, ctx, appPort)

	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	var ghFix *GitHubFixture
	gh := o.GHValidator
	if gh == nil {
		ghFix = newGitHubFixture()
		gh = githubclient.NewClient(
			&githubclient.Config{RequestTimeout: 10 * time.Second},
			githubclient.WithBaseURL(ghFix.URL()),
		)
	}

	repo := repository.New(db)

	// Real notifier consumer, in-process, sending through Mailpit — so e2e
	// exercises the actual publish → NATS → SMTP path, not a shortcut.
	mailer := notifier.NewSMTPMailer(&notifier.Config{
		Host:     smtpAddr.host,
		Port:     smtpAddr.port,
		Username: "harness@example.com",
		Password: "harness",
	})
	natsConn, js, err := natsbus.Connect(natsURL)
	require.NoError(t, err)
	require.NoError(t, natsbus.EnsureStreams(ctx, js))
	// context.Background(): the consumer must outlive New()'s startup ctx;
	// it is stopped via consume.Stop() in shutdown().
	consume, err := notifier.Subscribe(context.Background(), js, 5, 30*time.Second, notifier.NewHandler(mailer))
	require.NoError(t, err)

	note := service.NewEmailNotifier(baseURL, natspublisher.New(js))
	svc := service.New(repo, repo, gh, note, service.RandomToken)
	router := api.NewRouter(api.NewHandler(svc), o.APIKey)

	srv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		// log.Printf, not t.Logf: this goroutine can outlive the test (nothing
		// joins it on shutdown), and t.Logf after the test finishes panics.
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("harness http app: %v", err)
		}
	}()

	h := &Harness{
		BaseURL:        baseURL,
		BrowserBaseURL: browserBaseURL,
		BrowserWSURL:   wsURL,
		MailpitURL:     mailpitURL,
		APIKey:         o.APIKey,
		DB:             db,
		GitHub:         ghFix,
		pgC:            pgC,
		mailC:          mailC,
		natsC:          natsC,
		browserC:       browserC,
		srv:            srv,
		natsConn:       natsConn,
		consume:        consume,
	}
	waitForHealth(t, h.BaseURL)
	t.Cleanup(h.shutdown)
	return h
}

// TruncateDB clears subscription rows between tests. Test suites that share a
// Harness should call this in SetupTest.
func (h *Harness) TruncateDB(t *testing.T) {
	t.Helper()
	require.NoError(t, h.DB.Exec("TRUNCATE TABLE subscriptions RESTART IDENTITY CASCADE").Error)
}

// ResetMailpit deletes all captured messages from Mailpit.
func (h *Harness) ResetMailpit(t *testing.T) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, h.MailpitURL+"/api/v1/messages", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

func (h *Harness) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = h.srv.Shutdown(shutdownCtx)
	h.consume.Stop()
	_ = h.natsConn.Drain()
	if h.GitHub != nil {
		h.GitHub.close()
	}
	_ = h.pgC.Terminate(shutdownCtx)
	_ = h.mailC.Terminate(shutdownCtx)
	_ = h.natsC.Terminate(shutdownCtx)
	if h.browserC != nil {
		_ = h.browserC.Terminate(shutdownCtx)
	}
}

type smtpAddr struct {
	host string
	port string
}

func startPostgres(t *testing.T, ctx context.Context) *postgres.PostgresContainer {
	t.Helper()
	c, err := postgres.Run(
		ctx, pgImage,
		postgres.WithDatabase("e2e"),
		postgres.WithUsername("e2e"),
		postgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	return c
}

func startMailpit(t *testing.T, ctx context.Context) (testcontainers.Container, smtpAddr, string) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        mailpitImage,
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		Env: map[string]string{
			// Make Mailpit advertise SMTP AUTH and accept any creds; the real
			// mailer always sends PLAIN auth and net/smtp errors if unsupported.
			"MP_SMTP_AUTH_ACCEPT_ANY":     "true",
			"MP_SMTP_AUTH_ALLOW_INSECURE": "true",
		},
		WaitingFor: wait.ForHTTP("/api/v1/info").WithPort("8025/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(
		ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		},
	)
	require.NoError(t, err)

	host, err := c.Host(ctx)
	require.NoError(t, err)
	smtp, err := c.MappedPort(ctx, "1025")
	require.NoError(t, err)
	httpPort, err := c.MappedPort(ctx, "8025")
	require.NoError(t, err)

	return c, smtpAddr{host: host, port: smtp.Port()}, fmt.Sprintf(
		"http://%s:%s",
		host,
		httpPort.Port(),
	)
}

func startNATS(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	t.Helper()
	c, err := testcontainers.GenericContainer(
		ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        natsImage,
				Cmd:          []string{"-js", "-m", "8222"},
				ExposedPorts: []string{"4222/tcp"},
				WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(30 * time.Second),
			},
			Started: true,
		},
	)
	require.NoError(t, err)

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "4222")
	require.NoError(t, err)

	return c, fmt.Sprintf("nats://%s:%s", host, port.Port())
}

func waitForHealth(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("harness app never became healthy at %s", baseURL)
}
