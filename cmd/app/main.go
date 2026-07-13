package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/notifierclient"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"

	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/github"
)

const envFile = ".env"

type Config struct {
	DB                *database.Config
	Redis             *cache.Config
	GitHub            *githubclient.Config
	Scanner           *scanner.Config
	Log               *logging.Config
	Port              string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	APIKey            string
	NotifierAddr      string // NOTIFIER_GRPC_ADDR, e.g. "notifier:9090"
	NotifierToken     string // INTERNAL_API_TOKEN; empty disables gRPC auth
	NotifierTransport string // NOTIFIER_TRANSPORT: "grpc" (default) or "rest"
	NotifierRESTURL   string // NOTIFIER_REST_URL, e.g. "http://notifier:9091"
	BaseURL           string // BASE_URL for confirmation/unsubscribe links in emails
}

func (c *Config) validate() error {
	if c.DB.URL == "" && (c.DB.User == "" || c.DB.Name == "") {
		return fmt.Errorf("either DATABASE_URL or DB_USER+DB_NAME (with DB_HOST/DB_PORT/DB_PASSWORD) must be set")
	}
	if err := c.Log.Validate(); err != nil {
		return err
	}
	if c.NotifierTransport != "grpc" && c.NotifierTransport != "rest" {
		return fmt.Errorf("invalid NOTIFIER_TRANSPORT %q (want grpc or rest)", c.NotifierTransport)
	}
	if c.NotifierTransport == "rest" && c.NotifierRESTURL == "" {
		return fmt.Errorf("NOTIFIER_REST_URL is required when NOTIFIER_TRANSPORT=rest")
	}
	return nil
}

func loadCfg() (*Config, error) {
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load %s: %w", envFile, err)
	}

	dbCfg := database.LoadConfig()
	redisCfg := cache.LoadConfig()
	scannerCfg := scanner.LoadConfig()
	githubCfg := githubclient.LoadConfig()
	logCfg := logging.LoadConfig()

	if err := config.ValidateAll(dbCfg, redisCfg, scannerCfg, githubCfg, logCfg); err != nil {
		return nil, err
	}

	cfg := &Config{
		DB:                dbCfg,
		Redis:             redisCfg,
		Scanner:           scannerCfg,
		GitHub:            githubCfg,
		Log:               logCfg,
		Port:              config.GetEnvOrDefault("PORT", "8080"),
		ReadTimeout:       config.GetEnvDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      config.GetEnvDuration("WRITE_TIMEOUT", 10*time.Second),
		APIKey:            config.GetEnvOrDefault("API_KEY", ""),
		NotifierAddr:      config.GetEnvOrDefault("NOTIFIER_GRPC_ADDR", "localhost:9090"),
		NotifierToken:     config.GetEnvOrDefault("INTERNAL_API_TOKEN", ""),
		NotifierTransport: config.GetEnvOrDefault("NOTIFIER_TRANSPORT", "grpc"),
		NotifierRESTURL:   config.GetEnvOrDefault("NOTIFIER_REST_URL", ""),
		BaseURL:           config.GetEnvOrDefault("BASE_URL", "http://localhost:8080"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func run() error {
	cfg, err := loadCfg()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.NewLogger(cfg.Log, os.Stdout)
	slog.SetDefault(logger)

	db, err := database.NewPostgres(cfg.DB)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	repo := repository.New(db)
	gh := buildGitHubClient(cfg)

	sender, closeSender, err := newEmailSender(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeSender() }()

	note := service.NewEmailNotifier(cfg.BaseURL, sender)
	svc := service.New(repo, repo, gh, note, service.RandomToken)
	scan := scanner.New(repo, gh, note, cfg.Scanner)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go scan.Run(ctx)

	router := api.NewRouter(api.NewHandler(svc), cfg.APIKey)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("app shutdown error", "err", err)
		}
	}()

	slog.Info("starting app", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("app: %w", err)
	}
	slog.Info("shutdown complete")
	return nil
}

func main() {
	if err := run(); err != nil {
		// Pre-logger: slog default isn't configured until inside run().
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func newEmailSender(cfg *Config) (service.EmailSender, func() error, error) {
	if cfg.NotifierTransport == "rest" {
		return notifierclient.NewHTTPSender(
			cfg.NotifierRESTURL,
			cfg.NotifierToken,
		), func() error { return nil }, nil
	}
	conn, err := notifierclient.Dial(cfg.NotifierAddr, cfg.NotifierToken)
	if err != nil {
		return nil, nil, fmt.Errorf("dial notifier: %w", err)
	}
	return conn, conn.Close, nil
}

// githubClient is the GitHub capability surface main wires into both the
// service (repo validation) and the scanner (release fetching).
type githubClient interface {
	service.RepoValidator
	scanner.ReleaseFetcher
}

// buildGitHubClient returns the GitHub client, wrapped in the Redis cache
// decorator when Redis is configured and reachable. Cache failure is non-fatal:
// the app falls back to the uncached client.
func buildGitHubClient(cfg *Config) githubClient {
	base := githubclient.NewClient(cfg.GitHub)
	if cfg.Redis.DSN() == "" {
		return base
	}
	rdb, err := cache.NewRedis(cfg.Redis)
	if err != nil {
		slog.Warn("redis unavailable, continuing without cache", "err", err)
		return base
	}
	slog.Info("github client: redis cache enabled")
	return githubclient.NewCachedClient(base, rdb)
}
