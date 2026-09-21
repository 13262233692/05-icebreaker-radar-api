package config

import (
	"os"
	"strconv"
	"time"

	"icebreaker-radar/internal/iceanalyzer"
)

// Config holds all runtime settings, populated from environment variables.
type Config struct {
	TCPAddr       string
	HTTPAddr      string
	PostgresDSN   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	CacheTTL      time.Duration
	Workers       int
	IceThreshold  int
}

func Load() Config {
	cfg := Config{
		TCPAddr:       getenv("TCP_ADDR", ":9101"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		PostgresDSN:   getenv("POSTGRES_DSN", "postgres://ice:ice@localhost:5432/iceradar?sslmode=disable"),
		RedisAddr:     getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getenv("REDIS_PASSWORD", ""),
		RedisDB:       getenvInt("REDIS_DB", 0),
		CacheTTL:      time.Duration(getenvInt("CACHE_TTL_SECONDS", 600)) * time.Second,
		Workers:       getenvInt("WORKERS", 4),
		IceThreshold:  getenvInt("ICE_THRESHOLD", 120),
	}
	return cfg
}

// AnalyzerConfig maps env-derived settings onto the analyzer config.
func (c Config) AnalyzerConfig() iceanalyzer.Config {
	a := iceanalyzer.DefaultConfig()
	if c.IceThreshold > 0 && c.IceThreshold <= 255 {
		a.IceThreshold = byte(c.IceThreshold)
	}
	return a
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
