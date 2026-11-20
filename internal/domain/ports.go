package domain

import (
	"context"
	"time"
)

// Repository persists packages and tracking events. "By tracking number" lookups
// return the current generation; older generations are reached by package id.
type Repository interface {
	WithTx(ctx context.Context, fn func(Repository) error) error

	CreatePackage(ctx context.Context, pkg *Package) error
	GetPackageByID(ctx context.Context, packageID int64) (*Package, error)
	GetPackageByTrackingNumber(ctx context.Context, trackingNumber string) (*Package, error)
	// GetPackageForUpdate row-locks the current generation of trackingNumber.
	GetPackageForUpdate(ctx context.Context, trackingNumber string) (*Package, error)
	GetPackageByIDForUpdate(ctx context.Context, packageID int64) (*Package, error)
	UpdatePackage(ctx context.Context, pkg *Package) error
	ListPackages(ctx context.Context, f ListPackagesFilter) ([]Package, error)
	// ListPackageGenerations returns every generation of trackingNumber, newest first.
	ListPackageGenerations(ctx context.Context, trackingNumber string) ([]Package, error)
	// ListPackagesDueArchive pages inactive, unarchived packages last updated before inactiveBefore.
	ListPackagesDueArchive(ctx context.Context, inactiveBefore time.Time, limit int) ([]Package, error)
	// ListPackagesBySubscriptionStatus pages current generations only.
	ListPackagesBySubscriptionStatus(ctx context.Context, status string, afterID int64, limit int) ([]Package, error)
	// ListTrackingNotHeardSince pages current TRACKING packages whose last_callback_at is NULL or before since.
	ListTrackingNotHeardSince(ctx context.Context, since time.Time, afterID int64, limit int) ([]Package, error)

	// InsertEvents skips fingerprints already stored and returns only new rows.
	InsertEvents(ctx context.Context, events []TrackingEvent) ([]TrackingEvent, error)
	ListEventsByPackage(ctx context.Context, packageID int64) ([]TrackingEvent, error)
	DeleteEventsByPackage(ctx context.Context, packageID int64) error

	TryLock(ctx context.Context, name string) (release func(), ok bool, err error)
}

// ColdStore holds archived package event snapshots via obj-gateway.
type ColdStore interface {
	Put(ctx context.Context, key string, snap ArchiveSnapshot) error
	Get(ctx context.Context, key string) (*ArchiveSnapshot, error)
	Name() string
}

// IdempotencyStore dedups 17TRACK webhook deliveries.
type IdempotencyStore interface {
	// Claim atomically reserves key; false means another delivery already holds it.
	Claim(ctx context.Context, key string, ttlSeconds int) (bool, error)
	// Release frees a claimed key so a failed delivery can be retried.
	Release(ctx context.Context, key string) error
}

// RegisterJobPublisher enqueues deferred 17TRACK register attempts.
type RegisterJobPublisher interface {
	EnqueueRegister(ctx context.Context, job RegisterJob) error
}
