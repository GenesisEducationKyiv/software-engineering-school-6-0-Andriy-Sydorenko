//go:build e2e

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
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/api"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/subscription"
)

const (
	DefaultAPIKey     = "test-key"
	pgImage           = "postgres:16-alpine"
	mailpitImage      = "axllent/mailpit:v1.20"
	containerStartTTL = 90 * time.Second
)

type Harness struct {
	BaseURL        string
	BrowserBaseURL string
	BrowserWSURL   string
	MailpitURL     string
	APIKey         string
	DB             *gorm.DB
	GitHub         *GitHubFixture

	pgC      testcontainers.Container
	mailC    testcontainers.Container
	browserC testcontainers.Container
	srv      *http.Server
}

type Options struct {
	GHValidator subscription.RepoValidator
	APIKey      string
}

// New boots Postgres + Mailpit containers and a fresh in-process app,
// returning a ready-to-use Harness. Cleanup is registered with t.
func New(t *testing.T, opts ...Options) *Harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithTimeout(context.Background(), containerStartTTL)
	defer cancel()

	pgC := startPostgres(t, ctx)
	mailC, smtpAddr, mailpitURL := startMailpit(t, ctx)

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
			&githubclient.Config{
				Timeout: 10 * time.Second,
				BaseURL: ghFix.URL(),
			},
		)
	}

	repo := subscription.NewRepository(db)
	note := notifier.New(
		&notifier.Config{
			Host:     smtpAddr.host,
			Port:     smtpAddr.port,
			Username: "harness@example.com",
			Password: "harness",
			BaseURL:  baseURL,
		},
	)
	svc := subscription.New(repo, gh, note, subscription.RandomToken)
	router := api.NewRouter(api.NewHandler(svc), o.APIKey)

	srv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("harness http server: %v", err)
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
		browserC:       browserC,
		srv:            srv,
	}
	waitForHealth(t, h.BaseURL)
	t.Cleanup(h.shutdown)
	return h
}

func (h *Harness) TruncateDB(t *testing.T) {
	t.Helper()
	require.NoError(t, h.DB.Exec("TRUNCATE TABLE subscriptions RESTART IDENTITY CASCADE").Error)
}

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
	if h.GitHub != nil {
		h.GitHub.close()
	}
	_ = h.pgC.Terminate(shutdownCtx)
	_ = h.mailC.Terminate(shutdownCtx)
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
