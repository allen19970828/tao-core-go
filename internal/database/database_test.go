package database

import (
	"testing"

	"tao-core-go/internal/domain/models"
)

func TestMigrateAndVerifySchema(t *testing.T) {
	db, err := Open("sqlite", t.TempDir()+"/schema.db")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.Exec("CREATE TABLE IF NOT EXISTS lti_platforms (id text, issuer text)").Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_lti_platforms_issuer ON lti_platforms(issuer)").Error; err != nil {
		t.Fatalf("create legacy index: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	if err := VerifySchema(db); err != nil {
		t.Fatalf("verify migrated schema: %v", err)
	}
	if db.Migrator().HasIndex(&models.LTIPlatform{}, "idx_lti_platforms_issuer") {
		t.Fatal("legacy single-column LTI issuer index was not removed")
	}

	if err := db.Migrator().DropIndex(&models.ItemResponse{}, "idx_session_item"); err != nil {
		t.Fatalf("drop required index: %v", err)
	}
	if err := VerifySchema(db); err == nil {
		t.Fatal("expected verification to reject a missing required index")
	}
}

func TestDatabaseConfigurationValidation(t *testing.T) {
	if err := ValidateTransportSecurity("release", "postgres", "host=db sslmode=disable", false); err == nil {
		t.Fatal("expected insecure release PostgreSQL DSN to be rejected")
	}
	if err := ValidateTransportSecurity("release", "postgres", "host=db sslmode=verify-full", false); err != nil {
		t.Fatalf("expected verified TLS DSN to pass: %v", err)
	}
	if err := ValidateTransportSecurity("release", "postgres", "host=postgres sslmode=disable", true); err != nil {
		t.Fatalf("expected explicit isolated-network exception to pass: %v", err)
	}
	if _, err := Open("unsupported", "ignored"); err == nil {
		t.Fatal("expected unknown database driver to fail")
	}
	if err := VerifySchema(nil); err == nil {
		t.Fatal("expected nil database verification to fail")
	}
}
