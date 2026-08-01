package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"tao-core-go/internal/database"
)

func main() {
	mode := flag.String("mode", "verify", "migration mode: verify (read-only) or up")
	flag.Parse()

	driver := strings.TrimSpace(os.Getenv("DATABASE_DRIVER"))
	dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN"))
	serverMode := strings.TrimSpace(os.Getenv("SERVER_MODE"))
	if driver == "" || dsn == "" {
		log.Fatal("DATABASE_DRIVER and DATABASE_DSN are required")
	}
	if serverMode == "" {
		serverMode = "release"
	}
	allowInsecure, err := strconv.ParseBool(defaultValue(os.Getenv("DATABASE_ALLOW_INSECURE_INTERNAL"), "false"))
	if err != nil {
		log.Fatal("DATABASE_ALLOW_INSECURE_INTERNAL must be true or false")
	}
	if err := database.ValidateTransportSecurity(serverMode, driver, dsn, allowInsecure); err != nil {
		log.Fatalf("database transport validation failed: %v", err)
	}

	db, err := database.Open(driver, dsn)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("database handle failed: %v", err)
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		log.Fatalf("database ping failed: %v", err)
	}

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "verify":
		err = database.VerifySchema(db)
	case "up":
		err = database.Migrate(db)
	default:
		log.Fatal("mode must be verify or up")
	}
	if err != nil {
		log.Fatalf("database %s failed: %v", *mode, err)
	}
	fmt.Printf("database %s completed successfully\n", *mode)
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
