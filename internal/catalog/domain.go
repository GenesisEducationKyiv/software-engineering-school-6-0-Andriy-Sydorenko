package catalog

import "time"

// WatchedRepo is the per-repo release cursor the scanner advances.
type WatchedRepo struct {
	Repo         string    `gorm:"primaryKey;type:varchar(255)"`
	LastSeenTag  string    `gorm:"type:varchar(255);default:''"`
	LastPolledAt time.Time `gorm:"not null;default:now()"`
}

func (w WatchedRepo) IsNewRelease(tag string) bool { return tag != "" && tag != w.LastSeenTag }

// RepoRegistration is one subscription's interest in a repo. A repo is polled
// while it has at least one registration. subscription_id is the orchestrator-minted
// cross-service identity, which makes register/release idempotent.
type RepoRegistration struct {
	SubscriptionID string `gorm:"primaryKey;type:uuid"`
	Repo           string `gorm:"type:varchar(255);not null;index"`
	CreatedAt      time.Time
}
