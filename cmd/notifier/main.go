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
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
)

const (
	envFile      = ".env.notifier"
	drainTimeout = 10 * time.Second
)

type Config struct {
	SMTP       *notifier.Config
	Log        *logging.Config
	AdminAddr  string
	NATSURL    string        // NATS_URL
	MaxDeliver int           // redelivery cap before DLQ
	AckWait    time.Duration // per-message ack deadline
}

func (c *Config) validate() error {
	if c.NATSURL == "" {
		return fmt.Errorf("NATS_URL must be set")
	}
	if c.MaxDeliver < 1 {
		return fmt.Errorf("NATS_MAX_DELIVER must be >= 1, got %d", c.MaxDeliver)
	}
	if c.AckWait <= 0 {
		return fmt.Errorf("NATS_ACK_WAIT must be > 0, got %s", c.AckWait)
	}
	return nil
}

func loadCfg() (*Config, error) {
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to load %s: %w", envFile, err)
	}

	smtpCfg := notifier.LoadConfig()
	logCfg := logging.LoadConfig()
	if err := config.ValidateAll(smtpCfg, logCfg); err != nil {
		return nil, err
	}

	cfg := &Config{
		SMTP:       smtpCfg,
		Log:        logCfg,
		AdminAddr:  config.GetEnvOrDefault("ADMIN_ADDR", ":9091"),
		NATSURL:    config.GetEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		MaxDeliver: config.GetEnvInt("NATS_MAX_DELIVER", 5),
		AckWait:    config.GetEnvDuration("NATS_ACK_WAIT", 30*time.Second),
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

	slog.SetDefault(logging.NewLogger(cfg.Log, os.Stdout))

	mailer := notifier.NewSMTPMailer(cfg.SMTP)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	adminMux := http.NewServeMux()
	adminMux.Handle("/metrics", promhttp.Handler())
	adminSrv := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server", "err", err)
		}
	}()

	nc, js, err := natsbus.Connect(cfg.NATSURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer func() { _ = nc.Drain() }()
	if err := natsbus.EnsureStreams(context.Background(), js); err != nil {
		return fmt.Errorf("ensure streams: %w", err)
	}

	cc, err := notifier.Subscribe(ctx, js, cfg.MaxDeliver, cfg.AckWait, notifier.NewHandler(mailer))
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer cc.Stop()

	slog.Info("notifier consuming", "admin", cfg.AdminAddr)
	<-ctx.Done()

	// Stop pulling new messages and wait for the in-flight handler to finish
	// (and ack) so a send isn't cut mid-flight and redelivered on next start.
	slog.Info("notifier draining", "timeout", drainTimeout)
	cc.Drain()
	select {
	case <-cc.Closed():
	case <-time.After(drainTimeout):
		slog.Warn("notifier: drain timed out, exiting with in-flight work")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = adminSrv.Shutdown(shutdownCtx)
	return nil
}

func main() {
	if err := run(); err != nil {
		// Pre-logger: slog default isn't configured until inside run().
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
