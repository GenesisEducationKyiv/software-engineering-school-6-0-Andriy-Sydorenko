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
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/eventpublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"

	catalogdb "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/github"
	catalogsaga "github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/saga"
)

const envFile = ".env"

type Config struct {
	DB         *catalogdb.Config
	Redis      *cache.Config
	GitHub     *githubclient.Config
	Scanner    *scanner.Config
	Log        *logging.Config
	AdminPort  string
	NATSURL    string
	MaxDeliver int
	AckWait    time.Duration
}

func loadCfg() (*Config, error) {
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load %s: %w", envFile, err)
	}

	dbCfg := catalogdb.LoadConfig()
	redisCfg := cache.LoadConfig()
	scannerCfg := scanner.LoadConfig()
	githubCfg := githubclient.LoadConfig()
	logCfg := logging.LoadConfig()

	if err := config.ValidateAll(dbCfg, redisCfg, scannerCfg, githubCfg, logCfg); err != nil {
		return nil, err
	}

	return &Config{
		DB:         dbCfg,
		Redis:      redisCfg,
		GitHub:     githubCfg,
		Scanner:    scannerCfg,
		Log:        logCfg,
		AdminPort:  config.GetEnvOrDefault("ADMIN_PORT", "9092"),
		NATSURL:    config.GetEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		MaxDeliver: config.GetEnvInt("CONSUMER_MAX_DELIVER", 5),
		AckWait:    config.GetEnvDuration("CONSUMER_ACK_WAIT", 30*time.Second),
	}, nil
}

func run() error {
	cfg, err := loadCfg()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.NewLogger(cfg.Log, os.Stdout)
	slog.SetDefault(logger)

	db, err := catalogdb.NewPostgres(cfg.DB)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := catalogdb.Migrate(db); err != nil {
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

	handler := catalogsaga.NewHandler(repo, gh)
	scan := scanner.New(repo, gh, eventpublisher.New(js), cfg.Scanner)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stopConsumers, err := startConsumers(ctx, nc, js, handler, cfg)
	if err != nil {
		return err
	}
	defer stopConsumers()

	go scan.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	server := &http.Server{Addr: ":" + cfg.AdminPort, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("catalog shutdown error", "err", err)
		}
	}()

	slog.Info("starting catalog", "admin_addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("catalog admin: %w", err)
	}
	slog.Info("shutdown complete")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// startConsumers wires the saga command handlers (request-reply) and the
// subscription.removed event consumer, returning a single cleanup func.
func startConsumers(
	ctx context.Context,
	nc *nats.Conn,
	js jetstream.JetStream,
	h *catalogsaga.Handler,
	cfg *Config,
) (func(), error) {
	regSub, err := natsbus.RespondJSON(nc, saga.SubjCatalogRegister, saga.QueueCatalog, h.Register)
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", saga.SubjCatalogRegister, err)
	}
	relSub, err := natsbus.RespondJSON(nc, saga.SubjCatalogRelease, saga.QueueCatalog, h.Release)
	if err != nil {
		_ = regSub.Unsubscribe()
		return nil, fmt.Errorf("subscribe %s: %w", saga.SubjCatalogRelease, err)
	}
	removed, err := natsbus.Consume(
		ctx, js, natsbus.ConsumerConfig{
			Stream:        saga.EventsStreamName,
			Durable:       saga.DurableRemovedConsumer,
			FilterSubject: saga.SubjSubscriptionRemoved,
			MaxDeliver:    cfg.MaxDeliver,
			AckWait:       cfg.AckWait,
		}, h.OnSubscriptionRemoved,
	)
	if err != nil {
		_ = regSub.Unsubscribe()
		_ = relSub.Unsubscribe()
		return nil, fmt.Errorf("consume %s: %w", saga.SubjSubscriptionRemoved, err)
	}
	return func() {
		_ = regSub.Unsubscribe()
		_ = relSub.Unsubscribe()
		removed.Stop()
	}, nil
}

// githubClient is the GitHub capability surface wired into both the register
// handler (repo validation) and the scanner (release fetching).
type githubClient interface {
	catalogsaga.RepoValidator
	scanner.ReleaseFetcher
}

// buildGitHubClient returns the GitHub client, wrapped in the Redis cache
// decorator when Redis is configured and reachable. Cache failure is non-fatal.
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
