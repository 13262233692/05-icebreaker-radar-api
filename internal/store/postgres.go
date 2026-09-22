// Package store 负责冰情统计的 PostgreSQL 持久化与时空范围查询。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"icebreaker-radar/internal/ice_analyzer"
)

// Store 封装 PostgreSQL 访问。
type Store struct {
	db *sql.DB
}

// Schema 建表语句（幂等）。
const Schema = `
CREATE TABLE IF NOT EXISTS ice_stats (
    id              BIGSERIAL PRIMARY KEY,
    observed_at     TIMESTAMPTZ      NOT NULL,
    latitude        DOUBLE PRECISION NOT NULL,
    longitude       DOUBLE PRECISION NOT NULL,
    concentration   DOUBLE PRECISION NOT NULL,
    mean_intensity  DOUBLE PRECISION NOT NULL,
    max_intensity   INTEGER          NOT NULL,
    sample_count    INTEGER          NOT NULL,
    ridge_count     INTEGER          NOT NULL,
    ridges          JSONB            NOT NULL DEFAULT '[]',
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ice_stats_observed_at ON ice_stats (observed_at);
CREATE INDEX IF NOT EXISTS idx_ice_stats_lat_lon ON ice_stats (latitude, longitude);
`

func New(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate 创建所需表结构。
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, Schema)
	return err
}

// Insert 持久化一条冰情统计。
func (s *Store) Insert(ctx context.Context, res *ice_analyzer.Result) error {
	ridges, err := json.Marshal(res.Ridges)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ice_stats
		    (observed_at, latitude, longitude, concentration, mean_intensity,
		     max_intensity, sample_count, ridge_count, ridges)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		res.Timestamp, res.Latitude, res.Longitude, res.Concentration, res.MeanIntensity,
		res.MaxIntensity, res.SampleCount, len(res.Ridges), ridges)
	return err
}

// QueryFilter 描述时空范围查询条件。
type QueryFilter struct {
	Start  time.Time
	End    time.Time
	MinLat float64
	MaxLat float64
	MinLon float64
	MaxLon float64
	Limit  int
}

// Query 按时间范围 + 经纬度矩形区域查询冰情统计。
func (s *Store) Query(ctx context.Context, f QueryFilter) ([]ice_analyzer.Result, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT observed_at, latitude, longitude, concentration, mean_intensity,
		       max_intensity, sample_count, ridges
		FROM ice_stats
		WHERE observed_at BETWEEN $1 AND $2
		  AND latitude  BETWEEN $3 AND $4
		  AND longitude BETWEEN $5 AND $6
		ORDER BY observed_at ASC
		LIMIT $7`,
		f.Start, f.End, f.MinLat, f.MaxLat, f.MinLon, f.MaxLon, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []ice_analyzer.Result{}
	for rows.Next() {
		var r ice_analyzer.Result
		var ridges []byte
		if err := rows.Scan(&r.Timestamp, &r.Latitude, &r.Longitude, &r.Concentration,
			&r.MeanIntensity, &r.MaxIntensity, &r.SampleCount, &ridges); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ridges, &r.Ridges); err != nil {
			r.Ridges = []ice_analyzer.Ridge{}
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
