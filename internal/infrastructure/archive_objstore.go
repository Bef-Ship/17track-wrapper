package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/pkg/objstore"
)

// ObjArchive is a ColdStore backed by obj-gateway via pkg/objstore.
type ObjArchive struct {
	client *objstore.Client
}

// NewObjArchive wraps an objstore client. Returns nil client-safe no-op if c is nil/unconfigured.
func NewObjArchive(c *objstore.Client) *ObjArchive {
	return &ObjArchive{client: c}
}

func (a *ObjArchive) Name() string { return "obj-gateway" }

func (a *ObjArchive) Put(ctx context.Context, key string, snap domain.ArchiveSnapshot) error {
	if a == nil || a.client == nil || !a.client.Configured() {
		return fmt.Errorf("obj-gateway archive not configured")
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	key = strings.TrimPrefix(key, "/")
	return a.client.PutText(ctx, key, string(raw))
}

func (a *ObjArchive) Get(ctx context.Context, key string) (*domain.ArchiveSnapshot, error) {
	if a == nil || a.client == nil || !a.client.Configured() {
		return nil, fmt.Errorf("obj-gateway archive not configured")
	}
	key = strings.TrimPrefix(key, "/")
	body, err := a.client.GetText(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("objstore get %s: %w", key, err)
	}
	var snap domain.ArchiveSnapshot
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
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

var _ domain.ColdStore = (*ObjArchive)(nil)
