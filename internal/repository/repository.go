package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateSubscription(ctx context.Context, sub *domain.Subscription) error {
	return r.db.WithContext(ctx).Create(sub).Error
}

func (r *Repository) FindSubscriptionByEmailAndRepo(ctx context.Context, email, repo string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.WithContext(ctx).
		Where("email = ? AND repo = ?", email, repo).
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sub, err
}

func (r *Repository) FindSubscriptionsByEmail(ctx context.Context, email string) ([]domain.Subscription, error) {
	var subs []domain.Subscription
	err := r.db.WithContext(ctx).
		Where("email = ?", email).
		Find(&subs).Error
	return subs, err
}

func (r *Repository) FindSubscriptionByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.WithContext(ctx).
		Where("unsubscribe_token = ?", token).
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sub, err
}

func (r *Repository) ConfirmSubscription(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).
		Model(&domain.Subscription{}).
		Where("id = ?", id).
		Update("confirmed", true).Error
}

func (r *Repository) DeleteSubscription(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&domain.Subscription{}, id).Error
}

func (r *Repository) CreateToken(ctx context.Context, token *domain.ConfirmationToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *Repository) FindTokenByValue(ctx context.Context, tokenValue string) (*domain.ConfirmationToken, error) {
	var token domain.ConfirmationToken
	err := r.db.WithContext(ctx).
		Preload("Subscription").
		Where("token = ?", tokenValue).
		First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &token, err
}

func (r *Repository) DeleteToken(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&domain.ConfirmationToken{}, id).Error
}

func (r *Repository) DeleteTokensBySubscriptionID(ctx context.Context, subscriptionID uint) error {
	return r.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Delete(&domain.ConfirmationToken{}).Error
}

func (r *Repository) FindDistinctConfirmedRepos(ctx context.Context) ([]string, error) {
	var repos []string
	err := r.db.WithContext(ctx).
		Model(&domain.Subscription{}).
		Where("confirmed = ?", true).
		Distinct("repo").
		Pluck("repo", &repos).Error
	return repos, err
}

func (r *Repository) FindConfirmedSubscriptionsByRepo(ctx context.Context, repo string) ([]domain.Subscription, error) {
	var subs []domain.Subscription
	err := r.db.WithContext(ctx).
		Where("repo = ? AND confirmed = ?", repo, true).
		Find(&subs).Error
	return subs, err
}

func (r *Repository) UpdateLastSeenTag(ctx context.Context, id uint, tag string) error {
	return r.db.WithContext(ctx).
		Model(&domain.Subscription{}).
		Where("id = ?", id).
		Update("last_seen_tag", tag).Error
}
