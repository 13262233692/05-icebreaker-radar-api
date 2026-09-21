package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/polar/icebreakerradar/internal/model"
)

// Config holds all runtime settings, sourced from environment variables.
type Config struct {
	TCPListenAddr  string
	HTTPListenAddr string

	RedisAddr     string
	RedisPassword string
	RedisDB       int
	// EchoTTL keeps recent echo slices in Redis for this duration.
	EchoTTL time.Duration

	PostgresDSN string

	Analyzer model.AnalysisParams
	// MaxConnections limits concurrent radar TCP clients.
	MaxConnections int
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	return Config{
		TCPListenAddr:  getenv("TCP_LISTEN_ADDR", ":9101"),
		HTTPListenAddr: getenv("HTTP_LISTEN_ADDR", ":8080"),

		RedisAddr:     getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getenv("REDIS_PASSWORD", ""),
		RedisDB:       getenvInt("REDIS_DB", 0),
		EchoTTL:       getenvDuration("ECHO_TTL", 10*time.Minute),

		PostgresDSN: getenv("POSTGRES_DSN",
			"postgres://ice:ice@localhost:5432/iceradar?sslmode=disable"),

		Analyzer: model.AnalysisParams{
			IceThreshold:   getenvInt("ICE_THRESHOLD", 100),
			RidgeThreshold: getenvInt("RIDGE_THRESHOLD", 170),
			MinRidgeBins:   getenvInt("RIDGE_MIN_BINS", 3),
		},
		MaxConnections: getenvInt("MAX_TCP_CONNECTIONS", 16),
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
