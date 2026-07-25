package models

import (
	"time"
)

type Group struct {
	ID        string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	GroupCode string    `gorm:"type:varchar(50);uniqueIndex;not null" json:"group_code"` // e.g. "CLASS_101"
	GroupName string    `gorm:"type:varchar(100);not null" json:"group_name"`
	CreatedAt time.Time `json:"created_at"`
}

func (Group) TableName() string {
	return "groups"
}

type UserGroup struct {
	UserID  string `gorm:"primaryKey;type:varchar(36)" json:"user_id"`
	GroupID string `gorm:"primaryKey;type:varchar(36)" json:"group_id"`
}

type DeliveryGroup struct {
	DeliveryID string `gorm:"primaryKey;type:varchar(36)" json:"delivery_id"`
	GroupID    string `gorm:"primaryKey;type:varchar(36)" json:"group_id"`
}
