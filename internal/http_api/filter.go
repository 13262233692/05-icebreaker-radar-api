package http_api

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/polar/icebreakerradar/internal/model"
)

// parseFilter extracts the shared time-range / bbox / limit query parameters.
// Missing times default to [now-1h, now]; times accept RFC3339 or unix
// seconds (numeric).
func parseFilter(c *gin.Context) (model.IceFilter, error) {
	now := time.Now().UTC()
	from := now.Add(-defaultRange)
	to := now

	if v := c.Query("from"); v != "" {
		t, err := parseTime(v)
		if err != nil {
			return model.IceFilter{}, badRequest("invalid 'from': " + err.Error())
		}
		from = t
	}
	if v := c.Query("to"); v != "" {
		t, err := parseTime(v)
		if err != nil {
			return model.IceFilter{}, badRequest("invalid 'to': " + err.Error())
		}
		to = t
	}
	if !from.Before(to) {
		return model.IceFilter{}, badRequest("'from' must be before 'to'")
	}

	limit := defaultLimit
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return model.IceFilter{}, badRequest("'limit' must be a positive integer")
		}
		limit = n
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	f := model.IceFilter{From: from, To: to, Limit: limit}

	// BBox is active only when all four edges are supplied.
	edges := [4]string{c.Query("min_lat"), c.Query("max_lat"),
		c.Query("min_lon"), c.Query("max_lon")}
	if anyEmpty(edges) {
		if anyNonEmpty(edges) {
			return model.IceFilter{},
				badRequest("bbox requires all of min_lat,max_lat,min_lon,max_lon")
		}
		return f, nil
	}
	vals, err := parseEdges(edges)
	if err != nil {
		return model.IceFilter{}, err
	}
	box := model.BBox{
		MinLat: vals[0], MaxLat: vals[1], MinLon: vals[2], MaxLon: vals[3],
	}
	if !box.Valid() {
		return model.IceFilter{},
			badRequest("invalid bbox: need -90<=min_lat<=max_lat<=90 and " +
				"-180<=min_lon<=max_lon<=180")
	}
	f.BBox = box
	f.HasBBox = true
	return f, nil
}

func parseTime(v string) (time.Time, error) {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func parseEdges(e [4]string) ([4]float64, error) {
	var out [4]float64
	names := [4]string{"min_lat", "max_lat", "min_lon", "max_lon"}
	for i, raw := range e {
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return out, badRequest("'" + names[i] + "' must be a number")
		}
		out[i] = n
	}
	return out, nil
}

func anyEmpty(e [4]string) bool {
	for _, v := range e {
		if v == "" {
			return true
		}
	}
	return false
}

func anyNonEmpty(e [4]string) bool {
	for _, v := range e {
		if v != "" {
			return true
		}
	}
	return false
}
