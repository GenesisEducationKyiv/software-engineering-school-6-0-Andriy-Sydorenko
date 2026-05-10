package database

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// Config bundles Postgres connection knobs. URL wins when set.
type Config struct {
	URL      string
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

// DSN returns a libpq connection string.
func (c *Config) DSN() string {
	if c.URL != "" {
		return c.URL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode,
	)
}

func NewPostgres(cfg *Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
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
