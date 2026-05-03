package database

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

func NewPostgres(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return db, nil
}

func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&domain.Subscription{}, &domain.ConfirmationToken{}); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}

	// Uniqueness scoped to live rows — see README on soft delete + uniqueness.
	const stmt = `CREATE UNIQUE INDEX IF NOT EXISTS idx_email_repo_live ` +
		`ON subscriptions (email, repo) WHERE deleted_at IS NULL`
	if err := db.Exec(stmt).Error; err != nil {
		return fmt.Errorf("create partial unique index: %w", err)
	}
	return nil
}
