// Package http_api exposes ice-condition queries over REST using Gin.
package http_api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/icebreaker/ice-radar-api/internal/cache"
	"github.com/icebreaker/ice-radar-api/internal/storage"
)

// IngestStats is the subset of ingest counters shown in the health response.
type IngestStats interface {
	StatsSnapshot() any
}

// Server holds HTTP dependencies.
type Server struct {
	cache  *cache.EchoCache
	store  *storage.Postgres
	logger *slog.Logger
	http   *http.Server
	ingest IngestStats
}

func New(addr string, echoCache *cache.EchoCache, store *storage.Postgres,
	ingest IngestStats, logger *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger(logger))

	s := &Server{
		cache:  echoCache,
		store:  store,
		logger: logger,
		ingest: ingest,
	}

	api := r.Group("/api/v1")
	{
		api.GET("/health", s.health)
		api.GET("/echoes/latest", s.latestEcho)
		api.GET("/echoes", s.queryEchoes)
		api.GET("/stats", s.queryStats)
		api.GET("/stats/summary", s.statsSummary)
		api.GET("/ridges", s.queryRidges)
	}

	s.http = &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start serves HTTP until Shutdown is called.
func (s *Server) Start() error {
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Debug("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}
