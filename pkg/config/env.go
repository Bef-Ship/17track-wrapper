package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// AppEnv returns the normalized APP_ENV (production|develop|local|…).
func AppEnv() string {
	return normalizeAppEnv(Env("APP_ENV", "local"))
}

// IsProduction reports whether APP_ENV is production (or prod).
func IsProduction() bool {
	return AppEnv() == "production"
}

// EnvInt parses an integer env var; unset or empty returns fallback.
func EnvInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func EnvCSV(key, fallback string) []string {
	raw := Env(key, fallback)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func JWTSecret() string {
	if s := os.Getenv("GOTRUE_JWT_SECRET"); s != "" {
		return s
	}
	if s := os.Getenv("JWT_SECRET"); s != "" {
		return s
	}
	return devJWTSecret
}

// devJWTSecret is the local docker-compose GoTrue secret; never valid in production.
const devJWTSecret = "super-secret-jwt-token-with-at-least-32-characters-long"

// RequireJWTSecret fails when production would fall back to the public dev secret,
// which would let anyone mint accepted tokens.
func RequireJWTSecret(production bool) error {
	if production && JWTSecret() == devJWTSecret {
		return fmt.Errorf("GOTRUE_JWT_SECRET (or JWT_SECRET) must be set in production")
	}
	return nil
}

func DatabaseURL() string {
	return Env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable")
}

// AccessDatabaseURL is the Access BC pool (search_path prefers access).
// Same cluster may still query public.tenants via schema-qualified SQL.
func AccessDatabaseURL() string {
	return Env("ACCESS_DATABASE_URL",
		"postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable&search_path=access,public")
}

// BillingDatabaseURL is the Billing BC pool (plans, credits, suite_product catalog).
func BillingDatabaseURL() string {
	return Env("BILLING_DATABASE_URL",
		"postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable&search_path=billing,public")
}

// ShipmentDatabaseURL is the Shipment BC pool (tables in public, with Tenant).
func ShipmentDatabaseURL() string {
	return Env("SHIPMENT_DATABASE_URL",
		"postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable&search_path=public")
}

// TrackingDatabaseURL is the Tracking BC pool.
func TrackingDatabaseURL() string {
	return Env("TRACKING_DATABASE_URL",
		"postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable&search_path=tracking,public")
}

func RedisURL() string {
	return Env("REDIS_URL", "redis://127.0.0.1:6379/0")
}

func DefaultCORSOrigins() []string {
	return EnvCSV("CORS_ORIGINS",
		"http://localhost:5173,http://localhost:5174,http://localhost:5175")
}
