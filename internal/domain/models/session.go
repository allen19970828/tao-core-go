package models

import (
	"time"
)

// SessionStatus 定義測驗會話的生命週期狀態機。
type SessionStatus string

const (
	SessionStatusNotStarted SessionStatus = "NOT_STARTED" // 未開始
	SessionStatusInProgress SessionStatus = "IN_PROGRESS" // 進行中
	SessionStatusPaused     SessionStatus = "PAUSED"      // 暫停
	SessionStatusCompleted  SessionStatus = "COMPLETED"   // 已交卷
)

// TestSession 代表學生參加某次試驗發布 (Delivery) 的測驗會話實體。
type TestSession struct {
	ID               string         `gorm:"primaryKey;type:varchar(36)" json:"id"`
	DeliveryID       string         `gorm:"type:varchar(36);not null;index;uniqueIndex:idx_delivery_user_attempt,priority:1" json:"delivery_id"`
	Delivery         *Delivery      `gorm:"foreignKey:DeliveryID" json:"delivery,omitempty"`
	UserID           string         `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_delivery_user_attempt,priority:2" json:"user_id"`
	Attempt          int            `gorm:"not null;default:1;uniqueIndex:idx_delivery_user_attempt,priority:3" json:"attempt"`
	Status           SessionStatus  `gorm:"type:varchar(20);default:'NOT_STARTED';index" json:"status"`
	TimeSpentSeconds int            `gorm:"default:0" json:"time_spent_seconds"` // 答題總耗時 (秒)
	TotalScore       float64        `gorm:"default:0.0" json:"total_score"`      // 自動計分總得分
	Responses        []ItemResponse `gorm:"foreignKey:SessionID" json:"responses,omitempty"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// ItemResponse 代表學生對單一試題的答題與得分紀錄。
type ItemResponse struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	SessionID    string    `gorm:"type:varchar(36);not null;index;uniqueIndex:idx_session_item,priority:1" json:"session_id"`
	ItemID       string    `gorm:"type:varchar(36);not null;index;uniqueIndex:idx_session_item,priority:2" json:"item_id"`
	ResponseData string    `gorm:"type:text" json:"response_data"` // 答案字串 (例如 "A" 或 "A,C")
	ScoreGiven   float64   `gorm:"default:0.0" json:"score_given"`
	IsCorrect    bool      `gorm:"default:false" json:"is_correct"`
	RespondedAt  time.Time `json:"responded_at"`
}
