package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/api"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/cache"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/config"
	database "github.com/Andriy-Sydorenko/repo-release-notifier/internal/db"
	githubclient "github.com/Andriy-Sydorenko/repo-release-notifier/internal/github"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/repository"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := database.NewPostgres(cfg.PostgresDSN())
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	repo := repository.New(db)

	var gh service.RepoValidator = githubclient.NewClient(cfg.GitHubToken)
	var fetcher scanner.ReleaseFetcher = githubclient.NewClient(cfg.GitHubToken)

	if cfg.RedisURL != "" {
		rdb, rerr := cache.NewRedis(cfg.RedisURL)
		if rerr != nil {
			log.Printf("redis unavailable, continuing without cache: %v", rerr)
		} else {
			cached := githubclient.NewCachedClient(githubclient.NewClient(cfg.GitHubToken), rdb)
			gh = cached
			fetcher = cached
			log.Printf("github client: redis cache enabled")
		}
	}

	note := notifier.New(&notifier.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUserName,
		Password: cfg.SMTPPassword,
		BaseURL:  cfg.BaseURL,
	})
	svc := service.New(repo, gh, note)
	scan := scanner.New(repo, fetcher, note, cfg.ScanInterval)

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
			log.Printf("server shutdown error: %v", err)
		}
	}()

	log.Printf("starting server on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: %w", err)
	}
	log.Printf("shutdown complete")
	return nil
}
