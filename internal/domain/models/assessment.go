package models

import (
	"time"
)

// ItemType 定義 QTI 與測驗系統支援的題目類型。
type ItemType string

const (
	ItemTypeSingleChoice   ItemType = "SINGLE_CHOICE"   // 單選題
	ItemTypeMultipleChoice ItemType = "MULTIPLE_CHOICE" // 多選題
	ItemTypeShortAnswer    ItemType = "SHORT_ANSWER"    // 簡答題
)

// Option 代表選擇題的單一選項結構。
type Option struct {
	Identifier string `json:"identifier"` // 選項識別碼 (A, B, C, D)
	Text       string `json:"text"`       // 選項內容或 HTML 描述
}

// Item 代表獨立的試題實體 (對應 QTI 3.0 AssessmentItem)。
type Item struct {
	ID            string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Title         string    `gorm:"type:varchar(255);not null" json:"title"`
	Prompt        string    `gorm:"type:text;not null" json:"prompt"`                   // 題幹文字或 HTML
	ItemType      ItemType  `gorm:"type:varchar(50);not null" json:"item_type"`         // 題目類型
	OptionsJSON   string    `gorm:"type:text" json:"options_json"`                      // 序列化後的選項陣列
	CorrectAnswer string    `gorm:"type:varchar(255);not null" json:"-"`                // 正確答案永不透過 API 序列化
	MaxScore      float64   `gorm:"default:1.0" json:"max_score"`                       // 本題最高得分
	LayoutHint    string    `gorm:"type:varchar(50);default:'AUTO'" json:"layout_hint"` // 排版提示: AUTO, 1_COL, 2_COL, 4_COL
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Test 代表整張試驗/測驗卷 (對應 QTI 3.0 AssessmentTest)。
type Test struct {
	ID               string        `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Title            string        `gorm:"type:varchar(255);not null" json:"title"`
	Description      string        `gorm:"type:text" json:"description"`
	QTIVersion       string        `gorm:"type:varchar(20);default:'3.0'" json:"qti_version"`
	TimeLimitSeconds int           `gorm:"default:0" json:"time_limit_seconds"` // 考試時間限制 (秒)，0 為不限時
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	Sections         []TestSection `gorm:"foreignKey:TestID" json:"sections,omitempty"`
}

// TestSection 代表試卷大題或測驗章節 (對應 QTI AssessmentSection)。
type TestSection struct {
	ID          string     `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TestID      string     `gorm:"type:varchar(36);index;not null" json:"test_id"`
	Title       string     `gorm:"type:varchar(255);not null" json:"title"`
	OrderIndex  int        `gorm:"default:1" json:"order_index"`
	SectionType string     `gorm:"type:varchar(50);default:'MAIN'" json:"section_type"`
	Items       []TestItem `gorm:"foreignKey:SectionID" json:"items,omitempty"`
}

// TestItem 代表試卷大題與題目之間的關聯實體 (包含配分權重與題號順序)。
type TestItem struct {
	ID         string  `gorm:"primaryKey;type:varchar(36)" json:"id"`
	SectionID  string  `gorm:"type:varchar(36);index;not null" json:"section_id"`
	ItemID     string  `gorm:"type:varchar(36);index;not null" json:"item_id"`
	OrderIndex int     `gorm:"default:1" json:"order_index"`
	Weight     float64 `gorm:"default:1.0" json:"weight"` // 計分配分權重
	Item       Item    `gorm:"foreignKey:ItemID" json:"item,omitempty"`
}

// Delivery 代表考務發布實體 (將 Test 發布給學生團體參加的考試場次)。
type Delivery struct {
	ID          string     `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TestID      string     `gorm:"type:varchar(36);index;not null" json:"test_id"`
	Title       string     `gorm:"type:varchar(255);not null" json:"title"`
	StartTime   *time.Time `json:"start_time,omitempty"` // 開考開放時間
	EndTime     *time.Time `json:"end_time,omitempty"`   // 截止報名/考試時間
	IsActive    bool       `gorm:"default:true" json:"is_active"`
	MaxAttempts int        `gorm:"default:1" json:"max_attempts"` // 允許最大應試次數
	CreatedAt   time.Time  `json:"created_at"`
	Test        Test       `gorm:"foreignKey:TestID" json:"test,omitempty"`
}
