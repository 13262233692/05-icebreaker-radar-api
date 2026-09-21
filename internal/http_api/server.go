// Package http_api exposes the ice-condition query API with Gin: recent raw
// echoes from Redis, durable statistics and ridges from PostgreSQL, all
// filterable by UTC time range and lat/lon rectangle.
package http_api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/polar/icebreakerradar/internal/model"
	"github.com/polar/icebreakerradar/internal/tcp_ingest"
)

const (
	defaultLimit = 500
	maxLimit     = 5000
	defaultRange = time.Hour
)

// EchoCache serves analysed slices from the 10-minute Redis window.
type EchoCache interface {
	Recent(ctx context.Context, f model.IceFilter) ([]model.Envelope, error)
	Range(ctx context.Context, f model.IceFilter) ([]model.Envelope, error)
}

// IceDB serves durable observations and ridges from PostgreSQL.
type IceDB interface {
	QueryObservations(ctx context.Context, f model.IceFilter) ([]model.SweepAnalysis, error)
	QueryRidges(ctx context.Context, f model.IceFilter) ([]model.Ridge, error)
	Ping(ctx context.Context) error
}

// Pinger is implemented by backing stores for the health endpoint.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server wires the HTTP routes.
type Server struct {
	router *gin.Engine
	cache  EchoCache
	db     IceDB
	stats  func() tcp_ingest.Stats
	redis  Pinger
}

// NewServer builds the Gin engine and registers all routes.
func NewServer(cache EchoCache, db IceDB, redis Pinger,
	statsFn func() tcp_ingest.Stats) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())

	s := &Server{router: r, cache: cache, db: db, stats: statsFn, redis: redis}

	r.GET("/health", s.health)

	v1 := r.Group("/api/v1")
	{
		v1.GET("/echoes", s.listEchoes)
		v1.GET("/ice/observations", s.listObservations)
		v1.GET("/ice/ridges", s.listRidges)
		v1.GET("/ice/summary", s.iceSummary)
		v1.GET("/ingest/stats", s.ingestStats)
	}
	return s
}

// Handler exposes the engine to the HTTP server host.
func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) health(c *gin.Context) {
	status := gin.H{"status": "ok"}
	code := http.StatusOK
	deps := gin.H{}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		deps["postgres"] = err.Error()
		status["status"] = "degraded"
		code = http.StatusServiceUnavailable
	} else {
		deps["postgres"] = "ok"
	}
	if s.redis != nil {
		if err := s.redis.Ping(ctx); err != nil {
			deps["redis"] = err.Error()
			status["status"] = "degraded"
			code = http.StatusServiceUnavailable
		} else {
			deps["redis"] = "ok"
		}
	}
	status["dependencies"] = deps
	c.JSON(code, status)
}

func (s *Server) ingestStats(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ingest": s.stats()})
}
