//go:build e2e

// Package harness boots the full subscribe-saga topology in-process — orchestrator,
// subscription service, catalog, and notifier — against ephemeral Postgres (one per
// service) + Mailpit + NATS containers, for use by e2e tests. No browser.
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
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/confirmationconsumer"
	appeventpublisher "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/eventpublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/natspublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/releaseconsumer"
	apprepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	appsaga "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/saga"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service"
	catalogrepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/repository"
	catalogsaga "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/saga"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	orchapi "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/api"
	orchestratorrepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/repository"
	orchservice "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"

	appdb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"
	catalogdb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/github"
	orchestratordb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/db"
)

const (
	pgImage           = "postgres:16-alpine"
	mailpitImage      = "axllent/mailpit:v1.20"
	natsImage         = "nats:2.10-alpine"
	containerStartTTL = 120 * time.Second
)

// Options is reserved for future per-suite tuning. Currently empty.
type Options struct{}

// Harness holds the live services + their dependencies and exposes the URLs and
// DBs tests need. Cleanup is registered via t.Cleanup.
type Harness struct {
	OrchestratorURL string // POST /subscribe
	AppURL          string // confirm/unsubscribe/list
	MailpitURL      string
	SubDB           *gorm.DB
	CatDB           *gorm.DB
	OrchDB          *gorm.DB
	GitHub          *GitHubFixture

	containers map[string]testcontainers.Container
	cleanups   []func()
}

// New boots the containers + the four in-process services and returns a
// ready-to-use Harness. Cleanup is registered with t.
func New(t *testing.T, _ ...Options) *Harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithTimeout(context.Background(), containerStartTTL)
	defer cancel()

	natsC, natsURL := startNATS(t, ctx)
	mailC, smtp, mailpitURL := startMailpit(t, ctx)
	subC, subDB := startMigratedPG(t, ctx, appdb.Migrate)
	catC, catDB := startMigratedPG(t, ctx, catalogdb.Migrate)
	orchC, orchDB := startMigratedPG(t, ctx, orchestratordb.Migrate)

	nc, js, err := natsbus.Connect(natsURL)
	require.NoError(t, err)
	require.NoError(t, natsbus.EnsureStreams(ctx, js))

	ghFix := newGitHubFixture()
	githubClient := githubclient.NewClient(
		&githubclient.Config{RequestTimeout: 10 * time.Second},
		githubclient.WithBaseURL(ghFix.URL()),
	)

	h := &Harness{
		MailpitURL: mailpitURL,
		SubDB:      subDB, CatDB: catDB, OrchDB: orchDB,
		GitHub:     ghFix,
		containers: map[string]testcontainers.Container{"nats": natsC, "mailpit": mailC, "pg-subscription": subC, "pg-catalog": catC, "pg-orchestrator": orchC},
	}

	// notifier: real consumer → Mailpit SMTP, so e2e exercises publish → NATS → SMTP.
	mailer := notifier.NewSMTPMailer(&notifier.Config{
		Host: smtp.host, Port: smtp.port, Username: "harness@example.com", Password: "harness",
	})
	notifierConsume, err := notifier.Subscribe(context.Background(), js, 5, 30*time.Second, notifier.NewHandler(mailer))
	require.NoError(t, err)

	// HTTP servers (app + orchestrator) — listeners first so the app URL is known
	// before the email composer (which builds confirm/unsub links) is wired.
	appSrv, appURL, appListener := newServer(t)
	orchSrv, orchURL, orchListener := newServer(t)
	h.AppURL, h.OrchestratorURL = appURL, orchURL

	catCleanup := h.wireCatalog(t, nc, js, catalogrepo.New(catDB), githubClient)
	// Email links point at the orchestrator (it serves the confirm/unsubscribe pages).
	appRouter := h.wireSubscription(t, nc, js, apprepo.New(subDB), orchURL)
	coord := orchservice.NewCoordinator(
		orchservice.NewNATSParticipants(nc, 5*time.Second),
		orchservice.NewNATSConfirmationPublisher(js),
		orchestratorrepo.New(orchDB),
		orchservice.UUIDGen{},
	)
	subsClient := orchservice.NewSubscriptionClient(nc, 5*time.Second)

	appSrv.Handler = appRouter
	orchSrv.Handler = orchapi.NewRouter(orchapi.NewHTTPHandler(coord, subsClient))
	serve(appSrv, appListener)
	serve(orchSrv, orchListener)

	h.cleanups = append(h.cleanups,
		func() { _ = appSrv.Close() },
		func() { _ = orchSrv.Close() },
		catCleanup,
		notifierConsume.Stop,
		func() { _ = nc.Drain() },
		ghFix.close,
	)

	waitForHealth(t, appURL)
	waitForHealth(t, orchURL)
	t.Cleanup(h.shutdown)
	return h
}

