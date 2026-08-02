package models

import (
	"time"
)

// User 代表系統使用者實體 (學生、教師、管理員或考務人員)。
type User struct {
	ID        string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Username  string    `gorm:"type:varchar(100);uniqueIndex;not null" json:"username"`
	Email     string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"email"`
	Password  string    `gorm:"type:varchar(255);not null" json:"-"` // 加密雜湊密碼 (JSON 不序列化)
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Roles []Role `gorm:"many2many:user_roles;" json:"roles,omitempty"`
}

// Role 代表 RBAC (Role-Based Access Control) 角色實體 (例如: ADMIN, TEACHER, STUDENT)。
type Role struct {
	ID          string       `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Name        string       `gorm:"type:varchar(50);uniqueIndex;not null" json:"name"`
	Description string       `gorm:"type:varchar(255)" json:"description"`
	Permissions []Permission `gorm:"many2many:role_permissions;" json:"permissions,omitempty"`
}

// Permission 代表系統細粒度操作權限 (例如: item:create, session:submit)。
type Permission struct {
	ID          string `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Code        string `gorm:"type:varchar(100);uniqueIndex;not null" json:"code"`
	Description string `gorm:"type:varchar(255)" json:"description"`
}

// UserRole 代表使用者與角色的多對多關聯實體。
type UserRole struct {
	UserID string `gorm:"primaryKey;type:varchar(255)" json:"user_id"`
	RoleID string `gorm:"primaryKey;type:varchar(36)" json:"role_id"`
}

// RolePermission 代表角色與權限的多對多關聯實體。
type RolePermission struct {
	RoleID       string `gorm:"primaryKey;type:varchar(36)" json:"role_id"`
	PermissionID string `gorm:"primaryKey;type:varchar(36)" json:"permission_id"`
}
