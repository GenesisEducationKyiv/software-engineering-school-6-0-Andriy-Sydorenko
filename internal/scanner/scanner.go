package scanner

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

type Repository interface {
	FindDistinctConfirmedRepos(ctx context.Context) ([]string, error)
	FindConfirmedSubscriptionsByRepo(ctx context.Context, repo string) ([]domain.Subscription, error)
	UpdateLastSeenTag(ctx context.Context, id uint, tag string) error
}

type ReleaseFetcher interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (string, error)
}

type ReleaseNotifier interface {
	SendReleaseNotification(email, repo, tag, unsubscribeToken string) error
}

type Scanner struct {
	repo     Repository
	github   ReleaseFetcher
	notifier ReleaseNotifier
	interval time.Duration
}

func New(
	repo Repository,
	github ReleaseFetcher,
	notifier ReleaseNotifier,
	interval time.Duration,
) *Scanner {
	return &Scanner{repo: repo, github: github, notifier: notifier, interval: interval}
}

func (s *Scanner) Run(ctx context.Context) {
	log.Printf("scanner started with interval=%s", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Printf("scanner stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *Scanner) runOnce(ctx context.Context) {
	repos, err := s.repo.FindDistinctConfirmedRepos(ctx)
	if err != nil {
		log.Printf("scanner: failed to list repos: %v", err)
		return
	}

	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		if err := s.safeCheckRepo(ctx, repo); err != nil {
			if errors.Is(err, domain.ErrRateLimited) {
				log.Printf("scanner: GitHub rate limit hit, aborting cycle")
				return
			}
			log.Printf("scanner: repo=%s error: %v", repo, err)
		}
	}
}

// safeCheckRepo recovers from panics so one bad repo doesn't kill the goroutine.
func (s *Scanner) safeCheckRepo(ctx context.Context, repo string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("scanner: recovered panic on repo=%s: %v", repo, r)
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

	tag, err := s.github.GetLatestRelease(ctx, parts[0], parts[1])
	if err != nil {
		return err
	}
	if tag == "" {
		return nil
	}

	subs, err := s.repo.FindConfirmedSubscriptionsByRepo(ctx, repo)
	if err != nil {
		return err
	}

	for i := range subs {
		sub := &subs[i]
		if sub.LastSeenTag == tag {
			continue
		}
		if err := s.repo.UpdateLastSeenTag(ctx, sub.ID, tag); err != nil {
			log.Printf("scanner: failed to update last_seen_tag for id=%d: %v", sub.ID, err)
			continue
		}
		if sub.LastSeenTag == "" {
			continue
		}
		if err := s.notifier.SendReleaseNotification(sub.Email, sub.Repo, tag, sub.UnsubscribeToken); err != nil {
			log.Printf("scanner: failed to notify %s about %s@%s: %v", sub.Email, repo, tag, err)
		}
	}

	return nil
}
