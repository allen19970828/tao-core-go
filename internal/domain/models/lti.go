package models

import (
	"time"
)

type LTIPlatform struct {
	ID            string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Issuer        string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_lti_platform_registration,priority:1" json:"issuer"` // e.g. https://moodle.example.com
	ClientID      string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_lti_platform_registration,priority:2" json:"client_id"`
	KeySetURL     string    `gorm:"type:varchar(500);not null" json:"keyset_url"`
	AuthTokenURL  string    `gorm:"type:varchar(500);not null" json:"auth_token_url"`
	AuthLoginURL  string    `gorm:"type:varchar(500);not null" json:"auth_login_url"`
	ToolLaunchURL string    `gorm:"type:varchar(500);not null" json:"tool_launch_url"`
	KeyID         string    `gorm:"type:varchar(255)" json:"key_id,omitempty"`
	PrivateKey    string    `gorm:"type:text" json:"-"` // RSA Private Key (PEM)
	PublicKey     string    `gorm:"type:text" json:"-"` // RSA Public Key (PEM)
	CreatedAt     time.Time `json:"created_at"`
}

func (LTIPlatform) TableName() string {
	return "lti_platforms"
}

type LTILinkSession struct {
	SessionID      string    `gorm:"primaryKey;type:varchar(36)" json:"session_id"`
	PlatformID     string    `gorm:"type:varchar(36);index;not null" json:"platform_id"`
	Issuer         string    `gorm:"type:varchar(255);not null" json:"issuer"`
	ClientID       string    `gorm:"type:varchar(255);not null" json:"client_id"`
	LISUserID      string    `gorm:"type:varchar(255);not null" json:"lis_user_id"`
	DeploymentID   string    `gorm:"type:varchar(255);not null" json:"deployment_id"`
	ResourceLinkID string    `gorm:"type:varchar(255);not null" json:"resource_link_id"`
	LineItemURL    string    `gorm:"type:varchar(500)" json:"lineitem_url"` // AGS Grade Endpoint
	CreatedAt      time.Time `json:"created_at"`
}

func (LTILinkSession) TableName() string {
	return "lti_link_sessions"
}

type LTIOIDCState struct {
	State          string     `gorm:"primaryKey;type:varchar(64)" json:"-"`
	Nonce          string     `gorm:"type:varchar(64);not null" json:"-"`
	Issuer         string     `gorm:"type:varchar(255);index;not null" json:"-"`
	ClientID       string     `gorm:"type:varchar(255);not null" json:"-"`
	TargetLinkURI  string     `gorm:"type:varchar(500);not null" json:"-"`
	LTIMessageHint string     `gorm:"type:text" json:"-"`
	ExpiresAt      time.Time  `gorm:"index;not null" json:"-"`
	UsedAt         *time.Time `json:"-"`
	CreatedAt      time.Time  `json:"-"`
}

func (LTIOIDCState) TableName() string { return "lti_oidc_states" }

type LTIResourceLink struct {
	ID             string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	PlatformID     string    `gorm:"type:varchar(36);index;not null;uniqueIndex:idx_lti_resource_mapping,priority:1" json:"platform_id"`
	DeploymentID   string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_lti_resource_mapping,priority:2" json:"deployment_id"`
	ResourceLinkID string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_lti_resource_mapping,priority:3" json:"resource_link_id"`
	DeliveryID     string    `gorm:"type:varchar(36);index;not null" json:"delivery_id"`
	CreatedAt      time.Time `json:"created_at"`
}

func (LTIResourceLink) TableName() string { return "lti_resource_links" }

type AGSGradePayload struct {
	Timestamp        string  `json:"timestamp"`
	ScoreGiven       float64 `json:"scoreGiven"`
	ScoreMaximum     float64 `json:"scoreMaximum"`
	Comment          string  `json:"comment"`
	ActivityProgress string  `json:"activityProgress"` // "Completed"
	GradingProgress  string  `json:"gradingProgress"`  // "FullyGraded"
	UserID           string  `json:"userId"`
}
