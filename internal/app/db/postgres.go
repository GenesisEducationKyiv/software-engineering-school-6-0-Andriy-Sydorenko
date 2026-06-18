package database

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/config"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

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

func (c *Config) Validate() error {
	if c.URL != "" {
		return nil
	}
	if c.Host == "" || c.Port == "" || c.User == "" || c.Name == "" {
		return fmt.Errorf("db config: either DATABASE_URL or DB_HOST+DB_PORT+DB_USER+DB_NAME must be set")
	}
	return nil
}

func LoadConfig() *Config {
	return &Config{
		URL:      config.GetEnvOrDefault("DATABASE_URL", ""),
		Host:     config.GetEnvOrDefault("DB_HOST", "localhost"),
		Port:     config.GetEnvOrDefault("DB_PORT", "5432"),
		User:     config.GetEnvOrDefault("DB_USER", ""),
		Password: config.GetEnvOrDefault("DB_PASSWORD", ""),
		Name:     config.GetEnvOrDefault("DB_NAME", ""),
		SSLMode:  config.GetEnvOrDefault("DB_SSLMODE", "disable"),
	}
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

// Migrate applies pending SQL migrations. Tracking lives in the
// schema_migrations table managed by golang-migrate.
func Migrate(gormDB *gorm.DB) error {
	sqlDB, _ := gormDB.DB()
	driver, err := migratepg.WithInstance(sqlDB, &migratepg.Config{})
	if err != nil {
		return err
	}
	src, _ := iofs.New(migrationsFS, "migrations")
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
