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
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/natspublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"

	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/github"
)

const envFile = ".env"

type Config struct {
	DB           *database.Config
	Redis        *cache.Config
	GitHub       *githubclient.Config
	Scanner      *scanner.Config
	Log          *logging.Config
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	APIKey       string
	BaseURL      string // BASE_URL for confirmation/unsubscribe links in emails
	NATSURL      string // NATS_URL, e.g. "nats://localhost:4222"
}

func (c *Config) validate() error {
	if c.DB.URL == "" && (c.DB.User == "" || c.DB.Name == "") {
		return fmt.Errorf("either DATABASE_URL or DB_USER+DB_NAME (with DB_HOST/DB_PORT/DB_PASSWORD) must be set")
	}
	if err := c.Log.Validate(); err != nil {
		return err
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
		DB:           dbCfg,
		Redis:        redisCfg,
		Scanner:      scannerCfg,
		GitHub:       githubCfg,
		Log:          logCfg,
		Port:         config.GetEnvOrDefault("PORT", "8080"),
		ReadTimeout:  config.GetEnvDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout: config.GetEnvDuration("WRITE_TIMEOUT", 10*time.Second),
		APIKey:       config.GetEnvOrDefault("API_KEY", ""),
		BaseURL:      config.GetEnvOrDefault("BASE_URL", "http://localhost:8080"),
		NATSURL:      config.GetEnvOrDefault("NATS_URL", "nats://localhost:4222"),
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

	nc, js, err := natsbus.Connect(cfg.NATSURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer func() { _ = nc.Drain() }()
	if err := natsbus.EnsureStreams(context.Background(), js); err != nil {
		return fmt.Errorf("ensure streams: %w", err)
	}
	if err := natsbus.SetDedupWindow(
		context.Background(), js, notify.DedupWindow(cfg.Scanner.Interval),
	); err != nil {
		return fmt.Errorf("set dedup window: %w", err)
	}

	note := service.NewEmailNotifier(cfg.BaseURL, natspublisher.New(js))
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
