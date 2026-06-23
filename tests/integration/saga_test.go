//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/gorm"

	appdomain "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	apprepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	appsaga "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/saga"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog"
	catalogrepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator"
	orchestratorrepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"

	appdb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"
	catalogdb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/db"
	orchestratordb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/db"
)

// stubValidator is the saga harness's GitHub boundary; tests flip its error to
// drive the bad-repo / rate-limit paths through the real Catalog handler.
type stubValidator struct {
	mu  sync.Mutex
	err error
}

func (s *stubValidator) ValidateRepo(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stubValidator) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

type sagaInfra struct {
	nc                   *nats.Conn
	js                   jetstream.JetStream
	orchDB, subDB, catDB *gorm.DB
	github               *stubValidator
}

var (
	sagaOnce  sync.Once
	sagaShare *sagaInfra
	sagaErr   error
)

func mustSagaInfra(t *testing.T) *sagaInfra {
	t.Helper()
	sagaOnce.Do(func() { sagaShare, sagaErr = setupSagaInfra() })
	if sagaErr != nil {
		t.Fatalf("saga infra setup: %v", sagaErr)
	}
	return sagaShare
}

func setupSagaInfra() (*sagaInfra, error) {
	natsURL, err := startNATSContainer()
	if err != nil {
		return nil, fmt.Errorf("nats: %w", err)
	}
	nc, js, err := natsbus.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	if err := natsbus.EnsureStreams(context.Background(), js); err != nil {
		return nil, fmt.Errorf("ensure streams: %w", err)
	}

	orchDB, err := startMigratedPG(orchestratordb.Migrate)
	if err != nil {
		return nil, fmt.Errorf("orchestrator db: %w", err)
	}
	subDB, err := startMigratedPG(appdb.Migrate)
	if err != nil {
		return nil, fmt.Errorf("subscription db: %w", err)
	}
	catDB, err := startMigratedPG(catalogdb.Migrate)
	if err != nil {
		return nil, fmt.Errorf("catalog db: %w", err)
	}

	github := &stubValidator{}

	// Catalog participant handlers.
	catHandler := catalog.NewHandler(catalogrepo.New(catDB), github)
	if _, err := natsbus.RespondJSON(nc, saga.SubjCatalogRegister, saga.QueueCatalog, catHandler.Register); err != nil {
		return nil, err
	}
	if _, err := natsbus.RespondJSON(nc, saga.SubjCatalogRelease, saga.QueueCatalog, catHandler.Release); err != nil {
		return nil, err
	}

	// Subscription participant handlers.
	subHandler := appsaga.NewHandler(apprepo.New(subDB))
	if _, err := natsbus.RespondJSON(nc, saga.SubjSubscriptionCreate, saga.QueueSubscription, subHandler.Create); err != nil {
		return nil, err
	}
	if _, err := natsbus.RespondJSON(nc, saga.SubjSubscriptionCancel, saga.QueueSubscription, subHandler.Cancel); err != nil {
		return nil, err
	}

	return &sagaInfra{nc: nc, js: js, orchDB: orchDB, subDB: subDB, catDB: catDB, github: github}, nil
}

func (s *sagaInfra) newCoordinator() *orchestrator.Coordinator {
	return orchestrator.NewCoordinator(
		orchestrator.NewNATSParticipants(s.nc, 5*time.Second),
		orchestrator.NewNATSConfirmationPublisher(s.js),
		orchestratorrepo.New(s.orchDB),
		orchestrator.UUIDGen{},
	)
}

func (s *sagaInfra) reset(t *testing.T) {
	t.Helper()
	require.NoError(t, s.subDB.Exec(`TRUNCATE confirmation_tokens, subscriptions RESTART IDENTITY CASCADE`).Error)
	require.NoError(t, s.catDB.Exec(`TRUNCATE repo_registrations, watched_repos RESTART IDENTITY CASCADE`).Error)
	require.NoError(t, s.orchDB.Exec(`TRUNCATE saga_log`).Error)
	s.github.setErr(nil)
}

func (s *sagaInfra) sagaState(t *testing.T) string {
	t.Helper()
	var state string
	require.NoError(t, s.orchDB.Raw(`SELECT state FROM saga_log ORDER BY created_at DESC LIMIT 1`).Scan(&state).Error)
	return state
}

