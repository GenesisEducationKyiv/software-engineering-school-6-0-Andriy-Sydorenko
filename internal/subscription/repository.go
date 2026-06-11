package subscription

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// Repository is the subscription module's data access for subscriptions and
// confirmation_tokens. It satisfies SubscriptionRepository.
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateSubscriptionWithToken writes both rows in one transaction so they can't
// split-fail.
func (r *Repository) CreateSubscriptionWithToken(
	ctx context.Context,
	sub *domain.Subscription,
	token *domain.ConfirmationToken,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(sub).Error; err != nil {
			return err
		}
		token.SubscriptionID = sub.ID
		return tx.Create(token).Error
	})
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

var _ SubscriptionRepository = (*Repository)(nil)
