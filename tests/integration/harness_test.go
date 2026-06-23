//go:build integration

// Package integration exercises the subscription service's HTTP → service →
// repository → Postgres path. The Postgres container is started once per package
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

// noopEvents is a no-op service.EventPublisher — the unsubscribe cleanup event
// is covered by the saga integration tests, not the HTTP API tests.
type noopEvents struct{}

func (noopEvents) SubscriptionRemoved(context.Context, string, string) error { return nil }

// testEnv bundles the per-test handles: a fresh router and the shared DB
// (rows truncated between tests).
type testEnv struct {
	router *gin.Engine
	db     *gorm.DB
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := mustSharedDB(t)
	truncateAll(t, db)

	repo := repository.New(db)
	svc := service.New(repo, repo, noopEvents{})
	router := api.NewRouter(api.NewHandler(svc), testAPIKey)

	return &testEnv{router: router, db: db}
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
	if err := db.Exec(`TRUNCATE TABLE confirmation_tokens, subscriptions RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func init() {
	// testcontainers logs are very chatty by default.
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
