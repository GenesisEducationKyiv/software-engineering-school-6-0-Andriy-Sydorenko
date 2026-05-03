package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/mail"
	"regexp"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

var repoFormatRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)

type SubscriptionRepository interface {
	CreateSubscription(ctx context.Context, sub *domain.Subscription) error
	FindSubscriptionByEmailAndRepo(ctx context.Context, email, repo string) (*domain.Subscription, error)
	FindSubscriptionsByEmail(ctx context.Context, email string) ([]domain.Subscription, error)
	FindSubscriptionByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error)
	ConfirmSubscription(ctx context.Context, id uint) error
	DeleteSubscription(ctx context.Context, id uint) error
	CreateToken(ctx context.Context, token *domain.ConfirmationToken) error
	FindTokenByValue(ctx context.Context, tokenValue string) (*domain.ConfirmationToken, error)
	DeleteToken(ctx context.Context, id uint) error
}

type RepoValidator interface {
	ValidateRepo(ctx context.Context, owner, repo string) error
}

type ConfirmationSender interface {
	SendConfirmation(email, repo, token, unsubscribeToken string) error
}

type Service struct {
	repo     SubscriptionRepository
	github   RepoValidator
	notifier ConfirmationSender
}

func New(repo SubscriptionRepository, github RepoValidator, notifier ConfirmationSender) *Service {
	return &Service{repo: repo, github: github, notifier: notifier}
}

func (s *Service) Subscribe(ctx context.Context, req domain.SubscribeRequest) error {
	if !repoFormatRegex.MatchString(req.Repo) {
		return domain.ErrInvalidRepoFormat
	}

	existing, err := s.repo.FindSubscriptionByEmailAndRepo(ctx, req.Email, req.Repo)
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

	unsubToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("failed to generate unsubscribe token: %w", err)
	}

	sub := &domain.Subscription{
		Email:            req.Email,
		Repo:             req.Repo,
		UnsubscribeToken: unsubToken,
	}
	if err := s.repo.CreateSubscription(ctx, sub); err != nil {
		return fmt.Errorf("failed to create subscription: %w", err)
	}

	tokenValue, err := generateToken()
	if err != nil {
		return fmt.Errorf("failed to generate confirmation token: %w", err)
	}

	token := &domain.ConfirmationToken{
		Token:          tokenValue,
		SubscriptionID: sub.ID,
	}
	if err := s.repo.CreateToken(ctx, token); err != nil {
		return fmt.Errorf("failed to save confirmation token: %w", err)
	}

	if err := s.notifier.SendConfirmation(req.Email, req.Repo, tokenValue, unsubToken); err != nil {
		log.Printf("failed to send confirmation email for repo=%s: %v", req.Repo, err)
	}

	return nil
}

func (s *Service) ConfirmSubscription(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return domain.ErrTokenNotFound
	}

	token, err := s.repo.FindTokenByValue(ctx, tokenValue)
	if err != nil {
		return fmt.Errorf("failed to look up token: %w", err)
	}
	if token == nil {
		return domain.ErrTokenNotFound
	}

	if err := s.repo.ConfirmSubscription(ctx, token.SubscriptionID); err != nil {
		return fmt.Errorf("failed to confirm subscription id=%d: %w", token.SubscriptionID, err)
	}

	if err := s.repo.DeleteToken(ctx, token.ID); err != nil {
		log.Printf("failed to delete used confirmation token id=%d: %v", token.ID, err)
	}

	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return domain.ErrTokenNotFound
	}

	sub, err := s.repo.FindSubscriptionByUnsubscribeToken(ctx, tokenValue)
	if err != nil {
		return fmt.Errorf("failed to look up unsubscribe token: %w", err)
	}
	if sub == nil {
		return domain.ErrTokenNotFound
	}

	if err := s.repo.DeleteSubscription(ctx, sub.ID); err != nil {
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

	subs, err := s.repo.FindSubscriptionsByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscriptions: %w", err)
	}

	return domain.ToSubscriptionListResponse(subs), nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}
