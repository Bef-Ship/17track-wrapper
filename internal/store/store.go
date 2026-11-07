package store

import (
	"context"
	"errors"
	"time"

	"github.com/SolaTyolo/tracking-service/internal/domain"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

// IsNotFound reports whether err is a missing-record error.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// PackageFilter selects packages of one tenant, newest first.
// Empty fields are ignored.
type PackageFilter struct {
	TenantID           string
	Statuses           []string
	CarrierCode        string
	ProviderName       string
	ExternalRef        string
	OriginCountry      string
	DestinationCountry string
	IsArchived         *bool
	CreatedFrom        *time.Time
	CreatedTo          *time.Time
	// BeforeID is the keyset cursor: only packages with package_id < BeforeID.
	BeforeID int64
	Limit    int
}

// Store is the hot PostgreSQL persistence port.
type Store interface {
	Ping(ctx context.Context) error

	// WithTx runs fn in one transaction; fn must use the Store it is given.
	// Calling WithTx on that Store again joins the same transaction.
	WithTx(ctx context.Context, fn func(Store) error) error

	CreatePackage(ctx context.Context, pkg *domain.Package) error
	GetPackageByTrackingNumber(ctx context.Context, tenantID, trackingNumber string) (*domain.Package, error)
	// GetPackageForUpdate is GetPackageByTrackingNumber that also row-locks the
	// package until the surrounding transaction ends. Only meaningful inside WithTx.
	GetPackageForUpdate(ctx context.Context, tenantID, trackingNumber string) (*domain.Package, error)
	UpdatePackage(ctx context.Context, pkg *domain.Package) error
	ListPackages(ctx context.Context, f PackageFilter) ([]domain.Package, error)
	ListPackagesDueArchive(ctx context.Context, deliveredBefore time.Time, limit int) ([]domain.Package, error)

	// InsertEvents skips events whose fingerprint already exists for the package
	// and returns only the newly inserted ones.
	InsertEvents(ctx context.Context, events []domain.TrackingEvent) ([]domain.TrackingEvent, error)
	ListEventsByPackage(ctx context.Context, packageID int64) ([]domain.TrackingEvent, error)
	DeleteEventsByPackage(ctx context.Context, packageID int64) error

	// TryLock takes a cluster-wide named lock without waiting. ok=false means
	// another holder has it; otherwise release must be called when done.
	TryLock(ctx context.Context, name string) (release func(), ok bool, err error)
}
