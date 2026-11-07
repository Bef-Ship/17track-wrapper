package archive

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
)

// ColdStore holds archived package event snapshots in S3-compatible storage.
type ColdStore interface {
	// Put writes a snapshot and returns the storage key.
	Put(ctx context.Context, key string, snap domain.ArchiveSnapshot) error
	// Get loads a snapshot by key.
	Get(ctx context.Context, key string) (*domain.ArchiveSnapshot, error)
	// Name for logging.
	Name() string
}

// KeyForPackage builds a stable archive object key:
// tracking-events/<tenant_id>/<delivered yyyy/mm/dd>/<tracking_number>.json
func KeyForPackage(pkg domain.Package) string {
	day := time.Now().UTC().Format("2006/01/02")
	if pkg.DeliveredAt != nil {
		day = pkg.DeliveredAt.UTC().Format("2006/01/02")
	}
	return fmt.Sprintf("tracking-events/%s/%s/%s.json", pkg.TenantID, day, pkg.TrackingNumber)
}
