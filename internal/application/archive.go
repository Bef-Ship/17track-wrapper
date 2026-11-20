package application

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
	"github.com/SolaTyolo/17track-wrapper/internal/infrastructure"
	blog "github.com/SolaTyolo/17track-wrapper/pkg/log"
)

// ArchiveDuePackages moves the events of inactive packages (not REGISTERING / TRACKING,
// including superseded generations) untouched for the retention window to cold storage.
func (s *Service) ArchiveDuePackages(ctx context.Context) (int, error) {
	if s.cold == nil {
		return 0, fmt.Errorf("archive disabled")
	}
	release, ok, err := s.repo.TryLock(ctx, archiveLockName)
	if err != nil {
		return 0, fmt.Errorf("archive lock: %w", err)
	}
	if !ok {
		return 0, fmt.Errorf("archive job already running")
	}
	defer release()

	cutoff := time.Now().UTC().Add(-s.retention)
	total, failed := 0, 0
	for {
		pkgs, err := s.repo.ListPackagesDueArchive(ctx, cutoff, s.archiveBatch)
		if err != nil {
			return total, err
		}
		archived := 0
		for _, pkg := range pkgs {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			done, err := s.archiveOne(ctx, pkg, cutoff)
			if err != nil {
				failed++
				continue
			}
			if done {
				archived++
			}
		}
		total += archived
		if len(pkgs) < s.archiveBatch || archived == 0 {
			break
		}
	}
	if failed > 0 {
		blog.Svc("tracking").Warn("archive: some packages failed", "failed", failed, "archived", total)
	}
	return total, nil
}

// archiveOne uploads the snapshot before taking the row lock so object-store latency
// never holds it; the commit is skipped if the package moved since the snapshot (the
// orphaned object is overwritten on the next run, keys are deterministic).
func (s *Service) archiveOne(ctx context.Context, due domain.Package, cutoff time.Time) (bool, error) {
	events, err := s.repo.ListEventsByPackage(ctx, due.PackageID)
	if err != nil {
		return false, err
	}
	key := infrastructure.ArchiveKeyForPackage(due)
	if err := s.cold.Put(ctx, key, domain.ArchiveSnapshot{
		Package: due, Events: events, ArchivedAt: time.Now().UTC(),
	}); err != nil {
		return false, err
	}

	done := false
	err = s.repo.WithTx(ctx, func(tx domain.Repository) error {
		pkg, err := tx.GetPackageByIDForUpdate(ctx, due.PackageID)
		if err != nil {
			return err
		}
		if pkg.IsArchived || pkg.IsActive() || !pkg.UpdatedAt.Before(cutoff) {
			return nil
		}
		current, err := tx.ListEventsByPackage(ctx, pkg.PackageID)
		if err != nil {
			return err
		}
		if len(current) != len(events) || key != infrastructure.ArchiveKeyForPackage(*pkg) {
			return nil
		}
		pkg.IsArchived = true
		pkg.ArchiveKey = key
		if err := tx.UpdatePackage(ctx, pkg); err != nil {
			return err
		}
		if err := tx.DeleteEventsByPackage(ctx, pkg.PackageID); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

// Restore moves an archived generation's events back into the hot table. updated_at is
// bumped, so the package gets a fresh retention window before it is archived again.
// The cold object is kept; reading archived history does not need a restore.
func (s *Service) Restore(ctx context.Context, packageID int64) (domain.Package, error) {
	pkg, err := s.lookup(ctx, domain.PackageRef{PackageID: packageID})
	if err != nil {
		return domain.Package{}, err
	}
	if !pkg.IsArchived {
		return *pkg, nil
	}
	if s.cold == nil || pkg.ArchiveKey == "" {
		return domain.Package{}, domain.Errorf(domain.ErrNotConfigured, domain.CodeArchiveUnavailable,
			"package %d is archived but cold storage is not configured", packageID)
	}
	snap, err := s.cold.Get(ctx, pkg.ArchiveKey)
	if err != nil {
		return domain.Package{}, domain.Errorf(domain.ErrNotConfigured, domain.CodeArchiveUnavailable,
			"read archive %s: %v", pkg.ArchiveKey, err)
	}
	err = s.repo.WithTx(ctx, func(tx domain.Repository) error {
		locked, err := tx.GetPackageByIDForUpdate(ctx, packageID)
		if err != nil {
			return err
		}
		pkg = locked
		if !pkg.IsArchived {
			return nil
		}
		events := make([]domain.TrackingEvent, len(snap.Events))
		for i, ev := range snap.Events {
			ev.PackageID = pkg.PackageID
			events[i] = ev
		}
		if _, err := tx.InsertEvents(ctx, events); err != nil {
			return err
		}
		pkg.IsArchived = false
		pkg.ArchiveKey = ""
		return tx.UpdatePackage(ctx, pkg)
	})
	if err != nil {
		return domain.Package{}, err
	}
	return *pkg, nil
}
