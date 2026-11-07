package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/SolaTyolo/tracking-service/internal/domain"
	"github.com/SolaTyolo/tracking-service/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Store is a PostgreSQL implementation of store.Store.
type Store struct {
	pool *pgxpool.Pool
	db   querier // pool, or the transaction inside WithTx
	inTx bool
}

// Open connects and returns a Store. Pool size and timeouts can be tuned via
// DSN parameters such as pool_max_conns.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool, db: pool}, nil
}

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Migrate applies pending migrations from migrations/. A Postgres session lock
// keeps replicas starting at the same time from migrating concurrently.
func (s *Store) Migrate(ctx context.Context) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	results, err := p.Up(ctx)
	if err != nil {
		return err
	}
	for _, r := range results {
		slog.InfoContext(ctx, "migration applied", "version", r.Source.Version, "file", r.Source.Path)
	}
	return nil
}

func (s *Store) WithTx(ctx context.Context, fn func(store.Store) error) error {
	if s.inTx {
		return fn(s)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&Store{pool: s.pool, db: tx, inTx: true})
	})
}

// packageCols are the writable package columns; order must match packageArgs and scanPackage.
var packageCols = []string{
	"carrier_code", "provider_name", "external_ref", "label_url",
	"weight", "dim_length", "dim_width", "dim_height",
	"status", "sub_status", "sub_status_desc",
	"origin_country", "origin_state", "origin_city", "origin_postal_code",
	"destination_country", "destination_state", "destination_city", "destination_postal_code",
	"service_type", "pieces", "reference_number", "last_mile_tracking_number", "last_mile_carrier_code",
	"estimated_delivery_from", "estimated_delivery_to",
	"milestones", "picked_up_at", "delivered_at",
	"latest_event_time", "latest_event_desc", "latest_event_location",
	"is_archived", "archive_key", "latest_update_at",
}

func packageArgs(pkg *domain.Package) ([]any, error) {
	milestones, err := json.Marshal(pkg.Milestones)
	if err != nil {
		return nil, fmt.Errorf("milestones: %w", err)
	}
	if pkg.Milestones == nil {
		milestones = []byte("{}")
	}
	d := pkg.ShippingDetail
	return []any{
		pkg.CarrierCode, pkg.ProviderName, pkg.ExternalRef, pkg.LabelURL,
		pkg.Weight, pkg.DimLength, pkg.DimWidth, pkg.DimHeight,
		pkg.Status, pkg.SubStatus, pkg.SubStatusDesc,
		d.Origin.Country, d.Origin.State, d.Origin.City, d.Origin.PostalCode,
		d.Destination.Country, d.Destination.State, d.Destination.City, d.Destination.PostalCode,
		d.ServiceType, d.Pieces, d.ReferenceNumber, d.LastMileTrackingNumber, d.LastMileCarrierCode,
		d.EstimatedDeliveryFrom, d.EstimatedDeliveryTo,
		milestones, pkg.PickedUpAt, pkg.DeliveredAt,
		pkg.LatestEventTime, pkg.LatestEventDesc, pkg.LatestEventLocation,
		pkg.IsArchived, pkg.ArchiveKey, pkg.LatestUpdateAt,
	}, nil
}

func placeholders(from, n int) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = fmt.Sprintf("$%d", from+i)
	}
	return strings.Join(ps, ",")
}

