package storage

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/polar/icebreakerradar/internal/model"
)

// PostgresStore durably persists per-slice ice statistics and ridge
// detections, and serves time-range / bbox REST queries.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects (lazy pool creation) and applies the schema.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s := &PostgresStore{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS ice_observations (
	id                BIGINT PRIMARY KEY,
	ts                TIMESTAMPTZ NOT NULL,
	ship_lat          DOUBLE PRECISION NOT NULL,
	ship_lon          DOUBLE PRECISION NOT NULL,
	bearing_deg       DOUBLE PRECISION NOT NULL,
	range_bins        INTEGER NOT NULL,
	max_range_m       DOUBLE PRECISION NOT NULL,
	concentration     REAL NOT NULL,
	mean_intensity    REAL NOT NULL,
	mean_ice_intensity REAL NOT NULL,
	max_intensity     INTEGER NOT NULL,
	ice_bins          INTEGER NOT NULL,
	min_lat           DOUBLE PRECISION NOT NULL,
	max_lat           DOUBLE PRECISION NOT NULL,
	min_lon           DOUBLE PRECISION NOT NULL,
	max_lon           DOUBLE PRECISION NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ice_obs_ts
	ON ice_observations (ts DESC);
CREATE INDEX IF NOT EXISTS idx_ice_obs_box
	ON ice_observations (min_lat, max_lat, min_lon, max_lon);

CREATE TABLE IF NOT EXISTS ice_ridges (
	id             BIGSERIAL PRIMARY KEY,
	sweep_id       BIGINT NOT NULL REFERENCES ice_observations(id) ON DELETE CASCADE,
	ts             TIMESTAMPTZ NOT NULL,
	start_bin      INTEGER NOT NULL,
	end_bin        INTEGER NOT NULL,
	peak_bin       INTEGER NOT NULL,
	start_range_m  DOUBLE PRECISION NOT NULL,
	end_range_m    DOUBLE PRECISION NOT NULL,
	width_m        DOUBLE PRECISION NOT NULL,
	peak_intensity INTEGER NOT NULL,
	mean_intensity INTEGER NOT NULL,
	lat            DOUBLE PRECISION NOT NULL,
	lon            DOUBLE PRECISION NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ridges_ts ON ice_ridges (ts DESC);
CREATE INDEX IF NOT EXISTS idx_ridges_geo ON ice_ridges (lat, lon);
`)
	return err
}

// InsertEnvelope upserts one observation and replaces any ridge rows that
// previously belonged to the same sweep (idempotent replay protection).
func (s *PostgresStore) InsertEnvelope(ctx context.Context, env model.Envelope) error {
	batch := &pgx.Batch{}
	a := env.Analysis
	batch.Queue(`
INSERT INTO ice_observations
	(id, ts, ship_lat, ship_lon, bearing_deg, range_bins, max_range_m,
	 concentration, mean_intensity, mean_ice_intensity, max_intensity, ice_bins,
	 min_lat, max_lat, min_lon, max_lon)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (id) DO UPDATE SET
	ts=EXCLUDED.ts, ship_lat=EXCLUDED.ship_lat, ship_lon=EXCLUDED.ship_lon,
	bearing_deg=EXCLUDED.bearing_deg, range_bins=EXCLUDED.range_bins,
	max_range_m=EXCLUDED.max_range_m, concentration=EXCLUDED.concentration,
	mean_intensity=EXCLUDED.mean_intensity,
	mean_ice_intensity=EXCLUDED.mean_ice_intensity,
	max_intensity=EXCLUDED.max_intensity, ice_bins=EXCLUDED.ice_bins,
	min_lat=EXCLUDED.min_lat, max_lat=EXCLUDED.max_lat,
	min_lon=EXCLUDED.min_lon, max_lon=EXCLUDED.max_lon`,
		a.SweepID, a.Time, a.ShipLat, a.ShipLon, a.Bearing, a.RangeBins,
		a.MaxRangeM, a.Concentration, a.MeanIntensity, a.MeanIceIntensity,
		a.MaxIntensity, a.IceBins, a.MinLat, a.MaxLat, a.MinLon, a.MaxLon)
	batch.Queue(`DELETE FROM ice_ridges WHERE sweep_id = $1`, a.SweepID)
	for _, r := range a.Ridges {
		batch.Queue(`
INSERT INTO ice_ridges
	(sweep_id, ts, start_bin, end_bin, peak_bin, start_range_m, end_range_m,
	 width_m, peak_intensity, mean_intensity, lat, lon)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			r.SweepID, r.Time, r.StartBin, r.EndBin, r.PeakBin, r.StartRangeM,
			r.EndRangeM, r.WidthM, r.PeakIntensity, r.MeanIntensity, r.Lat, r.Lon)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// QueryObservations returns persisted per-slice statistics newest-first.
