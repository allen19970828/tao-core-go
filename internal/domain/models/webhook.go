package models

import (
	"time"
)

// WebhookConfig 代表 Webhook 事件訂閱點設定實體。
type WebhookConfig struct {
	ID          string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Event       string    `gorm:"type:varchar(100);index;not null" json:"event"` // 例如 "session.completed"
	TargetURL   string    `gorm:"type:varchar(500);not null" json:"target_url"`
	HTTPMethod  string    `gorm:"type:varchar(10);default:'POST'" json:"http_method"`
	SecretToken string    `gorm:"type:varchar(255)" json:"-"`
	IsActive    bool      `gorm:"default:true" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

// WebhookLog 代表 Webhook 異步推播特性的歷史執行紀錄。
type WebhookLog struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	ConfigID     string    `gorm:"type:varchar(36);index;not null" json:"config_id"`
	Event        string    `gorm:"type:varchar(100);not null" json:"event"`
	Payload      string    `gorm:"type:text" json:"payload"`
	StatusCode   int       `json:"status_code"`
	ResponseBody string    `gorm:"type:text" json:"response_body"`
	IsSuccess    bool      `gorm:"default:false" json:"is_success"`
	CreatedAt    time.Time `json:"created_at"`
}