func (s *Store) CreatePackage(ctx context.Context, pkg *domain.Package) error {
	if pkg.Status == "" {
		pkg.Status = domain.StatusRegistered
	}
	args, err := packageArgs(pkg)
	if err != nil {
		return err
	}
	if pkg.TenantID == "" {
		pkg.TenantID = domain.DefaultTenant
	}
	row := s.db.QueryRow(ctx,
		"INSERT INTO package (tenant_id, tracking_number,"+strings.Join(packageCols, ",")+
			") VALUES ($1,$2,"+placeholders(3, len(packageCols))+
			") RETURNING package_id, created_at, updated_at",
		append([]any{pkg.TenantID, pkg.TrackingNumber}, args...)...,
	)
	if err := row.Scan(&pkg.PackageID, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *Store) GetPackageByTrackingNumber(ctx context.Context, tenantID, trackingNumber string) (*domain.Package, error) {
	return scanPackage(s.db.QueryRow(ctx,
		packageSelect+" WHERE tenant_id=$1 AND tracking_number=$2", tenantID, trackingNumber))
}

func (s *Store) GetPackageForUpdate(ctx context.Context, tenantID, trackingNumber string) (*domain.Package, error) {
	return scanPackage(s.db.QueryRow(ctx,
		packageSelect+" WHERE tenant_id=$1 AND tracking_number=$2 FOR UPDATE", tenantID, trackingNumber))
}

var packageSelect = "SELECT package_id, tenant_id, tracking_number, created_at, updated_at," +
	strings.Join(packageCols, ",") + " FROM package"

func (s *Store) ListPackages(ctx context.Context, f store.PackageFilter) ([]domain.Package, error) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	add("tenant_id = $%d", f.TenantID)
	if len(f.Statuses) > 0 {
		add("status = ANY($%d)", f.Statuses)
	}
	for _, c := range []struct{ col, v string }{
		{"carrier_code", f.CarrierCode},
		{"provider_name", f.ProviderName},
		{"external_ref", f.ExternalRef},
		{"origin_country", f.OriginCountry},
		{"destination_country", f.DestinationCountry},
	} {
		if c.v != "" {
			add(c.col+" = $%d", c.v)
		}
	}
	if f.IsArchived != nil {
		add("is_archived = $%d", *f.IsArchived)
	}
	if f.CreatedFrom != nil {
		add("created_at >= $%d", *f.CreatedFrom)
	}
	if f.CreatedTo != nil {
		add("created_at < $%d", *f.CreatedTo)
	}
	if f.BeforeID > 0 {
		add("package_id < $%d", f.BeforeID)
	}
	args = append(args, f.Limit)
	rows, err := s.db.Query(ctx, fmt.Sprintf("%s WHERE %s ORDER BY package_id DESC LIMIT $%d",
		packageSelect, strings.Join(conds, " AND "), len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Package
	for rows.Next() {
		pkg, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pkg)
	}
	return out, rows.Err()
}

func scanPackage(row pgx.Row) (*domain.Package, error) {
	var pkg domain.Package
	var milestones []byte
	d := &pkg.ShippingDetail
	err := row.Scan(
		&pkg.PackageID, &pkg.TenantID, &pkg.TrackingNumber, &pkg.CreatedAt, &pkg.UpdatedAt,
		&pkg.CarrierCode, &pkg.ProviderName, &pkg.ExternalRef, &pkg.LabelURL,
		&pkg.Weight, &pkg.DimLength, &pkg.DimWidth, &pkg.DimHeight,
		&pkg.Status, &pkg.SubStatus, &pkg.SubStatusDesc,
		&d.Origin.Country, &d.Origin.State, &d.Origin.City, &d.Origin.PostalCode,
		&d.Destination.Country, &d.Destination.State, &d.Destination.City, &d.Destination.PostalCode,
		&d.ServiceType, &d.Pieces, &d.ReferenceNumber, &d.LastMileTrackingNumber, &d.LastMileCarrierCode,
		&d.EstimatedDeliveryFrom, &d.EstimatedDeliveryTo,
		&milestones, &pkg.PickedUpAt, &pkg.DeliveredAt,
		&pkg.LatestEventTime, &pkg.LatestEventDesc, &pkg.LatestEventLocation,
		&pkg.IsArchived, &pkg.ArchiveKey, &pkg.LatestUpdateAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(milestones) > 0 {
		if err := json.Unmarshal(milestones, &pkg.Milestones); err != nil {
			return nil, fmt.Errorf("milestones: %w", err)
		}
	}
	return &pkg, nil
}

func (s *Store) UpdatePackage(ctx context.Context, pkg *domain.Package) error {
	args, err := packageArgs(pkg)
	if err != nil {
		return err
	}
	ct, err := s.db.Exec(ctx,
		"UPDATE package SET ("+strings.Join(packageCols, ",")+", updated_at) = ("+
			placeholders(2, len(packageCols))+", now()) WHERE package_id=$1",
		append([]any{pkg.PackageID}, args...)...,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListPackagesDueArchive(ctx context.Context, deliveredBefore time.Time, limit int) ([]domain.Package, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, packageSelect+`
 WHERE delivered_at IS NOT NULL
   AND delivered_at < $1
   AND is_archived = false
 ORDER BY delivered_at ASC
 LIMIT $2`, deliveredBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Package
	for rows.Next() {
		pkg, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pkg)
	}
	return out, rows.Err()
}

const insertEventSQL = `
INSERT INTO tracking_event (
    package_id, tracking_number, event_time, carrier_code, stage, sub_status, status_code,
    description, location, country, state, city, postal_code, raw_json, event_fingerprint
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (package_id, event_fingerprint) DO NOTHING
RETURNING event_id, created_at`

func (s *Store) InsertEvents(ctx context.Context, events []domain.TrackingEvent) ([]domain.TrackingEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	batch := &pgx.Batch{}
	for _, ev := range events {
		var raw any
		if len(ev.RawJSON) > 0 {
			raw = ev.RawJSON
		}
		batch.Queue(insertEventSQL,
			ev.PackageID, ev.TrackingNumber, ev.EventTime, ev.CarrierCode, ev.Stage, ev.SubStatus, ev.StatusCode,
			ev.Description, ev.Location, ev.Address.Country, ev.Address.State, ev.Address.City, ev.Address.PostalCode,
			raw, ev.EventFingerprint,
		)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	var inserted []domain.TrackingEvent
	for _, ev := range events {
		err := br.QueryRow().Scan(&ev.EventID, &ev.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // duplicate fingerprint
		}
		if err != nil {
			return nil, err
		}
		inserted = append(inserted, ev)
	}
	return inserted, nil
}

func (s *Store) ListEventsByPackage(ctx context.Context, packageID int64) ([]domain.TrackingEvent, error) {
	rows, err := s.db.Query(ctx, `
SELECT event_id, package_id, tracking_number, event_time, carrier_code, stage, sub_status, status_code,
       description, location, country, state, city, postal_code, raw_json, event_fingerprint, created_at
FROM tracking_event
WHERE package_id=$1
ORDER BY event_time ASC, event_id ASC`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TrackingEvent
	for rows.Next() {
		var ev domain.TrackingEvent
		var raw []byte
		if err := rows.Scan(
			&ev.EventID, &ev.PackageID, &ev.TrackingNumber, &ev.EventTime,
			&ev.CarrierCode, &ev.Stage, &ev.SubStatus, &ev.StatusCode,
			&ev.Description, &ev.Location,
			&ev.Address.Country, &ev.Address.State, &ev.Address.City, &ev.Address.PostalCode,
			&raw, &ev.EventFingerprint, &ev.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			ev.RawJSON = json.RawMessage(raw)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) DeleteEventsByPackage(ctx context.Context, packageID int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM tracking_event WHERE package_id=$1`, packageID)
	return err
}

// TryLock uses a session-level advisory lock held on a dedicated pool connection.
func (s *Store) TryLock(ctx context.Context, name string) (func(), bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, name); err != nil {
			// Closing the session is the only other way to drop the lock.
			_ = conn.Conn().Close(ctx)
		}
		conn.Release()
	}
	return release, true, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
