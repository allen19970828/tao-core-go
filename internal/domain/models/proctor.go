package models

import (
	"time"
)

type ProctorEventType string

const (
	ProctorEventTabSwitch         ProctorEventType = "TAB_SWITCH"
	ProctorEventFocusLost         ProctorEventType = "FOCUS_LOST"
	ProctorEventScreenshotAttempt ProctorEventType = "SCREENSHOT_ATTEMPT"
	ProctorEventCopyAttempt       ProctorEventType = "COPY_ATTEMPT"
	ProctorEventFullscreenExit    ProctorEventType = "FULLSCREEN_EXIT"
)

type ProctorEvent struct {
	ID              string           `gorm:"primaryKey;type:varchar(36)" json:"id"`
	SessionID       string           `gorm:"type:varchar(36);index;not null" json:"session_id"`
	EventType       ProctorEventType `gorm:"type:varchar(50);index;not null" json:"event_type"`
	DurationSeconds int              `gorm:"default:0" json:"duration_seconds"` // 離開頁面時長 (秒)
	Details         string           `gorm:"type:text" json:"details"`          // Extra JSON metadata
	CreatedAt       time.Time        `json:"created_at"`
}

func (ProctorEvent) TableName() string {
	return "proctor_events"
}

type ProctorAnalyticsSummary struct {
	SessionID               string `json:"session_id"`
	TotalTabSwitches        int    `json:"total_tab_switches"`
	TotalFocusLostSeconds   int    `json:"total_focus_lost_seconds"`
	TotalScreenshotAttempts int    `json:"total_screenshot_attempts"`
	TotalCopyAttempts       int    `json:"total_copy_attempts"`
	RiskLevel               string `json:"risk_level"` // LOW, MEDIUM, HIGH
}
