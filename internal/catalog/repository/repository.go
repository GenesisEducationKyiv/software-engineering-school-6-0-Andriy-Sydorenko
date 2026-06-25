package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/domain"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Register(ctx context.Context, subscriptionID, repo string) error {
	return r.db.WithContext(ctx).Transaction(
		func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(&domain.WatchedRepo{Repo: repo}).Error; err != nil {
				return err
			}
			reg := domain.RepoRegistration{SubscriptionID: subscriptionID, Repo: repo}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reg).Error
		},
	)
}

func (r *Repository) Release(ctx context.Context, subscriptionID string) error {
	return r.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Delete(&domain.RepoRegistration{}).Error
}

func (r *Repository) ActiveRepos(ctx context.Context) ([]string, error) {
	var repos []string
	err := r.db.WithContext(ctx).
		Model(&domain.RepoRegistration{}).
		Distinct("repo").
		Pluck("repo", &repos).Error
	return repos, err
}

func (r *Repository) GetWatchedRepo(ctx context.Context, repo string) (*domain.WatchedRepo, error) {
	var w domain.WatchedRepo
	err := r.db.WithContext(ctx).Where("repo = ?", repo).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &w, err
}

func (r *Repository) SaveWatchedRepoTag(ctx context.Context, repo, tag string) error {
	return r.db.WithContext(ctx).
		Clauses(
			clause.OnConflict{
				Columns: []clause.Column{{Name: "repo"}},
				DoUpdates: clause.Assignments(
					map[string]any{
						"last_seen_tag": tag,
					},
				),
			},
		).
		Create(&domain.WatchedRepo{Repo: repo, LastSeenTag: tag}).Error
}
