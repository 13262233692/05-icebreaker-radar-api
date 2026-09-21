package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"icebreaker-radar/internal/store"
)

const (
	defaultLimit = 1000
	maxLimit     = 5000
)

// parseQuery extracts the time range, optional lat/lon rectangle and row limit
// shared by the query endpoints.
//
// Time:
//
//	from/to accept RFC3339 (e.g. 2026-09-21T00:00:00Z) or unix epoch seconds.
//	If omitted, the last defaultRange window is used.
//
// BBox (all four required when any is supplied):
//
//	min_lat, max_lat, min_lon, max_lon in decimal degrees.
func (s *Server) parseQuery(c *gin.Context) (store.TimeRange, *store.BBox, int, error) {
	now := time.Now().UTC()
	tr := store.TimeRange{From: now.Add(-s.defaultRange), To: now}

	if v := c.Query("from"); v != "" {
		t, err := parseTime(v)
		if err != nil {
			return tr, nil, 0, fmt.Errorf("invalid from: %w", err)
		}
		tr.From = t
	}
	if v := c.Query("to"); v != "" {
		t, err := parseTime(v)
		if err != nil {
			return tr, nil, 0, fmt.Errorf("invalid to: %w", err)
		}
		tr.To = t
	}
	if !tr.From.Before(tr.To) {
		return tr, nil, 0, errors.New("from must be strictly before to")
	}

	bbox, err := parseBBox(c)
	if err != nil {
		return tr, nil, 0, err
	}

	limit := defaultLimit
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return tr, nil, 0, errors.New("limit must be a positive integer")
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}
	return tr, bbox, limit, nil
}

func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC(), nil
	}
	return time.Time{}, errors.New("expected RFC3339 timestamp or unix epoch seconds")
}

func parseBBox(c *gin.Context) (*store.BBox, error) {
	keys := []string{"min_lat", "max_lat", "min_lon", "max_lon"}
	raw := map[string]string{}
	present := false
	for _, k := range keys {
		if v := c.Query(k); v != "" {
			raw[k] = v
			present = true
		}
	}
	if !present {
		return nil, nil
	}
	if len(raw) != 4 {
		return nil, fmt.Errorf("bbox requires all of min_lat,max_lat,min_lon,max_lon (got %d/4)", len(raw))
	}
	get := func(k string) (float64, error) {
		return strconv.ParseFloat(raw[k], 64)
	}
	minLat, err := get("min_lat")
	if err != nil {
		return nil, fmt.Errorf("invalid min_lat: %w", err)
	}
	maxLat, err := get("max_lat")
	if err != nil {
		return nil, fmt.Errorf("invalid max_lat: %w", err)
	}
	minLon, err := get("min_lon")
	if err != nil {
		return nil, fmt.Errorf("invalid min_lon: %w", err)
	}
	maxLon, err := get("max_lon")
	if err != nil {
		return nil, fmt.Errorf("invalid max_lon: %w", err)
	}

	if minLat < -90 || maxLat > 90 || minLat >= maxLat {
		return nil, errors.New("require -90 <= min_lat < max_lat <= 90")
	}
	if minLon < -180 || maxLon > 180 || minLon >= maxLon {
		return nil, errors.New("require -180 <= min_lon < max_lon <= 180")
	}
	return &store.BBox{MinLat: minLat, MaxLat: maxLat, MinLon: minLon, MaxLon: maxLon}, nil
}

func okResponse(c *gin.Context, data gin.H) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": data})
}

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
}

func internalError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
}
