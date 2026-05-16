// Package main is a backend binary used only for Playwright E2E.
// It boots the real router → service → repository → Postgres stack but
// stubs the two external boundaries the tests cannot reach: GitHub
// (accepts everything except owner "ghost") and SMTP (logs to stdout).
// Must never run in production.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/api"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/service"
)

type stubGitHub struct{}

func (stubGitHub) ValidateRepo(_ context.Context, owner, _ string) error {
	if owner == "ghost" {
		return domain.ErrRepoNotFound
	}
	return nil
}

type stdoutMailer struct{}

func (stdoutMailer) SendConfirmation(_ context.Context, email, repo, token, unsubToken string) error {
	log.Printf("E2E mail: to=%s repo=%s confirm=%s unsub=%s", email, repo, token, unsubToken)
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	dbCfg := &database.Config{
		URL:      os.Getenv("DATABASE_URL"),
		Host:     envOr("DB_HOST", "postgres"),
		Port:     envOr("DB_PORT", "5432"),
		User:     envOr("DB_USER", "test"),
		Password: envOr("DB_PASSWORD", "test"),
		Name:     envOr("DB_NAME", "test"),
		SSLMode:  envOr("DB_SSLMODE", "disable"),
	}

	db, err := openDBWithRetry(dbCfg)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	repo := repository.New(db)
	svc := service.New(repo, stubGitHub{}, stdoutMailer{}, service.RandomToken)
	router := api.NewRouter(api.NewHandler(svc), os.Getenv("API_KEY"))

	srv := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("e2e-server listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openDBWithRetry keeps re-dialing until Postgres accepts connections;
// docker-compose `depends_on` only waits for container start, not
// readiness.
func openDBWithRetry(cfg *database.Config) (*gorm.DB, error) {
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for {
		db, err := connectAndPing(cfg)
		if err == nil {
			return db, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("db not ready within 60s: %w", lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func connectAndPing(cfg *database.Config) (*gorm.DB, error) {
	db, err := database.NewPostgres(cfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
