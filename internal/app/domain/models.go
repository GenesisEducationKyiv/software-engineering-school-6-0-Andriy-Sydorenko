package domain

import "time"

type Subscription struct {
	ID               uint      `gorm:"primaryKey" json:"-"`
	PublicID         string    `gorm:"type:uuid;uniqueIndex;not null" json:"-"`
	Email            string    `gorm:"type:varchar(255);not null" json:"email"`
	Repo             string    `gorm:"type:varchar(255);not null" json:"repo"`
	Confirmed        bool      `gorm:"default:false;not null" json:"confirmed"`
	UnsubscribeToken string    `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	CreatedAt        time.Time `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

type ConfirmationToken struct {
	ID             uint         `gorm:"primaryKey" json:"-"`
	Token          string       `gorm:"type:varchar(255);uniqueIndex;not null" json:"token"`
	SubscriptionID uint         `gorm:"not null;index" json:"-"`
	Subscription   Subscription `gorm:"foreignKey:SubscriptionID;constraint:OnDelete:CASCADE" json:"-"`
	CreatedAt      time.Time    `json:"-"`
}
