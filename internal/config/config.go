package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds runtime configuration for the ice-radar backend.
type Config struct {
	TCPAddr         string
	HTTPAddr        string
	RedisURL        string
	PostgresDSN     string
	EchoTTL         time.Duration
	IceThreshold    uint8
	RidgeThreshold  uint8
	RidgeMinBins    int
	RidgeMinRun     int
	BatchSize       int
	BatchFlushAfter time.Duration
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// Load reads configuration from environment variables with safe defaults.
func Load() Config {
	return Config{
		TCPAddr:         getenv("TCP_ADDR", ":9101"),
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		RedisURL:        getenv("REDIS_URL", "redis://localhost:6379/0"),
		PostgresDSN:     getenv("POSTGRES_DSN", "postgres://radar:radar@localhost:5432/iceradar?sslmode=disable"),
		EchoTTL:         time.Duration(getenvInt("ECHO_TTL_SECONDS", 600)) * time.Second,
		IceThreshold:    uint8(getenvInt("ICE_THRESHOLD", 120)),
		RidgeThreshold:  uint8(getenvInt("RIDGE_THRESHOLD", 200)),
		RidgeMinBins:    getenvInt("RIDGE_MIN_BINS", 4),
		RidgeMinRun:     getenvInt("RIDGE_MIN_RUN", 2),
		BatchSize:       getenvInt("BATCH_SIZE", 100),
		BatchFlushAfter: time.Duration(getenvInt("BATCH_FLUSH_MS", 1000)) * time.Millisecond,
	}
}
