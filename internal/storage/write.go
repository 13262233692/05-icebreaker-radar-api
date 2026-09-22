package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

const (
	frameParamsPerRow = 13
	ridgeParamsPerRow = 15
	maxRowsPerStmt    = 500
)

// BatchInsert atomically upserts frame statistics and their ridges.
func (p *Postgres) BatchInsert(ctx context.Context, frames []model.AnalyzedFrame) error {
	if len(frames) == 0 {
		return nil
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for start := 0; start < len(frames); start += maxRowsPerStmt {
		end := start + maxRowsPerStmt
		if end > len(frames) {
			end = len(frames)
		}
		if err := insertFrames(ctx, tx, frames[start:end]); err != nil {
			return err
		}
	}

	frameTimes := make(map[uint32]model.EchoFrame, len(frames))
	var ridges []model.Ridge
	for _, af := range frames {
		frameTimes[af.Frame.FrameID] = af.Frame
		ridges = append(ridges, af.Ridges...)
	}
	for start := 0; start < len(ridges); start += maxRowsPerStmt {
		end := start + maxRowsPerStmt
		if end > len(ridges) {
			end = len(ridges)
		}
		if err := insertRidges(ctx, tx, frameTimes, ridges[start:end]); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage: commit: %w", err)
	}
	return nil
}

func insertFrames(ctx context.Context, tx pgx.Tx, frames []model.AnalyzedFrame) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO ice_frames (
		frame_id, sweep_id, observed_at, vessel_lat, vessel_lon,
		az_start_deg, az_end_deg, range_bins, bin_spacing_m,
		mean_intensity, max_intensity, ice_concentration, ridge_count
	) VALUES `)

	args := make([]any, 0, len(frames)*frameParamsPerRow)
	for i, af := range frames {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * frameParamsPerRow
		sb.WriteString(fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5,
			base+6, base+7, base+8, base+9,
			base+10, base+11, base+12, base+13,
		))
		f := af.Frame
		args = append(args,
			int64(f.FrameID), int64(f.SweepID), f.Timestamp,
			f.VesselLat, f.VesselLon, f.AzStart, f.AzEnd,
			af.Stats.TotalBins, f.BinSpacing,
			af.Stats.MeanIntensity, int16(af.Stats.MaxIntensity),
			af.Stats.IceConcentration, af.Stats.RidgeCount,
		)
	}
	sb.WriteString(` ON CONFLICT (frame_id) DO UPDATE SET
		mean_intensity = EXCLUDED.mean_intensity,
		max_intensity = EXCLUDED.max_intensity,
		ice_concentration = EXCLUDED.ice_concentration,
		ridge_count = EXCLUDED.ridge_count`)

	if _, err := tx.Exec(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("storage: insert frames: %w", err)
	}
	return nil
}

func insertRidges(ctx context.Context, tx pgx.Tx, frameTimes map[uint32]model.EchoFrame, ridges []model.Ridge) error {
	if len(ridges) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString(`INSERT INTO ice_ridges (
		frame_id, sweep_id, ridge_idx, observed_at, azimuth_deg,
		start_bin, end_bin, peak_bin, range_start_m, range_end_m, range_peak_m,
		peak_intensity, mean_intensity, lat, lon
	) VALUES `)

	args := make([]any, 0, len(ridges)*ridgeParamsPerRow)
	for i, r := range ridges {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * ridgeParamsPerRow
		sb.WriteString(fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5,
			base+6, base+7, base+8, base+9, base+10,
			base+11, base+12, base+13, base+14, base+15,
		))
		f := frameTimes[r.FrameID]
		args = append(args,
			int64(r.FrameID), int64(r.SweepID), r.RidgeIdx, f.Timestamp,
			r.AzimuthDeg, r.StartBin, r.EndBin, r.PeakBin,
			r.RangeStartM, r.RangeEndM, r.RangePeakM,
			int16(r.PeakIntensity), r.MeanIntensity, r.Lat, r.Lon,
		)
	}
	sb.WriteString(` ON CONFLICT (frame_id, ridge_idx) DO NOTHING`)

	if _, err := tx.Exec(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("storage: insert ridges: %w", err)
	}
	return nil
}
