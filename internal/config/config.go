package config

import (
	"os"
	"strconv"
	"strings"
)

// Config is process configuration loaded from environment variables.
type Config struct {
	HTTPAddr    string
	LogLevel    string
	DatabaseURL string
	APIKey      string // required on /v1/* when set

	ArchiveEnabled       bool
	ArchiveRetentionDays int
	ArchiveBatchSize     int
	ArchiveSchedule      string // daily HH:MM in ArchiveTimezone; "off" disables the in-process job
	ArchiveTimezone      string
	S3                   S3Config

	OLAPEnabled bool
	OLAPDriver  string // clickhouse
	ClickHouse  ClickHouseConfig
}

// S3Config is S3-compatible object storage for cold event archives.
// Tencent COS: set Endpoint to https://cos.<region>.myqcloud.com.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// ClickHouseConfig is the TCHouse-C / ClickHouse connection used by the OLAP adapter.
type ClickHouseConfig struct {
	DSN         string
	Table       string
	AsyncInsert bool
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		HTTPAddr:    getenv("HTTP_ADDR", ":8092"),
		LogLevel:    getenv("LOG_LEVEL", "info"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/tracking?sslmode=disable"),
		APIKey:      os.Getenv("API_KEY"),

		ArchiveEnabled:       getenvBool("ARCHIVE_ENABLED", false),
		ArchiveRetentionDays: getenvInt("ARCHIVE_RETENTION_DAYS", 90),
		ArchiveBatchSize:     getenvInt("ARCHIVE_BATCH_SIZE", 200),
		ArchiveSchedule:      getenv("ARCHIVE_SCHEDULE", "03:00"),
		ArchiveTimezone:      getenv("ARCHIVE_TIMEZONE", "Asia/Shanghai"),
		S3: S3Config{
			Endpoint:  os.Getenv("S3_ENDPOINT"),
			Region:    getenv("S3_REGION", "ap-shanghai"),
			Bucket:    os.Getenv("S3_BUCKET"),
			AccessKey: firstEnv("S3_ACCESS_KEY", "AWS_ACCESS_KEY_ID"),
			SecretKey: firstEnv("S3_SECRET_KEY", "AWS_SECRET_ACCESS_KEY"),
		},

		OLAPEnabled: getenvBool("OLAP_ENABLED", false),
		OLAPDriver:  getenv("OLAP_DRIVER", "clickhouse"),
		ClickHouse: ClickHouseConfig{
			DSN:         os.Getenv("CLICKHOUSE_DSN"),
			Table:       getenv("CLICKHOUSE_TABLE", "tracking_event_olap"),
			AsyncInsert: getenvBool("CLICKHOUSE_ASYNC_INSERT", true),
		},
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
