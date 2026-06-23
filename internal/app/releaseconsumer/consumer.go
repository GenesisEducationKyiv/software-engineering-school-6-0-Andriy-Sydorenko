// Package releaseconsumer turns a Catalog release.detected event into per-recipient
// emails. The subscription service owns the subscriber list, so the fan-out (which
// the scanner used to do inline) lives here.
package releaseconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

type SubscriptionRepo interface {
	FindConfirmedSubscriptionsByRepo(ctx context.Context, repo string) ([]domain.Subscription, error)
}

type ReleaseSender interface {
	SendReleaseNotification(ctx context.Context, email, repo, tag, unsubscribeToken string) error
}

type Consumer struct {
	repo     SubscriptionRepo
	notifier ReleaseSender
}

func New(repo SubscriptionRepo, notifier ReleaseSender) *Consumer {
	return &Consumer{repo: repo, notifier: notifier}
}

// Handle implements natsbus.EventHandler for events.release.detected.
func (c *Consumer) Handle(ctx context.Context, _ string, data []byte) error {
	var evt saga.ReleaseDetectedEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return fmt.Errorf("%w: unmarshal release.detected: %w", natsbus.ErrPermanent, err)
	}

	subs, err := c.repo.FindConfirmedSubscriptionsByRepo(ctx, evt.Repo)
	if err != nil {
		return fmt.Errorf("load subscribers for %s: %w", evt.Repo, err)
	}

	// Each per-recipient publish is independently retried/deduped downstream by
	// JetStream; a single failure is logged, not fatal to the batch.
	for i := range subs {
		sub := &subs[i]
		if err := c.notifier.SendReleaseNotification(ctx, sub.Email, evt.Repo, evt.Tag, sub.UnsubscribeToken); err != nil {
			slog.ErrorContext(ctx, "release fan-out publish failed", "id", sub.ID, "repo", evt.Repo, "tag", evt.Tag, "err", err)
		}
	}
	return nil
}
