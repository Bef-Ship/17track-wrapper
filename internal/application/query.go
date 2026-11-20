package application

import (
	"context"
	"errors"
	"strings"

	"github.com/SolaTyolo/17track-wrapper/internal/domain"
)

// ListPackages returns packages with filters.
func (s *Service) ListPackages(ctx context.Context, f domain.ListPackagesFilter) (domain.ListPackagesResult, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	items, err := s.repo.ListPackages(ctx, f)
	if err != nil {
		return domain.ListPackagesResult{}, err
	}
	res := domain.ListPackagesResult{Items: items, Limit: f.Limit, Offset: f.Offset}
	if len(items) == f.Limit {
		res.NextCursor = items[len(items)-1].PackageID
	}
	return res, nil
}

func (s *Service) lookup(ctx context.Context, ref domain.PackageRef) (*domain.Package, error) {
	if ref.PackageID > 0 {
		pkg, err := s.repo.GetPackageByID(ctx, ref.PackageID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.Errorf(domain.ErrNotFound, domain.CodeNotRegistered, "package_id %d does not exist.", ref.PackageID)
		}
		return pkg, err
	}
	tn := strings.TrimSpace(ref.Number)
	if tn == "" {
		return nil, domain.ErrRequired("number")
	}
	extRef := strings.TrimSpace(ref.ExternalRef)
	if extRef == "" {
		pkg, err := s.repo.GetPackageByTrackingNumber(ctx, tn)
		return pkg, notRegistered(tn, err)
	}
	gens, err := s.repo.ListPackageGenerations(ctx, tn)
	if err != nil {
		return nil, err
	}
	for i := range gens {
		if gens[i].ExternalRef == extRef {
			return &gens[i], nil
		}
	}
	return nil, domain.Errorf(domain.ErrNotFound, domain.CodeNotRegistered,
		"The tracking number %s does not register with external_ref %s.", tn, extRef)
}

// GetPackageEvents returns one generation with its events, read from cold storage
// (without restoring) when archived.
func (s *Service) GetPackageEvents(ctx context.Context, ref domain.PackageRef) (domain.PackageEventsView, error) {
	pkg, err := s.lookup(ctx, ref)
	if err != nil {
		return domain.PackageEventsView{}, err
	}
	return s.view(ctx, *pkg)
}

func (s *Service) view(ctx context.Context, pkg domain.Package) (domain.PackageEventsView, error) {
	if !pkg.IsArchived {
		events, err := s.repo.ListEventsByPackage(ctx, pkg.PackageID)
		if err != nil {
			return domain.PackageEventsView{}, err
		}
		return domain.PackageEventsView{Package: pkg, Events: events}, nil
	}
	if s.cold == nil || pkg.ArchiveKey == "" {
		return domain.PackageEventsView{}, domain.Errorf(domain.ErrNotConfigured, domain.CodeArchiveUnavailable,
			"package %d is archived but cold storage is not configured", pkg.PackageID)
	}
	snap, err := s.cold.Get(ctx, pkg.ArchiveKey)
	if err != nil {
		return domain.PackageEventsView{}, domain.Errorf(domain.ErrNotConfigured, domain.CodeArchiveUnavailable,
			"read archive %s: %v", pkg.ArchiveKey, err)
	}
	return domain.PackageEventsView{Package: pkg, Events: snap.Events, Cold: true}, nil
}

// History lists every generation of a (possibly carrier-recycled) tracking number,
// newest first. withEvents also loads each generation's hot or archived events.
func (s *Service) History(ctx context.Context, number string, withEvents bool) (domain.PackageHistory, error) {
	number = strings.TrimSpace(number)
	if number == "" {
		return domain.PackageHistory{}, domain.ErrRequired("number")
	}
	pkgs, err := s.repo.ListPackageGenerations(ctx, number)
	if err != nil {
		return domain.PackageHistory{}, err
	}
	if len(pkgs) == 0 {
		return domain.PackageHistory{}, domain.ErrNotRegistered(number)
	}
	out := domain.PackageHistory{Number: number, Generations: make([]domain.PackageEventsView, len(pkgs))}
	for i, pkg := range pkgs {
		if !withEvents {
			out.Generations[i] = domain.PackageEventsView{Package: pkg, Cold: pkg.IsArchived}
			continue
		}
		v, err := s.view(ctx, pkg)
		if err != nil {
			return domain.PackageHistory{}, err
		}
		out.Generations[i] = v
	}
	return out, nil
}

// GetRoute returns the GeoJSON route of one generation from stored event coordinates
// (oldest → newest); events without coordinates are skipped.
func (s *Service) GetRoute(ctx context.Context, ref domain.PackageRef) (domain.GeoJSONFeatureCollection, error) {
	v, err := s.GetPackageEvents(ctx, ref)
	if err != nil {
		return domain.GeoJSONFeatureCollection{}, err
	}
	return buildRouteGeoJSON(v.Package, v.Events), nil
}

func buildRouteGeoJSON(pkg domain.Package, events []domain.TrackingEvent) domain.GeoJSONFeatureCollection {
	coords := make([][]float64, 0)
	for _, ev := range events {
		if ev.Latitude == nil || ev.Longitude == nil {
			continue
		}
		coords = append(coords, []float64{*ev.Longitude, *ev.Latitude})
	}
	fc := domain.GeoJSONFeatureCollection{Type: "FeatureCollection", Features: []domain.GeoJSONFeature{}}
	if len(coords) == 0 {
		return fc
	}
	if len(coords) == 1 {
		coords = append(coords, coords[0])
	}
	fc.Features = append(fc.Features, domain.GeoJSONFeature{
		Type:     "Feature",
		Geometry: domain.GeoJSONGeometry{Type: "LineString", Coordinates: coords},
		Properties: map[string]any{
			"number":     pkg.TrackingNumber,
			"carrier":    pkg.Carrier,
			"package_id": pkg.PackageID,
		},
	})
	return fc
}
