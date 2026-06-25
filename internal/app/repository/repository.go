package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateForSaga(
	ctx context.Context,
	sub *domain.Subscription,
	token *domain.ConfirmationToken,
) (already, mine bool, err error) {
	err = r.db.WithContext(ctx).Transaction(
		func(tx *gorm.DB) error {
			res := tx.Clauses(
				clause.OnConflict{
					Columns:   []clause.Column{{Name: "email"}, {Name: "repo"}},
					DoNothing: true,
				},
			).Create(sub)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				var existing domain.Subscription
				if qErr := tx.Where("email = ? AND repo = ?", sub.Email, sub.Repo).
					First(&existing).Error; qErr != nil {
					return qErr
				}
				already = true
				mine = existing.PublicID == sub.PublicID
				return nil
			}
			token.SubscriptionID = sub.ID
			return tx.Create(token).Error
		},
	)
	return already, mine, err
}

func (r *Repository) FindSubscriptionByEmailAndRepo(
	ctx context.Context,
	email, repo string,
) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := r.db.WithContext(ctx).
		Where("email = ? AND repo = ?", email, repo).
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sub, err
}

func (r *Repository) FindSubscriptionsByEmail(
	ctx context.Context,
	email string,
) ([]domain.Subscription, error) {
	var subs []domain.Subscription
	err := r.db.WithContext(ctx).
		Where("email = ?", email).
		Find(&subs).Error
	return subs, err
}

func (r *Repository) FindSubscriptionByUnsubscribeToken(
	ctx context.Context,
	token string,
) (*domain.Subscription, error) {
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

func (r *Repository) FindTokenByValue(
	ctx context.Context,
	tokenValue string,
) (*domain.ConfirmationToken, error) {
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

func (r *Repository) FindConfirmedSubscriptionsByRepo(
	ctx context.Context,
	repo string,
) ([]domain.Subscription, error) {
	var subs []domain.Subscription
	err := r.db.WithContext(ctx).
		Where("repo = ? AND confirmed = ?", repo, true).
		Find(&subs).Error
	return subs, err
}