// wireCatalog registers the catalog saga handlers + the subscription.removed
// cleanup consumer. Returns a cleanup for the consumer.
func (h *Harness) wireCatalog(t *testing.T, nc *nats.Conn, js jetstream.JetStream, repo *catalogrepo.Repository, gh catalogsaga.RepoValidator) func() {
	t.Helper()
	handler := catalogsaga.NewHandler(repo, gh)
	_, err := natsbus.RespondJSON(nc, saga.SubjCatalogRegister, saga.QueueCatalog, handler.Register)
	require.NoError(t, err)
	_, err = natsbus.RespondJSON(nc, saga.SubjCatalogRelease, saga.QueueCatalog, handler.Release)
	require.NoError(t, err)
	removed, err := natsbus.Consume(context.Background(), js, natsbus.ConsumerConfig{
		Stream: saga.EventsStreamName, Durable: saga.DurableRemovedConsumer,
		FilterSubject: saga.SubjSubscriptionRemoved, MaxDeliver: 5, AckWait: 30 * time.Second,
	}, handler.OnSubscriptionRemoved)
	require.NoError(t, err)
	return removed.Stop
}

// wireSubscription registers the subscription service's NATS handlers (create +
// confirm/unsubscribe commands, release/confirmation consumers) and returns its
// HTTP router (the list endpoint). baseURL is where email links point.
func (h *Harness) wireSubscription(t *testing.T, nc *nats.Conn, js jetstream.JetStream, repo *apprepo.Repository, baseURL string) *gin.Engine {
	t.Helper()
	emailNotifier := service.NewEmailNotifier(baseURL, natspublisher.New(js))
	svc := service.New(repo, repo, appeventpublisher.New(js))
	sagaHandler := appsaga.NewHandler(repo)
	cmdHandler := appsaga.NewCommandHandler(svc)

	_, err := natsbus.RespondJSON(nc, saga.SubjSubscriptionCreate, saga.QueueSubscription, sagaHandler.Create)
	require.NoError(t, err)
	_, err = natsbus.RespondJSON(nc, saga.SubjSubscriptionConfirm, saga.QueueSubscription, cmdHandler.Confirm)
	require.NoError(t, err)
	_, err = natsbus.RespondJSON(nc, saga.SubjSubscriptionUnsubscribe, saga.QueueSubscription, cmdHandler.Unsubscribe)
	require.NoError(t, err)

	rel := releaseconsumer.New(repo, emailNotifier)
	_, err = natsbus.Consume(context.Background(), js, natsbus.ConsumerConfig{
		Stream: saga.EventsStreamName, Durable: saga.DurableReleaseConsumer,
		FilterSubject: saga.SubjReleaseDetected, MaxDeliver: 5, AckWait: 30 * time.Second,
	}, rel.Handle)
	require.NoError(t, err)

	conf := confirmationconsumer.New(emailNotifier)
	_, err = natsbus.Consume(context.Background(), js, natsbus.ConsumerConfig{
		Stream: saga.EventsStreamName, Durable: saga.DurableConfirmationConsumer,
		FilterSubject: saga.SubjConfirmationRequested, MaxDeliver: 5, AckWait: 30 * time.Second,
	}, conf.Handle)
	require.NoError(t, err)

	return api.NewRouter(api.NewHandler(svc), "")
}

func (h *Harness) shutdown() {
	for _, fn := range h.cleanups {
		fn()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, c := range h.containers {
		_ = c.Terminate(ctx)
	}
}

// TruncateDB clears mutable rows from all three service DBs between tests.
func (h *Harness) TruncateDB(t *testing.T) {
	t.Helper()
	require.NoError(t, h.SubDB.Exec("TRUNCATE confirmation_tokens, subscriptions RESTART IDENTITY CASCADE").Error)
	require.NoError(t, h.CatDB.Exec("TRUNCATE repo_registrations, watched_repos RESTART IDENTITY CASCADE").Error)
	require.NoError(t, h.OrchDB.Exec("TRUNCATE saga_log").Error)
}

// ResetMailpit deletes all captured messages from Mailpit.
func (h *Harness) ResetMailpit(t *testing.T) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, h.MailpitURL+"/api/v1/messages", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

type smtpAddr struct {
	host string
	port string
}

func newServer(t *testing.T) (*http.Server, string, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	return srv, fmt.Sprintf("http://127.0.0.1:%d", port), listener
}

func serve(srv *http.Server, listener net.Listener) {
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("harness http: %v", err)
		}
	}()
}

func startMigratedPG(t *testing.T, ctx context.Context, migrate func(*gorm.DB) error) (testcontainers.Container, *gorm.DB) {
	t.Helper()
	c, err := postgres.Run(
		ctx, pgImage,
		postgres.WithDatabase("e2e"), postgres.WithUsername("e2e"), postgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := appdb.NewPostgres(&appdb.Config{URL: dsn})
	require.NoError(t, err)
	require.NoError(t, migrate(db))
	return c, db
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
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	require.NoError(t, err)

	host, err := c.Host(ctx)
	require.NoError(t, err)
	smtp, err := c.MappedPort(ctx, "1025")
	require.NoError(t, err)
	httpPort, err := c.MappedPort(ctx, "8025")
	require.NoError(t, err)
	return c, smtpAddr{host: host, port: smtp.Port()}, fmt.Sprintf("http://%s:%s", host, httpPort.Port())
}

func startNATS(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	t.Helper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        natsImage,
			Cmd:          []string{"-js", "-m", "8222"},
			ExposedPorts: []string{"4222/tcp"},
			WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
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
	t.Fatalf("service never became healthy at %s", baseURL)
}
