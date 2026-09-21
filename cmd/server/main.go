// Command iceradar-server runs the TCP ingest pipeline and the ice-condition
// REST API in one process.
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

	"github.com/polar/icebreakerradar/internal/config"
	"github.com/polar/icebreakerradar/internal/http_api"
	"github.com/polar/icebreakerradar/internal/ice_analyzer"
	"github.com/polar/icebreakerradar/internal/storage"
	"github.com/polar/icebreakerradar/internal/tcp_ingest"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(log)

	cfg := config.Load()
	rootCtx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Storage. PostgreSQL is required for durability; fail fast if absent.
	redisStore, err := storage.NewRedisStore(rootCtx, cfg.RedisAddr,
		cfg.RedisPassword, cfg.RedisDB, cfg.EchoTTL)
	if err != nil {
		log.Error("redis unavailable", "err", err)
		os.Exit(1)
	}
	defer redisStore.Close()

	pgStore, err := storage.NewPostgresStore(rootCtx, cfg.PostgresDSN)
	if err != nil {
		log.Error("postgres unavailable", "err", err)
		os.Exit(1)
	}
	defer pgStore.Close()

	analyzer := ice_analyzer.New(cfg.Analyzer)

	// TCP ingest.
	ingest := tcp_ingest.NewServer(
		tcp_ingest.DefaultPipelineConfig(cfg.TCPListenAddr, cfg.MaxConnections),
		analyzer, redisStore, pgStore, log)

	ingestErr := make(chan error, 1)
	go func() { ingestErr <- ingest.ListenAndServe() }()

	// HTTP API.
	api := http_api.NewServer(redisStore, pgStore, redisStore, ingest.Snapshot)
	httpServer := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	httpErr := make(chan error, 1)
	go func() {
		log.Info("HTTP API listening", "addr", cfg.HTTPListenAddr)
		if err := httpServer.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
		close(httpErr)
	}()

	select {
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	case err := <-ingestErr:
		log.Error("ingest server stopped", "err", err)
	case err := <-httpErr:
		if err != nil {
			log.Error("http server stopped", "err", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown error", "err", err)
	}
	if err := ingest.Shutdown(shutdownCtx); err != nil {
		log.Error("ingest shutdown error", "err", err)
	}
	log.Info("shutdown complete")
}
