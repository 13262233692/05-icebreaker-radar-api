package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

const defaultLimit = 1000

type clauses struct {
	where string
	args  []any
}

// buildClashes builds a parameterized WHERE for an observed_at time range and a
// lat/lon rectangle. geoCols selects the coordinate columns of the table.
func buildClauses(f model.QueryFilter, latCol, lonCol string) clauses {
	var conds []string
	var args []any
	addNum := func(cond string, val float64) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}

	if !f.Start.IsZero() {
		args = append(args, f.Start)
		conds = append(conds, fmt.Sprintf("observed_at >= $%d", len(args)))
	}
	if !f.End.IsZero() {
		args = append(args, f.End)
		conds = append(conds, fmt.Sprintf("observed_at <= $%d", len(args)))
	}
	if f.HasBBox {
		addNum(latCol+" >= $%d", f.MinLat)
		addNum(latCol+" <= $%d", f.MaxLat)
		addNum(lonCol+" >= $%d", f.MinLon)
		addNum(lonCol+" <= $%d", f.MaxLon)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	return clauses{where: where, args: args}
}

// QueryStats returns persisted per-sweep statistics matching the filter.
func (p *Postgres) QueryStats(ctx context.Context, f model.QueryFilter) ([]model.StatsRow, error) {
	c := buildClauses(f, "vessel_lat", "vessel_lon")
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	sql := `SELECT frame_id, sweep_id, observed_at, vessel_lat, vessel_lon,
		az_start_deg, az_end_deg, range_bins,
		mean_intensity, max_intensity, ice_concentration, ridge_count
		FROM ice_frames` + c.where + ` ORDER BY observed_at DESC LIMIT ` + itoa(limit)

	rows, err := p.pool.Query(ctx, sql, c.args...)
	if err != nil {
		return nil, fmt.Errorf("storage: query stats: %w", err)
	}
	defer rows.Close()

	out := make([]model.StatsRow, 0)
	for rows.Next() {
		var (
			r                     model.StatsRow
			frameID, sweepID      int64
			rangeBins, ridgeCount int64
			maxIntensity          int16
		)
		if err := rows.Scan(
			&frameID, &sweepID, &r.Timestamp, &r.VesselLat, &r.VesselLon,
			&r.AzStart, &r.AzEnd, &rangeBins,
			&r.MeanIntensity, &maxIntensity, &r.IceConcentration, &ridgeCount,
		); err != nil {
			return nil, fmt.Errorf("storage: scan stats: %w", err)
		}
		r.FrameID = uint32(frameID)
		r.SweepID = uint32(sweepID)
		r.RangeBins = int(rangeBins)
		r.MaxIntensity = uint8(maxIntensity)
		r.RidgeCount = int(ridgeCount)
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryRidges returns detected ridges matching the filter.
func (p *Postgres) QueryRidges(ctx context.Context, f model.QueryFilter) ([]model.RidgeRow, error) {
	c := buildClauses(f, "lat", "lon")
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	sql := `SELECT frame_id, sweep_id, ridge_idx, observed_at, azimuth_deg,
		range_start_m, range_end_m, range_peak_m,
		peak_intensity, mean_intensity, lat, lon
		FROM ice_ridges` + c.where + ` ORDER BY observed_at DESC LIMIT ` + itoa(limit)

	rows, err := p.pool.Query(ctx, sql, c.args...)
	if err != nil {
		return nil, fmt.Errorf("storage: query ridges: %w", err)
	}
	defer rows.Close()

	out := make([]model.RidgeRow, 0)
	for rows.Next() {
		var (
			r                     model.RidgeRow
			frameID, sweepID, idx int64
			peak                  int16
		)
		if err := rows.Scan(
			&frameID, &sweepID, &idx, &r.Timestamp, &r.AzimuthDeg,
			&r.RangeStartM, &r.RangeEndM, &r.RangePeakM,
			&peak, &r.MeanIntensity, &r.Lat, &r.Lon,
		); err != nil {
			return nil, fmt.Errorf("storage: scan ridges: %w", err)
		}
		r.FrameID = uint32(frameID)
		r.SweepID = uint32(sweepID)
		r.RidgeIdx = int(idx)
		r.PeakIntensity = uint8(peak)
		out = append(out, r)
	}
	return out, rows.Err()
}

// StatsSummary aggregates statistics over the filter window.
type StatsSummary struct {
	FrameCount       int     `json:"frame_count"`
	AvgConcentration float64 `json:"avg_ice_concentration"`
	MaxConcentration float64 `json:"max_ice_concentration"`
	AvgMeanIntensity float64 `json:"avg_mean_intensity"`
	TotalRidges      int     `json:"total_ridges"`
}

// Summarize computes aggregate ice conditions over the filter window.
func (p *Postgres) Summarize(ctx context.Context, f model.QueryFilter) (StatsSummary, error) {
	c := buildClauses(f, "vessel_lat", "vessel_lon")
	sql := `SELECT COUNT(*), COALESCE(AVG(ice_concentration),0),
		COALESCE(MAX(ice_concentration),0), COALESCE(AVG(mean_intensity),0),
		COALESCE(SUM(ridge_count),0)
		FROM ice_frames` + c.where

	var (
		s             StatsSummary
		count, ridges int64
	)
	if err := p.pool.QueryRow(ctx, sql, c.args...).Scan(
		&count, &s.AvgConcentration, &s.MaxConcentration,
		&s.AvgMeanIntensity, &ridges,
	); err != nil {
		return StatsSummary{}, fmt.Errorf("storage: summarize: %w", err)
	}
	s.FrameCount = int(count)
	s.TotalRidges = int(ridges)
	return s, nil
}