func count(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(query, args...).Scan(&n).Error)
	return n
}

func startMigratedPG(migrate func(*gorm.DB) error) (*gorm.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c, err := tcpg.Run(
		ctx, "postgres:16-alpine",
		tcpg.WithDatabase("test"), tcpg.WithUsername("test"), tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(), tcpg.WithSQLDriver("pgx"),
	)
	if err != nil {
		return nil, err
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}
	db, err := openWithRetry(dsn, 30, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

func startNATSContainer() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

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
	if err != nil {
		return "", err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return "", err
	}
	port, err := c.MappedPort(ctx, "4222")
	if err != nil {
		return "", err
	}
	return "nats://" + host + ":" + port.Port(), nil
}

func TestSaga_HappyPath(t *testing.T) {
	infra := mustSagaInfra(t)
	infra.reset(t)
	ctx := context.Background()

	require.NoError(t, infra.newCoordinator().Subscribe(ctx, "alice@example.com", "golang/go"))

	require.Equal(t, int64(1), count(t, infra.subDB,
		`SELECT COUNT(*) FROM subscriptions WHERE email=? AND confirmed=false AND public_id IS NOT NULL`, "alice@example.com"))
	require.Equal(t, int64(1), count(t, infra.subDB, `SELECT COUNT(*) FROM confirmation_tokens`))
	require.Equal(t, int64(1), count(t, infra.catDB, `SELECT COUNT(*) FROM repo_registrations WHERE repo=?`, "golang/go"))
	require.Equal(t, "DONE", infra.sagaState(t)) // DONE ⟹ the confirmation event was published
}

func TestSaga_BadRepo_Aborts(t *testing.T) {
	infra := mustSagaInfra(t)
	infra.reset(t)
	infra.github.setErr(catalog.ErrRepoNotFound)
	ctx := context.Background()

	err := infra.newCoordinator().Subscribe(ctx, "a@example.com", "ghost/ghost")

	require.ErrorIs(t, err, orchestrator.ErrRepoNotFound)
	require.Equal(t, int64(0), count(t, infra.subDB, `SELECT COUNT(*) FROM subscriptions`))
	require.Equal(t, int64(0), count(t, infra.catDB, `SELECT COUNT(*) FROM repo_registrations`))
	require.Equal(t, "ABORTED", infra.sagaState(t))
}

func TestSaga_CreateFails_Compensates(t *testing.T) {
	infra := mustSagaInfra(t)
	infra.reset(t)
	ctx := context.Background()

	// A different holder already owns (email, repo): the saga's create will conflict.
	existing := &appdomain.Subscription{
		PublicID: uuid.NewString(), Email: "dup@example.com", Repo: "golang/go", UnsubscribeToken: uuid.NewString(),
	}
	require.NoError(t, infra.subDB.Create(existing).Error)

	err := infra.newCoordinator().Subscribe(ctx, "dup@example.com", "golang/go")

	require.ErrorIs(t, err, orchestrator.ErrAlreadySubscribed)
	// Register ran then the pivot failed → ReleaseRepo compensated it away.
	require.Equal(t, int64(0), count(t, infra.catDB, `SELECT COUNT(*) FROM repo_registrations WHERE repo=?`, "golang/go"))
	require.Equal(t, "COMPENSATED", infra.sagaState(t))
}

func TestSaga_CrashAfterCommit_RecoverRepublishes(t *testing.T) {
	infra := mustSagaInfra(t)
	infra.reset(t)
	ctx := context.Background()

	// Simulate a crash that left the saga COMMITTED but never sent the confirmation.
	store := orchestratorrepo.New(infra.orchDB)
	rec := &orchestrator.SagaRecord{
		SagaID: uuid.NewString(), State: orchestrator.StateCommitted, SubscriptionID: uuid.NewString(),
		Payload: orchestrator.SagaPayload{Email: "bob@example.com", Repo: "golang/go", ConfirmToken: "c", UnsubToken: "u"},
	}
	require.NoError(t, store.Create(ctx, rec))

	require.NoError(t, infra.newCoordinator().Recover(ctx))

	// Recovery rolled forward: confirmation re-published, saga finished.
	require.Equal(t, "DONE", infra.sagaState(t))
}
