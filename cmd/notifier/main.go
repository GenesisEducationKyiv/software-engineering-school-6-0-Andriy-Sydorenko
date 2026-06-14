package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/logging"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
)

const envFile = ".env.notifier"

type Config struct {
	SMTP *notifier.Config
	Log  *logging.Config
	Port string
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
		SMTP: smtpCfg,
		Log:  logCfg,
		Port: config.GetEnvOrDefault("PORT", "8080"),
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

	note := notifier.New(cfg.SMTP)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := grpc.NewServer()

	go func() { <-ctx.Done(); server.GracefulStop() }()
	slog.Info("notifier listening", "addr", listener.Addr().String())
	return server.Serve(listener)
}

func main() {
	if err := run(); err != nil {
		// Pre-logger: slog default isn't configured until inside run().
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
