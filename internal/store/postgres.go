package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BBox is a lat/lon rectangle.
type BBox struct {
	MinLat, MaxLat, MinLon, MaxLon float64
}

// TimeRange bounds a query by inclusive start/exclusive end.
type TimeRange struct {
	From, To time.Time
}

// SweepRow is one persisted ice-statistics record.
type SweepRow struct {
	ID            int64     `json:"id"`
	ObservedAt    time.Time `json:"observed_at"`
	ShipLat       float64   `json:"ship_lat"`
	ShipLon       float64   `json:"ship_lon"`
	RangeM        float32   `json:"range_m"`
	Concentration float64   `json:"concentration"`
	IceBins       int       `json:"ice_bins"`
	TotalBins     int       `json:"total_bins"`
	MeanIntensity float64   `json:"mean_intensity"`
	MaxIntensity  int       `json:"max_intensity"`
	MinLat        *float64  `json:"min_lat"`
	MaxLat        *float64  `json:"max_lat"`
	MinLon        *float64  `json:"min_lon"`
	MaxLon        *float64  `json:"max_lon"`
	RidgeCount    int       `json:"ridge_count"`
}

// RidgeRow is one persisted pressure-ridge detection.
type RidgeRow struct {
	ID           int64     `json:"id"`
	SweepID      int64     `json:"sweep_id"`
	ObservedAt   time.Time `json:"observed_at"`
	Lat          float64   `json:"lat"`
	Lon          float64   `json:"lon"`
	BearingRad   float64   `json:"bearing_rad"`
	LengthM      float64   `json:"length_m"`
	Aspect       float64   `json:"aspect"`
	BinCount     int       `json:"bin_count"`
	MaxIntensity int       `json:"max_intensity"`
}

// ConcentrationPoint is one sample in an aggregate time series.
type ConcentrationPoint struct {
	ObservedAt    time.Time `json:"observed_at"`
	Concentration float64   `json:"concentration"`
	Samples       int       `json:"samples"`
}

// SweepInput carries one analyzed sweep into persistence.
type SweepInput struct {
	ObservedAt    time.Time
	ShipLat       float64
	ShipLon       float64
	RangeM        float32
	Concentration float64
	IceBins       int
	TotalBins     int
	MeanIntensity float64
	MaxIntensity  int
	MinLat        *float64
	MaxLat        *float64
	MinLon        *float64
	MaxLon        *float64
	Ridges        []RidgeInput
}

// RidgeInput is a ridge belonging to a SweepInput.
type RidgeInput struct {
	Lat          float64
	Lon          float64
	BearingRad   float64
	LengthM      float64
	Aspect       float64
	BinCount     int
	MaxIntensity int
}

// Postgres is the durable ice-statistics store.
type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	pg := &Postgres{pool: pool}
	if err := pg.schema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pg, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Schema is exported so tooling/migrations can inspect the DDL.
const Schema = `
CREATE TABLE IF NOT EXISTS ice_sweeps (
    id             BIGSERIAL PRIMARY KEY,
    observed_at    TIMESTAMPTZ NOT NULL,
    ship_lat       DOUBLE PRECISION NOT NULL,
    ship_lon       DOUBLE PRECISION NOT NULL,
    range_m        REAL NOT NULL,
    concentration  REAL NOT NULL,
    ice_bins       INTEGER NOT NULL,
    total_bins     INTEGER NOT NULL,
    mean_intensity REAL NOT NULL,
    max_intensity  INTEGER NOT NULL,
    min_lat        DOUBLE PRECISION,
    max_lat        DOUBLE PRECISION,
    min_lon        DOUBLE PRECISION,
    max_lon        DOUBLE PRECISION,
    ridge_count    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_ice_sweeps_time
    ON ice_sweeps (observed_at);
CREATE INDEX IF NOT EXISTS idx_ice_sweeps_bbox
    ON ice_sweeps (ship_lat, ship_lon);

CREATE TABLE IF NOT EXISTS ice_ridges (
    id            BIGSERIAL PRIMARY KEY,
    sweep_id      BIGINT NOT NULL REFERENCES ice_sweeps(id) ON DELETE CASCADE,
    observed_at   TIMESTAMPTZ NOT NULL,
    lat           DOUBLE PRECISION NOT NULL,
    lon           DOUBLE PRECISION NOT NULL,
    bearing_rad   DOUBLE PRECISION NOT NULL,
    length_m      DOUBLE PRECISION NOT NULL,
    aspect        DOUBLE PRECISION NOT NULL,
    bin_count     INTEGER NOT NULL,
    max_intensity INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ice_ridges_time ON ice_ridges (observed_at);
CREATE INDEX IF NOT EXISTS idx_ice_ridges_geo ON ice_ridges (lat, lon);
`

