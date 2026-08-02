package database

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

// Open creates the configured database connection without logging the DSN.
func Open(driver, dsn string) (*gorm.DB, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql":
		return gorm.Open(postgres.Open(dsn), &gorm.Config{})
	case "sqlite", "sqlite3", "":
		return gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	default:
		return nil, fmt.Errorf("不支援的 database.driver: %s", driver)
	}
}

// IsSQLiteDriver reports whether the configured driver uses SQLite semantics.
func IsSQLiteDriver(driver string) bool {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return true
	default:
		return false
	}
}

// ValidateTransportSecurity prevents release deployments from silently using
// plaintext PostgreSQL outside an explicitly isolated internal network.
func ValidateTransportSecurity(mode, driver, dsn string, allowInsecureInternal bool) error {
	if strings.EqualFold(strings.TrimSpace(mode), "release") && !IsSQLiteDriver(driver) &&
		strings.Contains(strings.ToLower(dsn), "sslmode=disable") && !allowInsecureInternal {
		return errors.New("release 模式的 PostgreSQL 不可使用 sslmode=disable；僅隔離的內部容器網路可顯式設定 DATABASE_ALLOW_INSECURE_INTERNAL=true")
	}
	return nil
}

func schemaModels() []any {
	return []any{
		&models.User{},
		&models.Role{},
		&models.Permission{},
		&models.UserRole{},
		&models.RolePermission{},
		&models.Item{},
		&models.Test{},
		&models.TestSection{},
		&models.TestItem{},
		&models.Delivery{},
		&models.TestSession{},
		&models.ItemResponse{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.LTIPlatform{},
		&models.LTIOIDCState{},
		&models.LTIResourceLink{},
		&models.LTILinkSession{},
		&models.ProctorEvent{},
		&models.Group{},
		&models.UserGroup{},
		&models.DeliveryGroup{},
	}
}

var requiredIndexes = []struct {
	model any
	name  string
}{
	{&models.TestSession{}, "idx_delivery_user_attempt"},
	{&models.ItemResponse{}, "idx_session_item"},
	{&models.LTIPlatform{}, "idx_lti_platform_registration"},
	{&models.LTIResourceLink{}, "idx_lti_resource_mapping"},
}

// Migrate applies the additive GORM schema migration and then verifies the
// tables and uniqueness constraints relied on by concurrency-sensitive paths.
func Migrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("database connection is nil")
	}
	if err := db.AutoMigrate(schemaModels()...); err != nil {
		return fmt.Errorf("auto-migrate schema: %w", err)
	}

	// Older releases allowed only one client per issuer. The replacement index
	// supports the LTI issuer + client_id registration key.
	if db.Migrator().HasIndex(&models.LTIPlatform{}, "idx_lti_platforms_issuer") {
		if err := db.Migrator().DropIndex(&models.LTIPlatform{}, "idx_lti_platforms_issuer"); err != nil {
			return fmt.Errorf("drop legacy LTI issuer index: %w", err)
		}
	}
	return VerifySchema(db)
}

// VerifySchema is read-only and can be run after migration or during staging
// readiness checks.
func VerifySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("database connection is nil")
	}
	for _, model := range schemaModels() {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("required database table is missing for %T", model)
		}
	}
	for _, index := range requiredIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf("required database index is missing: %s", index.name)
		}
	}
	return nil
}
