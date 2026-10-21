package model

import (
	"time"

	"github.com/jamaa/gathering/backend/internal/data/enum"
)

// Device is one signed-in phone that can be pushed to.
type Device struct {
	ID         string `gorm:"primaryKey"`
	SessionID  string
	PlayerID   string
	Platform   enum.Platform
	FCMToken   string `gorm:"column:fcm_token"`
	CreatedAt  time.Time
	LastSeenAt time.Time
}

func (Device) TableName() string { return "devices" }
