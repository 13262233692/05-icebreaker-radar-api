package http_api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/icebreaker/ice-radar-api/internal/cache"
	"github.com/icebreaker/ice-radar-api/internal/model"
)

const maxQueryWindow = 30 * 24 * time.Hour

func (s *Server) health(c *gin.Context) {
	resp := gin.H{"status": "ok", "time": time.Now().UTC()}

	redisOK := true
	if err := s.cache.Ping(c.Request.Context()); err != nil {
		redisOK = false
		resp["redis_error"] = err.Error()
	}
	resp["redis"] = redisOK

	if s.ingest != nil {
		resp["ingest"] = s.ingest.StatsSnapshot()
	}

	status := http.StatusOK
	if !redisOK {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, resp)
}

func (s *Server) latestEcho(c *gin.Context) {
	cf, err := s.cache.Latest(c.Request.Context())
	if errors.Is(err, cache.ErrCacheMiss) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no echo frames cached yet"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"frame": cf})
}

// queryEchoes serves the recent (~TTL window) full echo slices from Redis.
func (s *Server) queryEchoes(c *gin.Context) {
	from, to, err := parseTimeRange(c, 10*time.Minute)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	frames, err := s.cache.Recent(c.Request.Context(), from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	minLat, maxLat, minLon, maxLon, err := parseBBox(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if bboxPresent(minLat, maxLat, minLon, maxLon) {
		frames = filterCachedByBBox(frames, minLat, maxLat, minLon, maxLon)
	}

	c.JSON(http.StatusOK, gin.H{
		"count":  len(frames),
		"frames": frames,
	})
}

func (s *Server) queryStats(c *gin.Context) {
	f, err := parseFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rows, err := s.store.QueryStats(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(rows), "stats": rows})
}

func (s *Server) statsSummary(c *gin.Context) {
	f, err := parseFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	summary, err := s.store.Summarize(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

func (s *Server) queryRidges(c *gin.Context) {
	f, err := parseFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rows, err := s.store.QueryRidges(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(rows), "ridges": rows})
}

func parseFilter(c *gin.Context) (model.QueryFilter, error) {
	from, to, err := parseTimeRange(c, time.Hour)
	if err != nil {
		return model.QueryFilter{}, err
	}
	minLat, maxLat, minLon, maxLon, err := parseBBox(c)
	if err != nil {
		return model.QueryFilter{}, err
	}
	limit := 1000
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 10000 {
			return model.QueryFilter{}, errors.New("limit must be between 1 and 10000")
		}
		limit = n
	}
	return model.QueryFilter{
		Start:   from,
		End:     to,
		MinLat:  minLat,
		MaxLat:  maxLat,
		MinLon:  minLon,
		MaxLon:  maxLon,
		HasBBox: bboxPresent(minLat, maxLat, minLon, maxLon),
		Limit:   limit,
	}, nil
}

// parseTimeRange accepts start/end as RFC3339 timestamps. When absent the
// window defaults to [now-defaultWindow, now].
func parseTimeRange(c *gin.Context, defaultWindow time.Duration) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-defaultWindow)
	to := now

	if v := c.Query("start"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("start must be RFC3339, e.g. 2026-09-22T00:00:00Z")
		}
		from = t
	}
	if v := c.Query("end"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("end must be RFC3339, e.g. 2026-09-22T01:00:00Z")
		}
		to = t
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, errors.New("start must be before end")
	}
	if to.Sub(from) > maxQueryWindow {
		return time.Time{}, time.Time{}, errors.New("query window must not exceed 30 days")
	}
	return from, to, nil
}

func parseBBox(c *gin.Context) (minLat, maxLat, minLon, maxLon float64, err error) {
	get := func(name string) (float64, bool) {
		v := c.Query(name)
		if v == "" {
			return 0, false
		}
		n, perr := strconv.ParseFloat(v, 64)
		if perr != nil {
			err = errors.New(name + " must be a number")
			return 0, false
		}
		return n, true
	}

	vals := make(map[string]float64)
	present := make(map[string]bool)
	for _, name := range []string{"min_lat", "max_lat", "min_lon", "max_lon"} {
		if n, ok := get(name); ok {
			vals[name] = n
			present[name] = true
		}
	}
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if len(present) > 0 && len(present) != 4 {
		return 0, 0, 0, 0, errors.New("bbox requires all of min_lat,max_lat,min_lon,max_lon")
	}
	if len(present) == 4 {
		minLat, maxLat = vals["min_lat"], vals["max_lat"]
		minLon, maxLon = vals["min_lon"], vals["max_lon"]
		if minLat < -90 || maxLat > 90 || minLat > maxLat {
			return 0, 0, 0, 0, errors.New("invalid latitude range")
		}
		if minLon < -180 || maxLon > 180 || minLon > maxLon {
			return 0, 0, 0, 0, errors.New("invalid longitude range")
		}
	}
	return minLat, maxLat, minLon, maxLon, nil
}

func bboxPresent(minLat, maxLat, minLon, maxLon float64) bool {
	return minLat != 0 || maxLat != 0 || minLon != 0 || maxLon != 0
}

func filterCachedByBBox(frames []model.CachedFrame, minLat, maxLat, minLon, maxLon float64) []model.CachedFrame {
	out := frames[:0]
	for _, f := range frames {
		if f.VesselLat >= minLat && f.VesselLat <= maxLat &&
			f.VesselLon >= minLon && f.VesselLon <= maxLon {
			out = append(out, f)
		}
	}
	return out
}
