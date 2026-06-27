//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/gorm"

	catalogdb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/db"
	catalogrepo "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/repository"
)

func newCatalogRepo(t *testing.T) *catalogrepo.Repository {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	c, err := tcpg.Run(
		ctx, "postgres:16-alpine",
		tcpg.WithDatabase("catalog"),
		tcpg.WithUsername("test"),
		tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
		tcpg.WithSQLDriver("pgx"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := openCatalogWithRetry(dsn, 30, 500*time.Millisecond)
	require.NoError(t, err)
	require.NoError(t, catalogdb.Migrate(db))

	return catalogrepo.New(db)
}

func openCatalogWithRetry(dsn string, attempts int, delay time.Duration) (*gorm.DB, error) {
	cfg := &catalogdb.Config{URL: dsn}
	var lastErr error
	for i := 0; i < attempts; i++ {
		db, err := catalogdb.NewPostgres(cfg)
		if err != nil {
			lastErr = err
		} else if sqlDB, e := db.DB(); e != nil {
			lastErr = fmt.Errorf("db handle: %w", e)
		} else if e := sqlDB.Ping(); e != nil {
			lastErr = fmt.Errorf("ping: %w", e)
		} else {
			return db, nil
		}
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("connect catalog db: %w", lastErr)
}

func TestCatalogRegister_IsIdempotent(t *testing.T) {
	repo := newCatalogRepo(t)
	ctx := context.Background()
	id := "11111111-1111-1111-1111-111111111111"

	require.NoError(t, repo.Register(ctx, id, "owner/name"))
	require.NoError(t, repo.Register(ctx, id, "owner/name")) // retry — no double count

	active, err := repo.ActiveRepos(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"owner/name"}, active)

	require.NoError(t, repo.Release(ctx, id))
	require.NoError(t, repo.Release(ctx, id)) // retry — no error

	active, err = repo.ActiveRepos(ctx)
	require.NoError(t, err)
	require.Empty(t, active)
}
