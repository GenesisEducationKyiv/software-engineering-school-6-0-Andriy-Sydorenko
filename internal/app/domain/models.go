package domain

import "time"

type Subscription struct {
	ID               uint      `gorm:"primaryKey" json:"-"`
	Email            string    `gorm:"type:varchar(255);not null" json:"email"`
	Repo             string    `gorm:"type:varchar(255);not null" json:"repo"`
	Confirmed        bool      `gorm:"default:false;not null" json:"confirmed"`
	UnsubscribeToken string    `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

// WatchedRepo is the scanner's per-repo release cursor: the last release tag it
// has already notified subscribers about. One row per repo, regardless of how
// many subscriptions point at it.
type WatchedRepo struct {
	Repo         string    `gorm:"primaryKey;type:varchar(255)" json:"repo"`
	LastSeenTag  string    `gorm:"type:varchar(255);default:''" json:"last_seen_tag"`
	LastPolledAt time.Time `gorm:"not null;default:now()" json:"-"`
}

// IsNewRelease reports whether tag is a release this repo has not been notified
// about yet. Empty tags are ignored so an upstream "no releases yet" response is
// not treated as a regression.
func (w *WatchedRepo) IsNewRelease(tag string) bool {
	return tag != "" && tag != w.LastSeenTag
}

type ConfirmationToken struct {
	ID             uint         `gorm:"primaryKey" json:"-"`
	Token          string       `gorm:"type:varchar(255);uniqueIndex;not null" json:"token"`
	SubscriptionID uint         `gorm:"not null;index" json:"-"`
	Subscription   Subscription `gorm:"foreignKey:SubscriptionID;constraint:OnDelete:CASCADE" json:"-"`
	CreatedAt      time.Time    `json:"-"`
}
