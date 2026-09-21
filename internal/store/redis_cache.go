package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// EchoSweep is the raw, recently-received echo slice served from cache.
type EchoSweep struct {
	ObservedAt      time.Time `json:"observed_at"`
	ShipLat         float64   `json:"ship_lat"`
	ShipLon         float64   `json:"ship_lon"`
	RangeM          float32   `json:"range_m"`
	BinSpacingM     float32   `json:"bin_spacing_m"`
	StartBearingRad float32   `json:"start_bearing_rad"`
	AngularStepRad  float32   `json:"angular_step_rad"`
	RayCount        int       `json:"ray_count"`
	BinsPerRay      int       `json:"bins_per_ray"`
	MeanIntensity   float64   `json:"mean_intensity"`
	// Rays are base64-encoded automatically by encoding/json.
	Rays [][]byte `json:"rays"`
}

const (
	indexKey     = "ice:echo:index"
	heartbeatKey = "ice:echo:heartbeat"
)

func sweepKey(t time.Time) string {
	return "ice:echo:sweep:" + fmt.Sprintf("%d", t.UnixNano())
}

// RedisCache retains the last retention window of raw echo slices.
type RedisCache struct {
	client    *redis.Client
	retention time.Duration
}

func NewRedisCache(ctx context.Context, addr, password string, db int, retention time.Duration) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &RedisCache{client: client, retention: retention}, nil
}

func (c *RedisCache) Close() error { return c.client.Close() }

func (c *RedisCache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

func (c *RedisCache) Retention() time.Duration { return c.retention }

// CacheSweep stores one echo slice, refreshes TTLs and evicts expired index entries.
func (c *RedisCache) CacheSweep(ctx context.Context, e EchoSweep) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal echo: %w", err)
	}
	key := sweepKey(e.ObservedAt)
	pipe := c.client.TxPipeline()
	pipe.Set(ctx, key, data, c.retention)
	pipe.ZAdd(ctx, indexKey, redis.Z{Score: float64(e.ObservedAt.UnixMilli()) / 1000, Member: key})
	pipe.Expire(ctx, indexKey, c.retention+time.Minute)
	cutoff := time.Now().Add(-c.retention).UnixMilli() / 1000
	pipe.ZRemRangeByScore(ctx, indexKey, "-inf", fmt.Sprintf("%d", cutoff))
	_, err = pipe.Exec(ctx)
	return err
}

// TouchHeartbeat records last radar-frontend contact for liveness checks.
func (c *RedisCache) TouchHeartbeat(ctx context.Context, ts time.Time) error {
	return c.client.Set(ctx, heartbeatKey, ts.UnixNano(), c.retention).Err()
}

func (c *RedisCache) LastHeartbeat(ctx context.Context) (time.Time, error) {
	v, err := c.client.Get(ctx, heartbeatKey).Int64()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, v).UTC(), nil
}

// RecentEchoes returns cached slices observed within [from,to), newest first.
func (c *RedisCache) RecentEchoes(ctx context.Context, tr TimeRange, limit int64) ([]EchoSweep, error) {
	keys, err := c.client.ZRevRangeByScore(ctx, indexKey, &redis.ZRangeBy{
		Min:    fmt.Sprintf("%f", float64(tr.From.UnixMilli())/1000),
		Max:    fmt.Sprintf("(%f", float64(tr.To.UnixMilli())/1000),
		Count:  limit,
		Offset: 0,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return []EchoSweep{}, nil
	}
	values, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]EchoSweep, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			continue
		}
		var e EchoSweep
		if err := json.Unmarshal([]byte(s), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
