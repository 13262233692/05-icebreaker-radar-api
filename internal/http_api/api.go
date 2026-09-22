// Package http_api 对外提供冰情查询 REST API（Gin）。
package http_api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"icebreaker-radar/internal/ice_analyzer"
	"icebreaker-radar/internal/store"
)

// RecentProvider 从缓存读取最近 10 分钟的回波分析切片。
type RecentProvider interface {
	Recent(ctx context.Context) ([]ice_analyzer.Result, error)
}

// StatsQuerier 从持久层按时空范围查询冰情统计。
type StatsQuerier interface {
	Query(ctx context.Context, f store.QueryFilter) ([]ice_analyzer.Result, error)
}

// API 聚合 HTTP 层依赖。
type API struct {
	recent RecentProvider
	stats  StatsQuerier
}

func New(recent RecentProvider, stats StatsQuerier) *API {
	return &API{recent: recent, stats: stats}
}

// Router 构建 Gin 路由。
func (a *API) Router() *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", a.healthz)
	v1 := r.Group("/api/v1")
	{
		v1.GET("/ice/recent", a.getRecent) // 最近 10 分钟（Redis 缓存）
		v1.GET("/ice/stats", a.getStats)   // 时间范围 + 经纬度矩形（PostgreSQL）
	}
	return r
}

func (a *API) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC()})
}

// getRecent 返回 Redis 滑动窗口（最近 10 分钟）内的回波分析切片。
// 可选查询参数：min_lat, max_lat, min_lon, max_lon 用于窗口内矩形过滤。
func (a *API) getRecent(c *gin.Context) {
	results, err := a.recent.Recent(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cache query failed: " + err.Error()})
		return
	}
	box, hasBox, err := parseBBox(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if hasBox {
		filtered := results[:0]
		for _, r := range results {
			if box.contains(r.Latitude, r.Longitude) {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	c.JSON(http.StatusOK, gin.H{
		"window": "10m",
		"count":  len(results),
		"data":   results,
	})
}

// getStats 按时间范围与经纬度矩形查询持久化的冰情统计。
// 必填：start, end（RFC3339 或 Unix 秒）；可选：min_lat, max_lat, min_lon, max_lon, limit。
func (a *API) getStats(c *gin.Context) {
	start, err := parseTimeParam(c, "start")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start: " + err.Error()})
		return
	}
	end, err := parseTimeParam(c, "end")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end: " + err.Error()})
		return
	}
	if !end.After(start) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end must be after start"})
		return
	}
	box, hasBox, err := parseBBox(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !hasBox {
		box = bbox{MinLat: -90, MaxLat: 90, MinLon: -180, MaxLon: 180}
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))

	results, err := a.stats.Query(c.Request.Context(), store.QueryFilter{
		Start: start, End: end,
		MinLat: box.MinLat, MaxLat: box.MaxLat,
		MinLon: box.MinLon, MaxLon: box.MaxLon,
		Limit: limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "store query failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"start": start, "end": end, "bbox": box,
		"count": len(results),
		"data":  results,
	})
}

type bbox struct {
	MinLat float64 `json:"min_lat"`
	MaxLat float64 `json:"max_lat"`
	MinLon float64 `json:"min_lon"`
	MaxLon float64 `json:"max_lon"`
}

func (b bbox) contains(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}

// parseBBox 解析可选的经纬度矩形参数；四个参数必须同时提供或同时缺省。
func parseBBox(c *gin.Context) (bbox, bool, error) {
	keys := []string{"min_lat", "max_lat", "min_lon", "max_lon"}
	vals := make([]float64, 4)
	provided := 0
	for i, k := range keys {
		s := c.Query(k)
		if s == "" {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return bbox{}, false, errInvalid(k, s)
		}
		vals[i] = v
		provided++
	}
	if provided == 0 {
		return bbox{}, false, nil
	}
	if provided != 4 {
		return bbox{}, false, errInvalid("bbox", "min_lat/max_lat/min_lon/max_lon must be provided together")
	}
	b := bbox{MinLat: vals[0], MaxLat: vals[1], MinLon: vals[2], MaxLon: vals[3]}
	if b.MinLat > b.MaxLat || b.MinLon > b.MaxLon ||
		b.MinLat < -90 || b.MaxLat > 90 || b.MinLon < -180 || b.MaxLon > 180 {
		return bbox{}, false, errInvalid("bbox", "out of valid range or min > max")
	}
	return b, true, nil
}

// parseTimeParam 支持 RFC3339 与 Unix 秒两种格式。
func parseTimeParam(c *gin.Context, key string) (time.Time, error) {
	s := c.Query(key)
	if s == "" {
		return time.Time{}, errInvalid(key, "required")
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC(), nil
	}
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(sec, 0).UTC(), nil
	}
	return time.Time{}, errInvalid(key, "must be RFC3339 or unix seconds")
}

type paramError struct{ msg string }

func (e *paramError) Error() string { return e.msg }

func errInvalid(key, why string) error {
	return &paramError{msg: "invalid parameter " + key + ": " + why}
}
