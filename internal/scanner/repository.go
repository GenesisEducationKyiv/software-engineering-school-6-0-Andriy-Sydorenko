package scanner

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// Repository is the scanner module's data access for watched_repo.
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// GetLastSeenTag returns the stored tag for repo, and whether a row exists.
// A missing row (found=false) is the baseline case: record-and-skip.
func (r *Repository) GetLastSeenTag(ctx context.Context, repo string) (tag string, found bool, err error) {
	var w domain.WatchedRepo
	err = r.db.WithContext(ctx).
		Where("repo = ?", repo).
		First(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return w.LastSeenTag, true, nil
}

// UpsertLastSeenTag records the latest tag + poll time for repo, inserting the
// row on first sight and updating it thereafter (PK = repo).
func (r *Repository) UpsertLastSeenTag(ctx context.Context, repo, tag string) error {
	row := domain.WatchedRepo{
		Repo:         repo,
		LastSeenTag:  tag,
		LastPolledAt: time.Now(),
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "repo"}},
			DoUpdates: clause.AssignmentColumns([]string{"last_seen_tag", "last_polled_at"}),
		}).
		Create(&row).Error
}

var _ WatchedRepoStore = (*Repository)(nil)
