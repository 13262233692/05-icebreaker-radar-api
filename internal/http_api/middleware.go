package http_api

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

func requestLogger() gin.HandlerFunc {
	logger := slog.Default()
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Debug("http", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "latency_ms", time.Since(start).Milliseconds())
	}
}
