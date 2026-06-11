package scanner

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/subscription"
)

// SubscriberLister is the subscription module's scan-path surface, consumed
// through its public interface (satisfied by subscription.API).
type SubscriberLister interface {
	ListConfirmedRepos(ctx context.Context) ([]string, error)
	ListConfirmedSubscribers(ctx context.Context, repo string) ([]subscription.Subscriber, error)
}

// WatchedRepoStore is the per-repo release-detection state port, satisfied by
// *Repository (watched_repo table).
type WatchedRepoStore interface {
	GetLastSeenTag(ctx context.Context, repo string) (tag string, found bool, err error)
	UpsertLastSeenTag(ctx context.Context, repo, tag string) error
}

// RepoValidator is the GitHub-boundary port backing the scanner's public
// ValidateRepo. Satisfied by the github client.
type RepoValidator interface {
	ValidateRepo(ctx context.Context, owner, repo string) error
}

type ReleaseFetcher interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (string, error)
}

type ReleaseNotifier interface {
	SendReleaseNotification(ctx context.Context, email, repo, tag, unsubscribeToken string) error
}

// Config bundles scanner knobs. The per-GitHub-call deadline lives on the
// GitHub client, not here.
type Config struct {
	Interval    time.Duration // default 5m
	Concurrency int           // default 8
}

func (c *Config) withDefaults() {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
}

type Scanner struct {
	subs      SubscriberLister
	store     WatchedRepoStore
	github    ReleaseFetcher
	notifier  ReleaseNotifier
	validator RepoValidator
	pool      *WorkerPool
	cfg       Config
}

func New(
	subs SubscriberLister,
	store WatchedRepoStore,
	github ReleaseFetcher,
	notifier ReleaseNotifier,
	cfg *Config,
) *Scanner {
	cfg.withDefaults()
	return &Scanner{
		subs:     subs,
		store:    store,
		github:   github,
		notifier: notifier,
		pool:     NewWorkerPool(cfg.Concurrency),
		cfg:      *cfg,
	}
}

// SetValidator injects the GitHub-boundary validator backing ValidateRepo.
// Separate from New so the composition root can wire the github client (also the
// ReleaseFetcher) without a circular constructor.
func (s *Scanner) SetValidator(v RepoValidator) {
	s.validator = v
}

// SetSubscribers injects the subscription module's scan-path reader. Set after
// construction to break the subscription↔scanner construction cycle.
func (s *Scanner) SetSubscribers(subs SubscriberLister) {
	s.subs = subs
}

// ValidateRepo is the scanner module's public API: reports whether owner/repo
// exists. A definitive "not found" is (false, nil); transport/rate-limit
// failures propagate as a non-nil error.
func (s *Scanner) ValidateRepo(ctx context.Context, owner, repo string) (bool, error) {
	err := s.validator.ValidateRepo(ctx, owner, repo)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, domain.ErrRepoNotFound) {
		return false, nil
	}
	return false, err
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

	// If a tick exceeds 0.8 × interval, drain the next pending tick so we don't
	// immediately re-fire after a long run.
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
	repos, err := s.subs.ListConfirmedRepos(ctx)
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
				if errors.Is(err, domain.ErrRateLimited) {
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

// RunOnceForTest runs a single scan cycle. Exported only for integration tests
// that drive a real DB-backed cycle without the ticker loop.
func (s *Scanner) RunOnceForTest(ctx context.Context) { s.runOnce(ctx) }

// safeCheckRepo recovers from panics so one bad repo doesn't tear down the
// worker pool. The panic is logged once here and the caller sees nil — it's a
// terminal event for that repo, not a propagatable error.
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
		return domain.ErrInvalidRepoFormat
	}

	// Per-call deadline is enforced by the GitHub client.
	tag, err := s.github.GetLatestRelease(ctx, parts[0], parts[1])
	if err != nil {
		return err
	}
	if tag == "" {
		return nil
	}

	lastSeen, found, err := s.store.GetLastSeenTag(ctx, repo)
	if err != nil {
		return err
	}
	if found && tag == lastSeen {
		return nil // already notified for this tag
	}

	// First sighting: record the tag and send nothing, so existing releases
	// don't spam every confirmed subscriber on the first scan (spec §9).
	if !found {
		if err := s.store.UpsertLastSeenTag(ctx, repo, tag); err != nil {
			return err
		}
		return nil
	}

	subs, err := s.subs.ListConfirmedSubscribers(ctx, repo)
	if err != nil {
		return err
	}

	// Persist before notifying: a failed upsert would re-fire next scan, so gate
	// the whole fan-out on it.
	if err := s.store.UpsertLastSeenTag(ctx, repo, tag); err != nil {
		return err
	}

	for i := range subs {
		sub := subs[i]
		if err := s.notifier.SendReleaseNotification(
			ctx,
			sub.Email,
			repo,
			tag,
			sub.UnsubscribeToken,
		); err != nil {
			slog.ErrorContext(
				ctx, "scanner: failed to send release notification",
				"repo", repo,
				"tag", tag,
				"err", err,
			)
		}
	}

	return nil
}
