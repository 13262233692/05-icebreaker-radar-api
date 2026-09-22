// Package cache 使用 Redis 有序集合维护最近 10 分钟的回波分析切片。
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"icebreaker-radar/internal/ice_analyzer"
)

const (
	recentKey     = "ice:echo:recent"
	defaultWindow = 10 * time.Minute
)

// Slice 是缓存中的单条回波分析切片。
type Slice struct {
	Result ice_analyzer.Result `json:"result"`
}

// Cache 封装 Redis 滑动窗口缓存。
type Cache struct {
	client *redis.Client
	window time.Duration
}

func New(addr, password string, db int) *Cache {
	return &Cache{
		client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db}),
		window: defaultWindow,
	}
}

func (c *Cache) Close() error { return c.client.Close() }

func (c *Cache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

// Add 将一条分析结果写入滑动窗口，并裁剪窗口外的旧数据。
func (c *Cache) Add(ctx context.Context, res *ice_analyzer.Result) error {
	payload, err := json.Marshal(Slice{Result: *res})
	if err != nil {
		return err
	}
	now := time.Now()
	cutoff := now.Add(-c.window).UnixMilli()
	// member 附加纳秒时间戳保证唯一性
	member := fmt.Sprintf("%d:%s", res.Timestamp.UnixNano(), payload)
	pipe := c.client.TxPipeline()
	pipe.ZAdd(ctx, recentKey, redis.Z{
		Score:  float64(res.Timestamp.UnixMilli()),
		Member: member,
	})
	pipe.ZRemRangeByScore(ctx, recentKey, "-inf", fmt.Sprintf("(%d", cutoff))
	pipe.Expire(ctx, recentKey, c.window+time.Minute)
	_, err = pipe.Exec(ctx)
	return err
}

// Recent 返回窗口内按时间升序的全部切片。
func (c *Cache) Recent(ctx context.Context) ([]ice_analyzer.Result, error) {
	cutoff := time.Now().Add(-c.window).UnixMilli()
	members, err := c.client.ZRangeByScore(ctx, recentKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", cutoff),
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}
	results := make([]ice_analyzer.Result, 0, len(members))
	for _, m := range members {
		// 去掉 "纳秒:" 前缀
		idx := 0
		for idx < len(m) && m[idx] != ':' {
			idx++
		}
		if idx >= len(m) {
			continue
		}
		var slice Slice
		if err := json.Unmarshal([]byte(m[idx+1:]), &slice); err != nil {
			continue
		}
		results = append(results, slice.Result)
	}
	return results, nil
}
