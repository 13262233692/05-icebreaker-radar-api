// Command ice-radar-api runs the TCP ingest server, analysis pipeline and
// REST API for polar icebreaker radar echo data.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/icebreaker/ice-radar-api/internal/cache"
	"github.com/icebreaker/ice-radar-api/internal/config"
	"github.com/icebreaker/ice-radar-api/internal/http_api"
	"github.com/icebreaker/ice-radar-api/internal/ice_analyzer"
	"github.com/icebreaker/ice-radar-api/internal/pipeline"
	"github.com/icebreaker/ice-radar-api/internal/storage"
	"github.com/icebreaker/ice-radar-api/internal/tcp_ingest"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("invalid REDIS_URL", "error", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(redisOpts)
	echoCache := cache.New(redisClient, cfg.EchoTTL)

	const startupTimeout = 30 * time.Second
	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	store, err := storage.Connect(startupCtx, cfg.PostgresDSN)
	cancelStartup()
	if err != nil {
		logger.Error("postgres unavailable", "error", err)
		os.Exit(1)
	}
	logger.Info("postgres connected, migrations applied")

	analyzer := ice_analyzer.New(ice_analyzer.Config{
		IceThreshold:   cfg.IceThreshold,
		RidgeThreshold: cfg.RidgeThreshold,
		RidgeMinBins:   cfg.RidgeMinBins,
		RidgeMinRun:    cfg.RidgeMinRun,
	})

	pipe := pipeline.New(cfg, analyzer, echoCache, store, logger)
	go pipe.Run(ctx)

	tcpServer := tcp_ingest.New(cfg.TCPAddr, pipe, logger)
	go func() {
		if err := tcpServer.Start(ctx); err != nil {
			logger.Error("tcp ingest stopped", "error", err)
		}
	}()

	api := http_api.New(cfg.HTTPAddr, echoCache, store, tcpServer, logger)
	go func() {
		logger.Info("http api listening", "addr", cfg.HTTPAddr)
		if err := api.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := api.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	tcpServer.Close()
	pipe.Close()
	store.Close()
	if err := redisClient.Close(); err != nil {
		logger.Error("redis close error", "error", err)
	}
	logger.Info("shutdown complete")
}
