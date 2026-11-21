package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/pkg/s3store"
)

// S3Archive is a ColdStore backed by S3-compatible storage (RustFS / COS).
type S3Archive struct {
	client *s3store.Client
}

// NewS3Archive wraps an s3store client.
func NewS3Archive(c *s3store.Client) *S3Archive {
	return &S3Archive{client: c}
}

func (a *S3Archive) Name() string { return "s3" }

func (a *S3Archive) Put(ctx context.Context, key string, snap domain.ArchiveSnapshot) error {
	if a == nil || a.client == nil || !a.client.Configured() {
		return fmt.Errorf("s3 archive not configured")
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	key = strings.TrimPrefix(key, "/")
	return a.client.Put(ctx, key, raw, "application/json")
}

func (a *S3Archive) Get(ctx context.Context, key string) (*domain.ArchiveSnapshot, error) {
	if a == nil || a.client == nil || !a.client.Configured() {
		return nil, fmt.Errorf("s3 archive not configured")
	}
	key = strings.TrimPrefix(key, "/")
	body, err := a.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	var snap domain.ArchiveSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// ArchiveKeyForPackage builds tracking-events/<tn>/<package_id>.json: one object per
// generation, named by its package_id, so it is derivable from the package row alone
// and re-archiving after a restore overwrites the same object.
func ArchiveKeyForPackage(pkg domain.Package) string {
	return fmt.Sprintf("tracking-events/%s/%d.json", url.PathEscape(pkg.TrackingNumber), pkg.PackageID)
}

var _ domain.ColdStore = (*S3Archive)(nil)
