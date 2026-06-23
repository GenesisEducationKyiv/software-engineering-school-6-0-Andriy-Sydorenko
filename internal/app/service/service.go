package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

type SubscriptionRepo interface {
	FindSubscriptionsByEmail(ctx context.Context, email string) ([]domain.Subscription, error)
	FindSubscriptionByUnsubscribeToken(ctx context.Context, token string) (
		*domain.Subscription,
		error,
	)
	ConfirmSubscription(ctx context.Context, id uint) error
	DeleteSubscription(ctx context.Context, id uint) error
}

type TokenRepo interface {
	FindTokenByValue(ctx context.Context, tokenValue string) (*domain.ConfirmationToken, error)
	DeleteToken(ctx context.Context, id uint) error
}

// EventPublisher emits the cleanup event so Catalog releases the repo
// registration after an unsubscribe.
type EventPublisher interface {
	SubscriptionRemoved(ctx context.Context, subscriptionID, repo string) error
}

type Service struct {
	subs   SubscriptionRepo
	tokens TokenRepo
	events EventPublisher
}

func New(subs SubscriptionRepo, tokens TokenRepo, events EventPublisher) *Service {
	return &Service{subs: subs, tokens: tokens, events: events}
}

func (s *Service) ConfirmSubscription(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return domain.ErrTokenNotFound
	}

	token, err := s.tokens.FindTokenByValue(ctx, tokenValue)
	if err != nil {
		return fmt.Errorf("failed to look up token: %w", err)
	}
	if token == nil {
		return domain.ErrTokenNotFound
	}

	if err := s.subs.ConfirmSubscription(ctx, token.SubscriptionID); err != nil {
		return fmt.Errorf("failed to confirm subscription id=%d: %w", token.SubscriptionID, err)
	}

	if err := s.tokens.DeleteToken(ctx, token.ID); err != nil {
		slog.WarnContext(
			ctx, "failed to delete used confirmation token",
			"id", token.ID,
			"err", err,
		)
	}

	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return domain.ErrTokenNotFound
	}

	sub, err := s.subs.FindSubscriptionByUnsubscribeToken(ctx, tokenValue)
	if err != nil {
		return fmt.Errorf("failed to look up unsubscribe token: %w", err)
	}
	if sub == nil {
		return domain.ErrTokenNotFound
	}

	if err := s.subs.DeleteSubscription(ctx, sub.ID); err != nil {
		return fmt.Errorf("failed to delete subscription id=%d: %w", sub.ID, err)
	}

	// Best-effort cleanup: tell Catalog to release the registration. A failure
	// only delays the scanner dropping a now-subscriberless repo (benign).
	if err := s.events.SubscriptionRemoved(ctx, sub.PublicID, sub.Repo); err != nil {
		slog.WarnContext(ctx, "failed to publish subscription.removed", "id", sub.ID, "err", err)
	}

	return nil
}

func (s *Service) GetSubscriptions(
	ctx context.Context,
	email string,
) ([]domain.SubscriptionResponse, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, domain.ErrInvalidEmail
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, domain.ErrInvalidEmail
	}

	subs, err := s.subs.FindSubscriptionsByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscriptions: %w", err)
	}

	return domain.ToSubscriptionListResponse(subs), nil
}
