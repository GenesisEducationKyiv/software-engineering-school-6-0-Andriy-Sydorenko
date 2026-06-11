package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/config"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/logging"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/subscription"
)

func main() {
	if err := run(); err != nil {
		// Pre-logger: slog default isn't configured until inside run().
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.NewLogger(cfg.Log, os.Stdout)
	slog.SetDefault(logger)

	db, err := database.NewPostgres(&cfg.DB)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	gh, fetcher := buildGitHubClients(cfg)
	note := notifier.New(&cfg.SMTP)

	// scanner owns watched_repo + the GitHub boundary (ValidateRepo + fetch).
	scan := scanner.New(nil, scanner.NewRepository(db), fetcher, note, &cfg.Scanner)
	scan.SetValidator(gh)

	// subscription owns subscriptions + tokens; reaches the scanner only through
	// the validator adapter (the public ValidateRepo, port-shaped).
	subRepo := subscription.NewRepository(db)
	svc := subscription.New(subRepo, scannerValidator{scan}, note, subscription.RandomToken)

	// Close the bidirectional loop: the scanner's scan-path reads come from the
	// subscription module's public API.
	scan.SetSubscribers(svc)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go scan.Run(ctx)

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
			slog.Error("server shutdown error", "err", err)
		}
	}()

	slog.Info("starting server", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: %w", err)
	}
	slog.Info("shutdown complete")
	return nil
}

// scannerValidator adapts the scanner's public ValidateRepo (bool, error) to the
// subscription module's RepoValidator port (error). A "does not exist" becomes
// ErrRepoNotFound; transport errors pass through.
type scannerValidator struct {
	scan *scanner.Scanner
}

func (a scannerValidator) ValidateRepo(ctx context.Context, owner, repo string) error {
	ok, err := a.scan.ValidateRepo(ctx, owner, repo)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrRepoNotFound
	}
	return nil
}

// buildGitHubClients returns validator and fetcher views of the GitHub client,
// optionally wrapped in the Redis cache decorator. Cache failure is non-fatal.
func buildGitHubClients(cfg *config.Config) (scanner.RepoValidator, scanner.ReleaseFetcher) {
	if cfg.Redis.DSN() == "" {
		c := githubclient.NewClient(&cfg.GitHub)
		return c, c
	}
	rdb, err := cache.NewRedis(&cfg.Redis)
	if err != nil {
		slog.Warn("redis unavailable, continuing without cache", "err", err)
		c := githubclient.NewClient(&cfg.GitHub)
		return c, c
	}
	cached := githubclient.NewCachedClient(githubclient.NewClient(&cfg.GitHub), rdb)
	slog.Info("github client: redis cache enabled")
	return cached, cached
}
