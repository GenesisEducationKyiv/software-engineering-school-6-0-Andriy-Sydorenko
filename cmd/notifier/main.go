package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notifierpb"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/grpcmw"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
)

const envFile = ".env.notifier"

type Config struct {
	SMTP      *notifier.Config
	Log       *logging.Config
	Port      string
	AdminAddr string
}

func (c *Config) validate() error {
	if c.Port == "" {
		return fmt.Errorf("port must be set")
	}
	return nil
}

func loadCfg() (*Config, error) {
	if err := godotenv.Load(envFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load %s: %w", envFile, err)
	}

	smtpCfg := notifier.LoadConfig()
	logCfg := logging.LoadConfig()
	if err := config.ValidateAll(smtpCfg, logCfg); err != nil {
		return nil, err
	}

	cfg := &Config{
		SMTP:      smtpCfg,
		Log:       logCfg,
		Port:      config.GetEnvOrDefault("PORT", "9090"),
		AdminAddr: config.GetEnvOrDefault("ADMIN_ADDR", ":9091"),
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

	listener, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			grpcmw.RequestIDServerInterceptor(),
			notifier.MetricsInterceptor(),
		),
	)
	notifierpb.RegisterNotifierServiceServer(server, notifier.NewGRPCServer(mailer))

	go func() {
		<-ctx.Done()
		server.GracefulStop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(shutdownCtx)
	}()

	slog.Info("notifier listening", "grpc", listener.Addr().String(), "admin", cfg.AdminAddr)
	return server.Serve(listener)
}

func main() {
	if err := run(); err != nil {
		// Pre-logger: slog default isn't configured until inside run().
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
