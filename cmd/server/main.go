// 冰情雷达回波解析后端：TCP 接入 -> 解析 -> 分析 -> Redis 缓存 / PostgreSQL 持久化 -> REST 查询。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"icebreaker-radar/internal/cache"
	"icebreaker-radar/internal/echo_parser"
	"icebreaker-radar/internal/http_api"
	"icebreaker-radar/internal/ice_analyzer"
	"icebreaker-radar/internal/store"
	"icebreaker-radar/internal/tcp_ingest"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	var (
		tcpAddr  = env("TCP_LISTEN_ADDR", ":9000")
		httpAddr = env("HTTP_LISTEN_ADDR", ":8080")
		redisAdr = env("REDIS_ADDR", "localhost:6379")
		pgDSN    = env("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/icebreaker?sslmode=disable")
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// PostgreSQL 持久层
	pg, err := store.New(pgDSN)
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}
	defer pg.Close()
	if err := pg.Migrate(ctx); err != nil {
		log.Fatalf("postgres migrate: %v", err)
	}
	log.Println("[main] postgres ready")

	// Redis 缓存（最近 10 分钟滑动窗口）
	rc := cache.New(redisAdr, env("REDIS_PASSWORD", ""), 0)
	defer rc.Close()
	if err := rc.Ping(ctx); err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	log.Println("[main] redis ready")

	// 分析配置
	analyzerCfg := ice_analyzer.DefaultConfig()

	// TCP 接入：每帧 -> 分析 -> 缓存 + 持久化
	ingest := tcp_ingest.NewServer(tcpAddr, func(ctx context.Context, frame *echo_parser.EchoFrame) {
		result := ice_analyzer.Analyze(frame, analyzerCfg)
		if err := rc.Add(ctx, result); err != nil {
			log.Printf("[main] cache add failed: %v", err)
		}
		if err := pg.Insert(ctx, result); err != nil {
			log.Printf("[main] store insert failed: %v", err)
		}
	})
	go func() {
		if err := ingest.Start(ctx); err != nil {
			log.Printf("[tcp_ingest] server stopped: %v", err)
		}
	}()

	// REST API
	api := http_api.New(rc, pg)
	srv := &http.Server{Addr: httpAddr, Handler: api.Router()}
	go func() {
		log.Printf("[http_api] listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[main] shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = ingest.Shutdown()
}
