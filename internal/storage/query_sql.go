package storage

import (
	"github.com/jackc/pgx/v5"

	"github.com/polar/icebreakerradar/internal/model"
)

func applyObsFilter(q string, args pgx.NamedArgs, f model.IceFilter) string {
	if !f.From.IsZero() {
		q += " AND ts >= @from"
		args["from"] = f.From
	}
	if !f.To.IsZero() {
		q += " AND ts <= @to"
		args["to"] = f.To
	}
	if f.HasBBox {
		// Slice coverage boxes are line segments along one ray; overlap with
		// the query rectangle requires interval intersection on both axes.
		q += ` AND min_lat <= @max_lat AND max_lat >= @min_lat
		       AND min_lon <= @max_lon AND max_lon >= @min_lon`
		args["min_lat"] = f.BBox.MinLat
		args["max_lat"] = f.BBox.MaxLat
		args["min_lon"] = f.BBox.MinLon
		args["max_lon"] = f.BBox.MaxLon
	}
	return q
}

func applyRidgeFilter(q string, args pgx.NamedArgs, f model.IceFilter) string {
	if !f.From.IsZero() {
		q += " AND ts >= @from"
		args["from"] = f.From
	}
	if !f.To.IsZero() {
		q += " AND ts <= @to"
		args["to"] = f.To
	}
	if f.HasBBox {
		q += " AND lat BETWEEN @min_lat AND @max_lat AND lon BETWEEN @min_lon AND @max_lon"
		args["min_lat"] = f.BBox.MinLat
		args["max_lat"] = f.BBox.MaxLat
		args["min_lon"] = f.BBox.MinLon
		args["max_lon"] = f.BBox.MaxLon
	}
	return q
}
