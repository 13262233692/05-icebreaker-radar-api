package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"icebreaker-radar/internal/store"
)

// IngestMetricsSource exposes ingest counters for the status endpoint.
type IngestMetricsSource interface {
	Metrics() map[string]any
}

// Server exposes the ice-condition REST API.
type Server struct {
	pg           *store.Postgres
	cache        *store.RedisCache
	ingest       IngestMetricsSource
	defaultRange time.Duration
}

func New(pg *store.Postgres, cache *store.RedisCache, ingest IngestMetricsSource) *Server {
	return &Server{
		pg:           pg,
		cache:        cache,
		ingest:       ingest,
		defaultRange: time.Hour,
	}
}

// Router builds the gin engine with all routes registered.
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", s.health)
	r.GET("/api/v1/status", s.status)

	api := r.Group("/api/v1")
	{
		api.GET("/sweeps", s.listSweeps)
		api.GET("/ridges", s.listRidges)
		api.GET("/concentration", s.concentrationSeries)
		api.GET("/echoes/recent", s.recentEchoes)
		api.GET("/latest", s.latest)
	}
	return r
}

func (s *Server) health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	deps := gin.H{"api": "ok"}
	code := http.StatusOK
	if err := s.pg.Ping(ctx); err != nil {
		deps["postgres"] = "error: " + err.Error()
		code = http.StatusServiceUnavailable
	} else {
		deps["postgres"] = "ok"
	}
	if err := s.cache.Ping(ctx); err != nil {
		deps["redis"] = "error: " + err.Error()
		code = http.StatusServiceUnavailable
	} else {
		deps["redis"] = "ok"
	}
	c.JSON(code, gin.H{"status": deps})
}

func (s *Server) status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	resp := gin.H{}
	hb, err := s.cache.LastHeartbeat(ctx)
	switch {
	case err != nil:
		resp["last_heartbeat_error"] = err.Error()
	case hb.IsZero():
		resp["last_heartbeat"] = nil
	default:
		resp["last_heartbeat"] = hb
		resp["heartbeat_age_seconds"] = time.Since(hb).Seconds()
	}
	if s.ingest != nil {
		resp["ingest"] = s.ingest.Metrics()
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) listSweeps(c *gin.Context) {
	tr, bbox, limit, err := s.parseQuery(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	rows, err := s.pg.QuerySweeps(c.Request.Context(), tr, bbox, limit)
	if err != nil {
		internalError(c, err)
		return
	}
	okResponse(c, gin.H{"count": len(rows), "sweeps": rows})
}

func (s *Server) listRidges(c *gin.Context) {
	tr, bbox, limit, err := s.parseQuery(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	rows, err := s.pg.QueryRidges(c.Request.Context(), tr, bbox, limit)
	if err != nil {
		internalError(c, err)
		return
	}
	okResponse(c, gin.H{"count": len(rows), "ridges": rows})
}

func (s *Server) concentrationSeries(c *gin.Context) {
	tr, bbox, _, err := s.parseQuery(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	bucket := 60 * time.Second
	if v := c.Query("bucket_seconds"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 86400 {
			badRequest(c, "bucket_seconds must be an integer in [1,86400]")
			return
		}
		bucket = time.Duration(n) * time.Second
	}
	points, err := s.pg.ConcentrationSeries(c.Request.Context(), tr, bbox, bucket)
	if err != nil {
		internalError(c, err)
		return
	}
	okResponse(c, gin.H{"bucket_seconds": int(bucket.Seconds()), "points": points})
}

func (s *Server) recentEchoes(c *gin.Context) {
	tr, _, limit, err := s.parseQuery(c)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	// Echo slices are only cached for the retention window; clamp the range
	// so callers cannot request stale keys Redis has already evicted.
	earliest := time.Now().Add(-s.cache.Retention())
	if tr.From.Before(earliest) {
		tr.From = earliest
	}
	echoes, err := s.cache.RecentEchoes(c.Request.Context(), tr, int64(limit))
	if err != nil {
		internalError(c, err)
		return
	}
	okResponse(c, gin.H{
		"count":             len(echoes),
		"retention_seconds": int(s.cache.Retention().Seconds()),
		"echoes":            echoes,
	})
}

func (s *Server) latest(c *gin.Context) {
	limit := 1
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			badRequest(c, "limit must be an integer in [1,100]")
			return
		}
		limit = n
	}
	rows, err := s.pg.LatestSweeps(c.Request.Context(), limit)
	if err != nil {
		internalError(c, err)
		return
	}
	okResponse(c, gin.H{"count": len(rows), "sweeps": rows})
}
