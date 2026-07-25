package models

import (
	"time"
)

type LTIPlatform struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Issuer       string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"issuer"` // e.g. https://moodle.example.com
	ClientID     string    `gorm:"type:varchar(255);not null" json:"client_id"`
	KeySetURL    string    `gorm:"type:varchar(500);not null" json:"keyset_url"`
	AuthTokenURL string    `gorm:"type:varchar(500);not null" json:"auth_token_url"`
	AuthLoginURL string    `gorm:"type:varchar(500);not null" json:"auth_login_url"`
	PrivateKey   string    `gorm:"type:text" json:"-"` // RSA Private Key (PEM)
	PublicKey    string    `gorm:"type:text" json:"-"` // RSA Public Key (PEM)
	CreatedAt    time.Time `json:"created_at"`
}

func (LTIPlatform) TableName() string {
	return "lti_platforms"
}

type LTILinkSession struct {
	SessionID   string    `gorm:"primaryKey;type:varchar(36)" json:"session_id"`
	Issuer      string    `gorm:"type:varchar(255);not null" json:"issuer"`
	ClientID    string    `gorm:"type:varchar(255);not null" json:"client_id"`
	LISUserID   string    `gorm:"type:varchar(255);not null" json:"lis_user_id"`
	LineItemURL string    `gorm:"type:varchar(500)" json:"lineitem_url"` // AGS Grade Endpoint
	CreatedAt   time.Time `json:"created_at"`
}

func (LTILinkSession) TableName() string {
	return "lti_link_sessions"
}

type AGSGradePayload struct {
	Timestamp        string  `json:"timestamp"`
	ScoreGiven       float64 `json:"scoreGiven"`
	ScoreMaximum     float64 `json:"scoreMaximum"`
	Comment          string  `json:"comment"`
	ActivityProgress string  `json:"activityProgress"` // "Completed"
	GradingProgress  string  `json:"gradingProgress"`  // "FullyGraded"
	UserID           string  `json:"userId"`
}
