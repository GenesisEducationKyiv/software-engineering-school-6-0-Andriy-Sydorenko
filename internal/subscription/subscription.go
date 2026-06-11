package subscription

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

var repoFormatRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

type SubscriptionRepo interface {
	CreateSubscriptionWithToken(
		ctx context.Context,
		sub *domain.Subscription,
		token *domain.ConfirmationToken,
	) error
	FindSubscriptionByEmailAndRepo(ctx context.Context, email, repo string) (*domain.Subscription, error)
	FindSubscriptionsByEmail(ctx context.Context, email string) ([]domain.Subscription, error)
	FindSubscriptionByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error)
	ConfirmSubscription(ctx context.Context, id uint) error
	DeleteSubscription(ctx context.Context, id uint) error

	// Scan-path reads — back ListConfirmedRepos/ListConfirmedSubscribers.
	FindDistinctConfirmedRepos(ctx context.Context) ([]string, error)
	FindConfirmedSubscriptionsByRepo(ctx context.Context, repo string) ([]domain.Subscription, error)
}

type TokenRepo interface {
	FindTokenByValue(ctx context.Context, tokenValue string) (*domain.ConfirmationToken, error)
	DeleteToken(ctx context.Context, id uint) error
}

type SubscriptionRepository interface {
	SubscriptionRepo
	TokenRepo
}

// RepoValidator is the scanner-module port: subscription validates a repo exists
// before persisting. Injected as scanner.API at the composition root.
type RepoValidator interface {
	ValidateRepo(ctx context.Context, owner, repo string) error
}

// ConfirmationSender is the notifier port for the confirmation email.
type ConfirmationSender interface {
	SendConfirmation(ctx context.Context, email, repo, token, unsubscribeToken string) error
}

// TokenGenerator returns a fresh opaque token. Injectable so tests can produce
// deterministic values without depending on the UUID library.
type TokenGenerator func() (string, error)

func RandomToken() (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("uuid generation failed: %w", err)
	}
	return u.String(), nil
}

type Service struct {
	subs     SubscriptionRepo
	tokens   TokenRepo
	github   RepoValidator
	notifier ConfirmationSender
	newToken TokenGenerator
}

func New(
	repo SubscriptionRepository,
	github RepoValidator,
	notifier ConfirmationSender,
	newToken TokenGenerator,
) *Service {
	if newToken == nil {
		newToken = RandomToken
	}
	return &Service{
		subs:     repo,
		tokens:   repo,
		github:   github,
		notifier: notifier,
		newToken: newToken,
	}
}

func (s *Service) Subscribe(ctx context.Context, req domain.SubscribeRequest) error {
	if !repoFormatRegex.MatchString(req.Repo) {
		return domain.ErrInvalidRepoFormat
	}

	existing, err := s.subs.FindSubscriptionByEmailAndRepo(ctx, req.Email, req.Repo)
	if err != nil {
		return fmt.Errorf("failed to check existing subscription: %w", err)
	}
	if existing != nil {
		return domain.ErrAlreadySubscribed
	}

	parts := strings.SplitN(req.Repo, "/", 2)
	if err := s.github.ValidateRepo(ctx, parts[0], parts[1]); err != nil {
		return err
	}

	unsubToken, err := s.newToken()
	if err != nil {
		return fmt.Errorf("failed to generate unsubscribe token: %w", err)
	}
	confirmToken, err := s.newToken()
	if err != nil {
		return fmt.Errorf("failed to generate confirmation token: %w", err)
	}

	sub := &domain.Subscription{
		Email:            req.Email,
		Repo:             req.Repo,
		UnsubscribeToken: unsubToken,
	}
	token := &domain.ConfirmationToken{Token: confirmToken}

	if err := s.subs.CreateSubscriptionWithToken(ctx, sub, token); err != nil {
		return fmt.Errorf("failed to persist subscription: %w", err)
	}

	if err := s.notifier.SendConfirmation(ctx, req.Email, req.Repo, confirmToken, unsubToken); err != nil {
		slog.ErrorContext(
			ctx, "failed to send confirmation email",
			"repo", req.Repo,
			"err", err,
		)
	}

	return nil
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

	return nil
}

func (s *Service) GetSubscriptions(ctx context.Context, email string) ([]domain.SubscriptionResponse, error) {
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

// ListConfirmedRepos returns the distinct repos with at least one confirmed
// subscriber. Scan-path read, consumed by the scanner through API.
func (s *Service) ListConfirmedRepos(ctx context.Context) ([]string, error) {
	repos, err := s.subs.FindDistinctConfirmedRepos(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list confirmed repos: %w", err)
	}
	return repos, nil
}

// ListConfirmedSubscribers returns a repo's confirmed subscribers, projected to
// the scanner-facing Subscriber value type.
func (s *Service) ListConfirmedSubscribers(ctx context.Context, repo string) ([]Subscriber, error) {
	subs, err := s.subs.FindConfirmedSubscriptionsByRepo(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to list confirmed subscribers for %s: %w", repo, err)
	}
	out := make([]Subscriber, len(subs))
	for i := range subs {
		out[i] = Subscriber{
			Email:            subs[i].Email,
			UnsubscribeToken: subs[i].UnsubscribeToken,
		}
	}
	return out, nil
}
