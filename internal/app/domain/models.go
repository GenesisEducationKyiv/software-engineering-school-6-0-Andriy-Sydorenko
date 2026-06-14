package domain

import "time"

type Subscription struct {
	ID               uint      `gorm:"primaryKey" json:"-"`
	Email            string    `gorm:"type:varchar(255);not null" json:"email"`
	Repo             string    `gorm:"type:varchar(255);not null" json:"repo"`
	Confirmed        bool      `gorm:"default:false;not null" json:"confirmed"`
	LastSeenTag      string    `gorm:"type:varchar(255);default:''" json:"last_seen_tag"`
	UnsubscribeToken string    `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

// IsNewTag reports whether tag represents a release the subscription
// has not been notified about yet. Empty tags are ignored so that an
// upstream "no releases yet" response is not treated as a regression.
func (s *Subscription) IsNewTag(tag string) bool {
	return tag != "" && tag != s.LastSeenTag
}

type ConfirmationToken struct {
	ID             uint         `gorm:"primaryKey" json:"-"`
	Token          string       `gorm:"type:varchar(255);uniqueIndex;not null" json:"token"`
	SubscriptionID uint         `gorm:"not null;index" json:"-"`
	Subscription   Subscription `gorm:"foreignKey:SubscriptionID;constraint:OnDelete:CASCADE" json:"-"`
	CreatedAt      time.Time    `json:"-"`
}
