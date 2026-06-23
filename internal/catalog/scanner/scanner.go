package scanner

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog"
)

type Repository interface {
	ActiveRepos(ctx context.Context) ([]string, error)
	GetWatchedRepo(ctx context.Context, repo string) (*catalog.WatchedRepo, error)
	SaveWatchedRepoTag(ctx context.Context, repo, tag string) error
}

type ReleaseFetcher interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (string, error)
}

// EventPublisher emits one release.detected per new release; the subscription
// service (which owns the subscriber list) fans it out to recipients.
type EventPublisher interface {
	ReleaseDetected(ctx context.Context, repo, tag string) error
}

type Scanner struct {
	repo   Repository
	github ReleaseFetcher
	events EventPublisher
	pool   *WorkerPool
	cfg    Config
}

func New(
	repo Repository,
	github ReleaseFetcher,
	events EventPublisher,
	cfg *Config,
) *Scanner {
	cfg.withDefaults()
	return &Scanner{
		repo:   repo,
		github: github,
		events: events,
		pool:   NewWorkerPool(cfg.Concurrency),
		cfg:    *cfg,
	}
}

func (s *Scanner) Run(ctx context.Context) {
	slog.InfoContext(
		ctx, "scanner started",
		"interval", s.cfg.Interval,
		"concurrency", s.cfg.Concurrency,
	)

	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	s.runOnce(ctx)

	// If a tick exceeds 0.8 × interval, drain the next pending tick so
	// we don't immediately re-fire after a long run.
	budget := s.cfg.Interval * 8 / 10

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "scanner stopped", "err", ctx.Err())
			return
		case <-ticker.C:
			start := time.Now()
			s.runOnce(ctx)
			if elapsed := time.Since(start); elapsed > budget {
				slog.WarnContext(
					ctx, "scanner: tick exceeded budget, skipping next tick",
					"elapsed", elapsed,
					"budget", budget,
				)
				select {
				case <-ticker.C:
				default:
				}
			}
		}
	}
}

func (s *Scanner) runOnce(ctx context.Context) {
	repos, err := s.repo.ActiveRepos(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "scanner: failed to list repos", "err", err)
		return
	}

	// Signal-only abort (not ctx cancel) so in-flight workers keep their
	// per-call deadlines; siblings short-circuit on the next check.
	var rateLimitHit atomic.Bool

	s.pool.Run(
		ctx, repos, func(ctx context.Context, repo string) {
			if rateLimitHit.Load() {
				return
			}
			if err := s.safeCheckRepo(ctx, repo); err != nil {
				if errors.Is(err, catalog.ErrRateLimited) {
					if rateLimitHit.CompareAndSwap(false, true) {
						slog.WarnContext(ctx, "scanner: GitHub rate limit hit, aborting cycle")
					}
					return
				}
				slog.ErrorContext(ctx, "scanner: repo check failed", "repo", repo, "err", err)
			}
		},
	)
}

// safeCheckRepo recovers from panics so one bad repo doesn't tear down
// the worker pool. The panic is logged once here and the caller sees nil
// — it's a terminal event for that repo, not a propagatable error.
func (s *Scanner) safeCheckRepo(ctx context.Context, repo string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "scanner: recovered panic", "repo", repo, "panic", r)
			err = nil
		}
	}()
	return s.checkRepo(ctx, repo)
}

func (s *Scanner) checkRepo(ctx context.Context, repo string) error {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return catalog.ErrInvalidRepoFormat
	}

	// Per-call deadline is enforced by the GitHub client.
	tag, err := s.github.GetLatestRelease(ctx, parts[0], parts[1])
	if err != nil {
		return err
	}
	if tag == "" {
		return nil
	}

	watched, err := s.repo.GetWatchedRepo(ctx, repo)
	if err != nil {
		return err
	}

	// No new release, or first sighting: record the poll (stamps last_polled_at,
	// and baselines the tag on first sighting) and stop. A first sighting is
	// silent — the current release predates every subscription.
	if watched == nil || !watched.IsNewRelease(tag) {
		return s.repo.SaveWatchedRepoTag(ctx, repo, tag)
	}

	// Advance the cursor before publishing: a failed persist re-fires next scan
	// (no missed release) rather than letting a successful publish re-fan-out.
	if err := s.repo.SaveWatchedRepoTag(ctx, repo, tag); err != nil {
		slog.ErrorContext(
			ctx, "scanner: failed to save watched repo tag",
			"repo", repo,
			"tag", tag,
			"err", err,
		)
		return nil
	}

	if err := s.events.ReleaseDetected(ctx, repo, tag); err != nil {
		slog.ErrorContext(
			ctx, "scanner: failed to publish release.detected",
			"repo", repo,
			"tag", tag,
			"err", err,
		)
	}

	return nil
}
