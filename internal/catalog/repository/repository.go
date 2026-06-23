package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Register is idempotent: ON CONFLICT DO NOTHING on the registration PK, then
// ensure a cursor row exists (without resetting an existing cursor). A retried
// command with the same subscription_id is a no-op.
func (r *Repository) Register(ctx context.Context, subscriptionID, repo string) error {
	return r.db.WithContext(ctx).Transaction(
		func(tx *gorm.DB) error {
			reg := catalog.RepoRegistration{SubscriptionID: subscriptionID, Repo: repo}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reg).Error; err != nil {
				return err
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(&catalog.WatchedRepo{Repo: repo}).Error
		},
	)
}

// Release is idempotent: deleting a non-existent registration is a no-op.
func (r *Repository) Release(ctx context.Context, subscriptionID string) error {
	return r.db.WithContext(ctx).
		Where("subscription_id = ?", subscriptionID).
		Delete(&catalog.RepoRegistration{}).Error
}

// ActiveRepos returns the distinct repos the scanner should poll (>= 1 registration).
func (r *Repository) ActiveRepos(ctx context.Context) ([]string, error) {
	var repos []string
	err := r.db.WithContext(ctx).
		Model(&catalog.RepoRegistration{}).
		Distinct("repo").
		Pluck("repo", &repos).Error
	return repos, err
}

// GetWatchedRepo returns the repo's release cursor, or nil if never scanned.
func (r *Repository) GetWatchedRepo(ctx context.Context, repo string) (*catalog.WatchedRepo, error) {
	var w catalog.WatchedRepo
	err := r.db.WithContext(ctx).Where("repo = ?", repo).First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &w, err
}

// SaveWatchedRepoTag upserts the repo's cursor: insert on first sighting, else
// update the tag and stamp last_polled_at.
func (r *Repository) SaveWatchedRepoTag(ctx context.Context, repo, tag string) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "repo"}},
			DoUpdates: clause.Assignments(map[string]any{
				"last_seen_tag":  tag,
				"last_polled_at": gorm.Expr("now()"),
			}),
		}).
		Create(&catalog.WatchedRepo{Repo: repo, LastSeenTag: tag}).Error
}