func (p *Postgres) schema(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, Schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

// InsertSweep persists one sweep and its ridges atomically.
func (p *Postgres) InsertSweep(ctx context.Context, in SweepInput) (int64, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO ice_sweeps (
			observed_at, ship_lat, ship_lon, range_m, concentration,
			ice_bins, total_bins, mean_intensity, max_intensity,
			min_lat, max_lat, min_lon, max_lon, ridge_count
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id`,
		in.ObservedAt, in.ShipLat, in.ShipLon, in.RangeM, in.Concentration,
		in.IceBins, in.TotalBins, in.MeanIntensity, in.MaxIntensity,
		in.MinLat, in.MaxLat, in.MinLon, in.MaxLon, len(in.Ridges),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert sweep: %w", err)
	}

	for _, r := range in.Ridges {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ice_ridges (
				sweep_id, observed_at, lat, lon, bearing_rad,
				length_m, aspect, bin_count, max_intensity
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, in.ObservedAt, r.Lat, r.Lon, r.BearingRad,
			r.LengthM, r.Aspect, r.BinCount, r.MaxIntensity,
		); err != nil {
			return 0, fmt.Errorf("insert ridge: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// QuerySweeps returns ice statistics inside an optional time range and whose
// ice footprint overlaps the optional bounding box.
func (p *Postgres) QuerySweeps(ctx context.Context, tr TimeRange, bbox *BBox, limit int) ([]SweepRow, error) {
	q := `
		SELECT id, observed_at, ship_lat, ship_lon, range_m, concentration,
		       ice_bins, total_bins, mean_intensity, max_intensity,
		       min_lat, max_lat, min_lon, max_lon, ridge_count
		FROM ice_sweeps
		WHERE observed_at >= $1 AND observed_at < $2`
	args := []any{tr.From, tr.To}
	if bbox != nil {
		q += `
		  AND min_lat <= $3 AND max_lat >= $4
		  AND min_lon <= $5 AND max_lon >= $6`
		args = append(args, bbox.MaxLat, bbox.MinLat, bbox.MaxLon, bbox.MinLon)
	}
	q += ` ORDER BY observed_at DESC LIMIT ` + fmt.Sprintf("%d", clampLimit(limit))

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SweepRow
	for rows.Next() {
		var r SweepRow
		if err := rows.Scan(
			&r.ID, &r.ObservedAt, &r.ShipLat, &r.ShipLon, &r.RangeM,
			&r.Concentration, &r.IceBins, &r.TotalBins, &r.MeanIntensity,
			&r.MaxIntensity, &r.MinLat, &r.MaxLat, &r.MinLon, &r.MaxLon,
			&r.RidgeCount,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryRidges returns detected ridges by time range and point-in-bbox filter.
func (p *Postgres) QueryRidges(ctx context.Context, tr TimeRange, bbox *BBox, limit int) ([]RidgeRow, error) {
	q := `
		SELECT r.id, r.sweep_id, r.observed_at, r.lat, r.lon,
		       r.bearing_rad, r.length_m, r.aspect, r.bin_count, r.max_intensity
		FROM ice_ridges r
		WHERE r.observed_at >= $1 AND r.observed_at < $2`
	args := []any{tr.From, tr.To}
	if bbox != nil {
		q += ` AND r.lat BETWEEN $3 AND $4 AND r.lon BETWEEN $5 AND $6`
		args = append(args, bbox.MinLat, bbox.MaxLat, bbox.MinLon, bbox.MaxLon)
	}
	q += ` ORDER BY r.observed_at DESC LIMIT ` + fmt.Sprintf("%d", clampLimit(limit))

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RidgeRow
	for rows.Next() {
		var r RidgeRow
		if err := rows.Scan(
			&r.ID, &r.SweepID, &r.ObservedAt, &r.Lat, &r.Lon,
			&r.BearingRad, &r.LengthM, &r.Aspect, &r.BinCount, &r.MaxIntensity,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ConcentrationSeries averages concentration into fixed-width time buckets.
func (p *Postgres) ConcentrationSeries(ctx context.Context, tr TimeRange, bbox *BBox, bucket time.Duration) ([]ConcentrationPoint, error) {
	q := `
		SELECT to_timestamp(floor(extract(epoch FROM observed_at) / $3) * $3)
		         AT TIME ZONE 'UTC' AS bucket,
		       AVG(concentration)::float8 AS conc,
		       COUNT(*) AS n
		FROM ice_sweeps
		WHERE observed_at >= $1 AND observed_at < $2`
	args := []any{tr.From, tr.To, bucket.Seconds()}
	if bbox != nil {
		q += `
		  AND min_lat <= $4 AND max_lat >= $5
		  AND min_lon <= $6 AND max_lon >= $7`
		args = append(args, bbox.MaxLat, bbox.MinLat, bbox.MaxLon, bbox.MinLon)
	}
	q += ` GROUP BY bucket ORDER BY bucket`

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConcentrationPoint
	for rows.Next() {
		var pt ConcentrationPoint
		if err := rows.Scan(&pt.ObservedAt, &pt.Concentration, &pt.Samples); err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

// LatestSweeps returns the most recent persisted sweeps regardless of time.
func (p *Postgres) LatestSweeps(ctx context.Context, limit int) ([]SweepRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, observed_at, ship_lat, ship_lon, range_m, concentration,
		       ice_bins, total_bins, mean_intensity, max_intensity,
		       min_lat, max_lat, min_lon, max_lon, ridge_count
		FROM ice_sweeps ORDER BY observed_at DESC LIMIT $1`,
		clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (SweepRow, error) {
		var r SweepRow
		err := row.Scan(
			&r.ID, &r.ObservedAt, &r.ShipLat, &r.ShipLon, &r.RangeM,
			&r.Concentration, &r.IceBins, &r.TotalBins, &r.MeanIntensity,
			&r.MaxIntensity, &r.MinLat, &r.MaxLat, &r.MinLon, &r.MaxLon,
			&r.RidgeCount)
		return r, err
	})
}

func clampLimit(n int) int {
	if n <= 0 || n > 5000 {
		return 1000
	}
	return n
}
