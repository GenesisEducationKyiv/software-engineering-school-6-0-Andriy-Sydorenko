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

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"

	orchestratordb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/db"
)

const envFile = ".env"

type Config struct {
	DB              *orchestratordb.Config
	Log             *logging.Config
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	NATSURL         string
	RequestTimeout  time.Duration
	RecoverInterval time.Duration
}

func (c *Config) validate() error {
	if c.DB.URL == "" && (c.DB.User == "" || c.DB.Name == "") {
		return fmt.Errorf("either DATABASE_URL or DB_USER+DB_NAME (with DB_HOST/DB_PORT/DB_PASSWORD) must be set")
	}
	return c.Log.Validate()
}

func loadCfg() (*Config, error) {
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load %s: %w", envFile, err)
	}

	dbCfg := orchestratordb.LoadConfig()
	logCfg := logging.LoadConfig()

	if err := config.ValidateAll(dbCfg, logCfg); err != nil {
		return nil, err
	}

	cfg := &Config{
		DB:              dbCfg,
		Log:             logCfg,
		Port:            config.GetEnvOrDefault("PORT", "8090"),
		ReadTimeout:     config.GetEnvDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    config.GetEnvDuration("WRITE_TIMEOUT", 15*time.Second),
		NATSURL:         config.GetEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		RequestTimeout:  config.GetEnvDuration("SAGA_REQUEST_TIMEOUT", 5*time.Second),
		RecoverInterval: config.GetEnvDuration("SAGA_RECOVER_INTERVAL", 30*time.Second),
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

	db, err := orchestratordb.NewPostgres(cfg.DB)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := orchestratordb.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	nc, js, err := natsbus.Connect(cfg.NATSURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer func() { _ = nc.Drain() }()
	if err := natsbus.EnsureStreams(context.Background(), js); err != nil {
		return fmt.Errorf("ensure streams: %w", err)
	}

	coord := service.NewCoordinator(
		service.NewNATSParticipants(nc, cfg.RequestTimeout),
		service.NewNATSConfirmationPublisher(js),
		repository.New(db),
		service.UUIDGen{},
	)
	subsClient := service.NewSubscriptionClient(nc, cfg.RequestTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := coord.Recover(ctx); err != nil {
		slog.ErrorContext(ctx, "initial recovery sweep failed", "err", err)
	}
	go runRecoveryLoop(ctx, coord, cfg.RecoverInterval)

	router := api.NewRouter(api.NewHTTPHandler(coord, subsClient))
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
			slog.Error("orchestrator shutdown error", "err", err)
		}
	}()

	slog.Info("starting orchestrator", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("orchestrator: %w", err)
	}
	slog.Info("shutdown complete")
	return nil
}

func runRecoveryLoop(ctx context.Context, coord *service.Coordinator, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runSweep(ctx, coord)
		}
	}
}

// runSweep runs one recovery pass, recovering from a panic so one bad saga never
// kills the recovery loop for the process lifetime.
func runSweep(ctx context.Context, coord *service.Coordinator) {
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "recovery sweep panicked", "panic", r)
		}
	}()
	if err := coord.Recover(ctx); err != nil {
		slog.ErrorContext(ctx, "recovery sweep failed", "err", err)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
