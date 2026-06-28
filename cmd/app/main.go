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

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/confirmationconsumer"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/eventpublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/natspublisher"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/releaseconsumer"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"

	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/db"
	appsaga "github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/saga"
)

const envFile = ".env"

type Config struct {
	DB           *database.Config
	Log          *logging.Config
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	APIKey       string
	BaseURL      string
	NATSURL      string
	MaxDeliver   int
	AckWait      time.Duration
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

	dbCfg := database.LoadConfig()
	logCfg := logging.LoadConfig()

	if err := config.ValidateAll(dbCfg, logCfg); err != nil {
		return nil, err
	}

	port := config.GetEnvOrDefault("PORT", "8080")
	cfg := &Config{
		DB:           dbCfg,
		Log:          logCfg,
		Port:         port,
		ReadTimeout:  config.GetEnvDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout: config.GetEnvDuration("WRITE_TIMEOUT", 10*time.Second),
		APIKey:       config.GetEnvOrDefault("API_KEY", ""),
		BaseURL:      config.GetEnvOrDefault("BASE_URL", "http://localhost:8090"),
		NATSURL:      config.GetEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		MaxDeliver:   config.GetEnvInt("CONSUMER_MAX_DELIVER", 5),
		AckWait:      config.GetEnvDuration("CONSUMER_ACK_WAIT", 30*time.Second),
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

	nc, js, err := natsbus.Connect(cfg.NATSURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer func() { _ = nc.Drain() }()
	if err := natsbus.EnsureStreams(context.Background(), js); err != nil {
		return fmt.Errorf("ensure streams: %w", err)
	}

	emailNotifier := service.NewEmailNotifier(cfg.BaseURL, natspublisher.New(js))
	svc := service.New(repo, repo, eventpublisher.New(js))
	sagaHandler := appsaga.NewHandler(repo)
	cmdHandler := appsaga.NewCommandHandler(svc)
	relConsumer := releaseconsumer.New(repo, emailNotifier)
	confConsumer := confirmationconsumer.New(emailNotifier)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stopConsumers, err := startConsumers(
		ctx,
		nc,
		js,
		sagaHandler,
		cmdHandler,
		relConsumer,
		confConsumer,
		cfg,
	)
	if err != nil {
		return err
	}
	defer stopConsumers()

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

// startConsumers wires the subscription.create saga handler (request-reply) and
// the release.detected + confirmation.requested consumers, returning one cleanup func.
func startConsumers(
	ctx context.Context,
	nc *nats.Conn,
	js jetstream.JetStream,
	h *appsaga.Handler,
	cmd *appsaga.CommandHandler,
	rc *releaseconsumer.Consumer,
	cc *confirmationconsumer.Consumer,
	cfg *Config,
) (func(), error) {
	var replies []*nats.Subscription
	respond := func(subject string, fn natsbus.HandlerFunc) error {
		sub, err := natsbus.RespondJSON(nc, subject, saga.QueueSubscription, fn)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
		replies = append(replies, sub)
		return nil
	}
	unsubReplies := func() {
		for _, s := range replies {
			_ = s.Unsubscribe()
		}
	}

	if err := respond(saga.SubjSubscriptionCreate, h.Create); err != nil {
		return nil, err
	}
	if err := respond(saga.SubjSubscriptionConfirm, cmd.Confirm); err != nil {
		unsubReplies()
		return nil, err
	}
	if err := respond(saga.SubjSubscriptionUnsubscribe, cmd.Unsubscribe); err != nil {
		unsubReplies()
		return nil, err
	}

	release, err := natsbus.Consume(
		ctx, js, natsbus.ConsumerConfig{
			Stream:        saga.EventsStreamName,
			Durable:       saga.DurableReleaseConsumer,
			FilterSubject: saga.SubjReleaseDetected,
			MaxDeliver:    cfg.MaxDeliver,
			AckWait:       cfg.AckWait,
		}, rc.Handle,
	)
	if err != nil {
		unsubReplies()
		return nil, fmt.Errorf("consume %s: %w", saga.SubjReleaseDetected, err)
	}
	confirmation, err := natsbus.Consume(
		ctx, js, natsbus.ConsumerConfig{
			Stream:        saga.EventsStreamName,
			Durable:       saga.DurableConfirmationConsumer,
			FilterSubject: saga.SubjConfirmationRequested,
			MaxDeliver:    cfg.MaxDeliver,
			AckWait:       cfg.AckWait,
		}, cc.Handle,
	)
	if err != nil {
		unsubReplies()
		release.Stop()
		return nil, fmt.Errorf("consume %s: %w", saga.SubjConfirmationRequested, err)
	}
	return func() {
		unsubReplies()
		release.Stop()
		confirmation.Stop()
	}, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
