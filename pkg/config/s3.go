package config

import "strings"

// Default Tencent COS APPID suffix used in bucket names when S3_APPID / COS_APPID unset.
const defaultS3AppID = "1490074736"

// S3AppID returns the COS APPID used as bucket-name suffix.
func S3AppID() string {
	return Env("S3_APPID", Env("COS_APPID", defaultS3AppID))
}

// S3DiskBucket is the studio/admin cloud-disk bucket. Same as S3AppBucket
// (shared S3_BUCKET); kept as a named accessor for studio wiring.
func S3DiskBucket() string {
	return S3AppBucket()
}

// S3FEBucket is the frontend static deploy bucket. Defaults to S3AppBucket
// (same shared bucket). Optional S3_FE_BUCKET override only if you intentionally split.
func S3FEBucket() string {
	if v := Env("S3_FE_BUCKET", ""); v != "" {
		return v
	}
	return S3AppBucket()
}

// S3AppBucket is the shared object bucket for tenant uploads, ops cloud-disk,
// and (by default) FE static (production-1490074736 / develop-1490074736).
// Override with S3_BUCKET. When unset, derived from APP_ENV; local leaves empty
// so compose can set an explicit name.
func S3AppBucket() string {
	if v := Env("S3_BUCKET", ""); v != "" {
		return v
	}
	switch normalizeAppEnv(Env("APP_ENV", "local")) {
	case "production":
		return "production-" + S3AppID()
	case "develop":
		return "develop-" + S3AppID()
	default:
		return ""
	}
}

func normalizeAppEnv(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "production", "prod":
		return "production"
	case "develop", "development", "dev", "test", "staging":
		return "develop"
	case "local", "":
		return "local"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}
