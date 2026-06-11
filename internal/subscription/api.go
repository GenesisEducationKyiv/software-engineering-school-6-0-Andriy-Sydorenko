package subscription

import (
	"context"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// Subscriber is the scanner-facing projection of a confirmed subscription — only
// the fields the notify path needs. Owned here so the scanner depends on
// subscription's public type, not on domain models.
type Subscriber struct {
	Email            string
	UnsubscribeToken string
}

// Shared DTOs with internal/api (gin-bound); aliased rather than re-declared.
type (
	SubscribeRequest     = domain.SubscribeRequest
	SubscriptionResponse = domain.SubscriptionResponse
)

// API is the subscription module's public interface for cross-module callers
// (the scanner). The HTTP layer keeps its own narrower internal/api.Service.
type API interface {
	Subscribe(ctx context.Context, req SubscribeRequest) error
	ConfirmSubscription(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
	GetSubscriptions(ctx context.Context, email string) ([]SubscriptionResponse, error)

	ListConfirmedRepos(ctx context.Context) ([]string, error)
	ListConfirmedSubscribers(ctx context.Context, repo string) ([]Subscriber, error)
}
