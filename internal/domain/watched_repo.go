package domain

import "time"

// WatchedRepo is the scanner module's per-repo release-detection state.
type WatchedRepo struct {
	Repo         string    `gorm:"primaryKey;column:repo;type:text" json:"repo"`
	LastSeenTag  string    `gorm:"column:last_seen_tag;type:text;not null;default:''" json:"last_seen_tag"`
	LastPolledAt time.Time `gorm:"column:last_polled_at" json:"last_polled_at"`
}

// TableName pins the singular table name; GORM would otherwise pluralize.
func (WatchedRepo) TableName() string { return "watched_repo" }
