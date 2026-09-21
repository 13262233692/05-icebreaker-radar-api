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

	"icebreaker-radar/internal/config"
	"icebreaker-radar/internal/httpapi"
	"icebreaker-radar/internal/store"
	"icebreaker-radar/internal/tcpingest"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pg, err := store.NewPostgres(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("postgres init", "err", err)
		os.Exit(1)
	}
	defer pg.Close()
	logger.Info("postgres connected")

	cache, err := store.NewRedisCache(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.CacheTTL)
	if err != nil {
		logger.Error("redis init", "err", err)
		os.Exit(1)
	}
	defer cache.Close()
	logger.Info("redis connected", "cache_ttl", cfg.CacheTTL)

	ingest := tcpingest.NewServer(cfg.TCPAddr, cfg.AnalyzerConfig(), pg, cache, cfg.Workers, logger)

	// Adapt typed ingest metrics to the generic API-layer interface.
	metricsAdapter := metricsFunc(func() map[string]any {
		m := ingest.Metrics()
		return map[string]any{
			"active_conns": m.ActiveConns,
			"sweeps_in":    m.SweepsIn,
			"heartbeats":   m.Heartbeats,
			"persisted":    m.Persisted,
			"parse_errors": m.ParseErrors,
		}
	})

	api := httpapi.New(pg, cache, metricsAdapter)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ingestErr := make(chan error, 1)
	go func() {
		ingestErr <- ingest.Serve(ctx)
	}()

	go func() {
		logger.Info("http api listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", "err", err)
	}
	if err := <-ingestErr; err != nil {
		logger.Error("ingest stopped", "err", err)
	}
	logger.Info("shutdown complete")
}

type metricsFunc func() map[string]any

func (f metricsFunc) Metrics() map[string]any { return f() }
