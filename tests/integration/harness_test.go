//go:build integration

// Package integration exercises the full HTTP → service → repository
// → Postgres path. The Postgres container is started once per package
// run; the schema is reset between tests via TRUNCATE.
package integration

import (
	"context"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/gorm"

	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service"
)

const testAPIKey = "test-api-key"

var (
	sharedDB     *gorm.DB
	sharedDBOnce sync.Once
	sharedDBErr  error
)

// stubGitHub is the test's GitHub boundary. Tests flip wantErr to
// drive 404/503 paths through the real service+repo wiring.
type stubGitHub struct {
	mu      sync.Mutex
	wantErr error
	calls   int
}

func (s *stubGitHub) ValidateRepo(_ context.Context, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.wantErr
}

func (s *stubGitHub) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wantErr = err
}

func (s *stubGitHub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// stubMailer records every confirmation send so tests can assert on
// what the service handed off, without opening an SMTP connection.
type stubMailer struct {
	mu    sync.Mutex
	sends []sentMail
	err   error
}

type sentMail struct {
	email      string
	repo       string
	token      string
	unsubToken string
}

func (m *stubMailer) SendConfirmation(
	_ context.Context,
	email, repo, token, unsubToken string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sends = append(m.sends, sentMail{email, repo, token, unsubToken})
	return nil
}

func (m *stubMailer) snapshot() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sentMail, len(m.sends))
	copy(out, m.sends)
	return out
}

// testEnv bundles the per-test handles: a fresh router with fresh
// stubs, the shared DB (rows truncated), and the stubs for assertions.
type testEnv struct {
	router *gin.Engine
	db     *gorm.DB
	github *stubGitHub
	mailer *stubMailer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := mustSharedDB(t)
	truncateAll(t, db)

	gh := &stubGitHub{}
	mailer := &stubMailer{}
	repo := repository.New(db)
	svc := service.New(repo, repo, gh, mailer, service.RandomToken)
	router := api.NewRouter(api.NewHandler(svc), testAPIKey)

	return &testEnv{
		router: router,
		db:     db,
		github: gh,
		mailer: mailer,
	}
}

func mustSharedDB(t *testing.T) *gorm.DB {
	t.Helper()
	sharedDBOnce.Do(
		func() {
			sharedDB, sharedDBErr = startPostgres()
		},
	)
	if sharedDBErr != nil {
		t.Fatalf("postgres container setup: %v", sharedDBErr)
	}
	return sharedDB
}

func startPostgres() (*gorm.DB, error) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c, err := tcpg.Run(
		ctx,
		"postgres:16-alpine",
		tcpg.WithDatabase("test"),
		tcpg.WithUsername("test"),
		tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
		tcpg.WithSQLDriver("pgx"),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres: %w", err)
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("dsn: %w", err)
	}

	// Wait for the DB to actually accept queries — BasicWaitStrategies
	// returns once logs say "ready" but a fresh container occasionally
	// still rejects the first connection.
	db, err := openWithRetry(dsn, 30, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	if err := database.Migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

func openWithRetry(dsn string, attempts int, delay time.Duration) (*gorm.DB, error) {
	cfg := &database.Config{URL: dsn}
	var lastErr error
	for i := 0; i < attempts; i++ {
		db, err := database.NewPostgres(cfg)
		if err != nil {
			lastErr = err
			time.Sleep(delay)
			continue
		}
		sqlDB, err := db.DB()
		if err == nil {
			if err = sqlDB.Ping(); err == nil {
				return db, nil
			}
		}
		lastErr = err
		time.Sleep(delay)
	}
	return nil, lastErr
}

func truncateAll(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`TRUNCATE TABLE confirmation_tokens, subscriptions, watched_repos RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func init() {
	// testcontainers logs are very chatty by default.
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
