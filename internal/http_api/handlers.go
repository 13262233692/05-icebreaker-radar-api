package http_api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/polar/icebreakerradar/internal/model"
)

// echoDTO is one cached echo slice plus its analysis; raw intensity is
// included only with include_echo=1 (it can be large).
type echoDTO struct {
	model.Sweep
	Analysis  *model.SweepAnalysis `json:"analysis"`
	Intensity []byte               `json:"intensity,omitempty"`
}

// GET /api/v1/echoes — recent slices from the Redis 10-minute window.
func (s *Server) listEchoes(c *gin.Context) {
	f, err := parseFilter(c)
	if abortOnError(c, err) {
		return
	}
	includeEcho := c.Query("include_echo") == "1" || c.Query("include_echo") == "true"

	// Redis only retains the retention window; clamp the requested range to it.
	var envs []model.Envelope
	envs, err = s.cache.Recent(c.Request.Context(), f)
	if abortOnError(c, err) {
		return
	}

	items := make([]echoDTO, 0, len(envs))
	for _, env := range envs {
		dto := echoDTO{Sweep: *env.Sweep, Analysis: env.Analysis}
		if includeEcho {
			dto.Intensity = env.Sweep.Intensity
		}
		items = append(items, dto)
	}
	c.JSON(http.StatusOK, gin.H{
		"count": len(items),
		"items": items,
		"query": filterEcho(f),
	})
}

// GET /api/v1/ice/observations — persisted per-slice statistics.
func (s *Server) listObservations(c *gin.Context) {
	f, err := parseFilter(c)
	if abortOnError(c, err) {
		return
	}
	items, err := s.db.QueryObservations(c.Request.Context(), f)
	if abortOnError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count": len(items),
		"items": items,
		"query": filterEcho(f),
	})
}

// GET /api/v1/ice/ridges — geo-located pressure-ridge detections.
func (s *Server) listRidges(c *gin.Context) {
	f, err := parseFilter(c)
	if abortOnError(c, err) {
		return
	}
	items, err := s.db.QueryRidges(c.Request.Context(), f)
	if abortOnError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count": len(items),
		"items": items,
		"query": filterEcho(f),
	})
}

// summaryDTO aggregates ice statistics over the query window.
type summaryDTO struct {
	Observations        int     `json:"observations"`
	RidgeCount          int     `json:"ridge_count"`
	MeanConcentration   float64 `json:"mean_concentration"`
	MaxConcentration    float64 `json:"max_concentration"`
	LatestConcentration float64 `json:"latest_concentration"`
	LatestTime          *string `json:"latest_time,omitempty"`
	LatestShipLat       float64 `json:"latest_ship_lat"`
	LatestShipLon       float64 `json:"latest_ship_lon"`
}

// GET /api/v1/ice/summary — aggregated ice condition over the time/bbox.
func (s *Server) iceSummary(c *gin.Context) {
	f, err := parseFilter(c)
	if abortOnError(c, err) {
		return
	}
	obs, err := s.db.QueryObservations(c.Request.Context(), f)
	if abortOnError(c, err) {
		return
	}
	ridges, err := s.db.QueryRidges(c.Request.Context(), f)
	if abortOnError(c, err) {
		return
	}

	out := summaryDTO{Observations: len(obs), RidgeCount: len(ridges)}
	var sum float64
	for i := len(obs) - 1; i >= 0; i-- {
		o := obs[i]
		sum += o.Concentration
		if o.Concentration > out.MaxConcentration {
			out.MaxConcentration = o.Concentration
		}
	}
	if len(obs) > 0 {
		out.MeanConcentration = sum / float64(len(obs))
		// QueryObservations is newest-first.
		latest := obs[0]
		out.LatestConcentration = latest.Concentration
		t := latest.Time.Format("2006-01-02T15:04:05Z07:00")
		out.LatestTime = &t
		out.LatestShipLat = latest.ShipLat
		out.LatestShipLon = latest.ShipLon
	}
	c.JSON(http.StatusOK, gin.H{"summary": out, "query": filterEcho(f)})
}

func filterEcho(f model.IceFilter) gin.H {
	q := gin.H{
		"from":  f.From.Format("2006-01-02T15:04:05Z07:00"),
		"to":    f.To.Format("2006-01-02T15:04:05Z07:00"),
		"limit": f.Limit,
	}
	if f.HasBBox {
		q["bbox"] = f.BBox
	}
	return q
}
