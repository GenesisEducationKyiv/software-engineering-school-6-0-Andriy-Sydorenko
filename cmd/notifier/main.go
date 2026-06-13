package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/logging"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := logging.NewLogger(logging.Config{
		Level:  logging.Level(strings.ToLower(platform.GetOrDefault("LOG_LEVEL", "info"))),
		Format: logging.Format(strings.ToLower(platform.GetOrDefault("LOG_FORMAT", "text"))),
	}, os.Stdout)
	slog.SetDefault(logger)

	cfg, err := notifier.LoadServiceConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	core := notifier.NewCore(&cfg.SMTP)

	grpcServer := platform.NewServer(cfg.Token)
	pb.RegisterNotifierServiceServer(grpcServer, notifier.NewGRPCServer(core))

	admin := platform.RunAdminServer(cfg.AdminAddr)
	slog.Info("notifier admin server listening", "addr", cfg.AdminAddr)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.GRPCAddr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		slog.Info("notifier shutting down")

		// Bound the graceful drain: GracefulStop alone waits indefinitely on
		// in-flight RPCs (e.g. a hung SMTP send), which would let Docker SIGKILL
		// mid-drain. Force Stop() past the deadline so shutdown is always bounded.
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(8 * time.Second):
			slog.Warn("notifier: graceful stop timed out, forcing")
			grpcServer.Stop()
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := admin.Shutdown(shutdownCtx); err != nil {
			slog.Error("admin shutdown error", "err", err)
		}
	}()

	slog.Info("notifier gRPC server listening", "addr", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	slog.Info("notifier shutdown complete")
	return nil
}