func (s *PostgresStore) QueryObservations(ctx context.Context, f model.IceFilter) ([]model.SweepAnalysis, error) {
	q := `SELECT id, ts, ship_lat, ship_lon, bearing_deg, range_bins, max_range_m,
	             concentration, mean_intensity, mean_ice_intensity, max_intensity,
	             ice_bins, min_lat, max_lat, min_lon, max_lon
	      FROM ice_observations WHERE TRUE`
	args := pgx.NamedArgs{}
	q = applyObsFilter(q, args, f)
	q += " ORDER BY ts DESC LIMIT @limit"
	args["limit"] = f.Limit

	rows, err := s.pool.Query(ctx, q, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.SweepAnalysis
	for rows.Next() {
		var a model.SweepAnalysis
		if err := rows.Scan(&a.SweepID, &a.Time, &a.ShipLat, &a.ShipLon, &a.Bearing,
			&a.RangeBins, &a.MaxRangeM, &a.Concentration, &a.MeanIntensity,
			&a.MeanIceIntensity, &a.MaxIntensity, &a.IceBins,
			&a.MinLat, &a.MaxLat, &a.MinLon, &a.MaxLon); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// QueryRidges returns persisted ridge detections newest-first.
func (s *PostgresStore) QueryRidges(ctx context.Context, f model.IceFilter) ([]model.Ridge, error) {
	q := `SELECT sweep_id, ts, start_bin, end_bin, peak_bin, start_range_m,
	             end_range_m, width_m, peak_intensity, mean_intensity, lat, lon
	      FROM ice_ridges WHERE TRUE`
	args := pgx.NamedArgs{}
	q = applyRidgeFilter(q, args, f)
	q += " ORDER BY ts DESC LIMIT @limit"
	args["limit"] = f.Limit

	rows, err := s.pool.Query(ctx, q, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Ridge
	for rows.Next() {
		var r model.Ridge
		if err := rows.Scan(&r.SweepID, &r.Time, &r.StartBin, &r.EndBin,
			&r.PeakBin, &r.StartRangeM, &r.EndRangeM, &r.WidthM,
			&r.PeakIntensity, &r.MeanIntensity, &r.Lat, &r.Lon); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestObservation returns the most recent observation or zero time if empty.
func (s *PostgresStore) LatestObservation(ctx context.Context) (model.SweepAnalysis, error) {
	row := s.pool.QueryRow(ctx, `
SELECT id, ts, ship_lat, ship_lon, bearing_deg, range_bins, max_range_m,
       concentration, mean_intensity, mean_ice_intensity, max_intensity,
       ice_bins, min_lat, max_lat, min_lon, max_lon
FROM ice_observations ORDER BY ts DESC LIMIT 1`)
	var a model.SweepAnalysis
	err := row.Scan(&a.SweepID, &a.Time, &a.ShipLat, &a.ShipLon, &a.Bearing,
		&a.RangeBins, &a.MaxRangeM, &a.Concentration, &a.MeanIntensity,
		&a.MeanIceIntensity, &a.MaxIntensity, &a.IceBins,
		&a.MinLat, &a.MaxLat, &a.MinLon, &a.MaxLon)
	if err == pgx.ErrNoRows {
		return a, nil
	}
	return a, err
}

// Close shuts down the pool, waiting for in-flight queries.
func (s *PostgresStore) Close() { s.pool.Close() }

// Ping verifies database reachability for health checks.
func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
