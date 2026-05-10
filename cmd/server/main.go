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

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/config"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := database.NewPostgres(&cfg.DB)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	repo := repository.New(db)

	gh, fetcher := buildGitHubClients(cfg)

	note := notifier.New(&cfg.SMTP)
	svc := service.New(repo, gh, note, service.RandomToken)
	scan := scanner.New(repo, fetcher, note, &cfg.Scanner)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.ScannerEnabled {
		go scan.Run(ctx)
	} else {
		log.Printf("scanner disabled (SCANNER_ENABLED=false)")
	}

	router := api.NewRouter(api.NewHandler(svc), cfg.APIKey)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
	}()

	log.Printf("starting server on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: %w", err)
	}
	log.Printf("shutdown complete")
	return nil
}

// buildGitHubClients returns validator and fetcher views of the
// GitHub client, optionally wrapped in the Redis cache decorator.
// Cache failure is non-fatal.
func buildGitHubClients(cfg *config.Config) (service.RepoValidator, scanner.ReleaseFetcher) {
	if cfg.Redis.DSN() == "" {
		c := githubclient.NewClient(&cfg.GitHub)
		return c, c
	}
	rdb, err := cache.NewRedis(&cfg.Redis)
	if err != nil {
		log.Printf("redis unavailable, continuing without cache: %v", err)
		c := githubclient.NewClient(&cfg.GitHub)
		return c, c
	}
	cached := githubclient.NewCachedClient(githubclient.NewClient(&cfg.GitHub), rdb)
	log.Printf("github client: redis cache enabled")
	return cached, cached
}
