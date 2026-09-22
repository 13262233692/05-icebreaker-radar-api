// Package cache stores the most recent radar echo slices in Redis so that the
// API can serve live, high-resolution sweeps without touching PostgreSQL.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/icebreaker/ice-radar-api/internal/model"
)

const (
	zsetKey        = "radar:echo:recent"
	frameKeyPrefix = "radar:echo:frame:"
)

// ErrCacheMiss is returned when a requested frame is no longer cached.
var ErrCacheMiss = errors.New("cache: frame not found or expired")

// EchoCache keeps recent echo slices available for live queries.
type EchoCache struct {
	client *redis.Client
	ttl    time.Duration
}

func New(client *redis.Client, ttl time.Duration) *EchoCache {
	return &EchoCache{client: client, ttl: ttl}
}

// Put writes one frame to the cache and prunes frames older than the TTL.
func (c *EchoCache) Put(ctx context.Context, af model.AnalyzedFrame) error {
	cf := model.CachedFrame{
		FrameID:          af.Frame.FrameID,
		SweepID:          af.Frame.SweepID,
		Timestamp:        af.Frame.Timestamp,
		VesselLat:        af.Frame.VesselLat,
		VesselLon:        af.Frame.VesselLon,
		Heading:          af.Frame.Heading,
		AzStart:          af.Frame.AzStart,
		AzEnd:            af.Frame.AzEnd,
		BinSpacing:       af.Frame.BinSpacing,
		RangeBins:        af.Frame.RangeBins,
		Intensity:        af.Frame.Intensity,
		MeanIntensity:    af.Stats.MeanIntensity,
		MaxIntensity:     af.Stats.MaxIntensity,
		IceConcentration: af.Stats.IceConcentration,
		RidgeCount:       af.Stats.RidgeCount,
	}
	payload, err := json.Marshal(cf)
	if err != nil {
		return fmt.Errorf("cache: marshal frame: %w", err)
	}

	member := strconv.FormatUint(uint64(cf.FrameID), 10)
	score := float64(cf.Timestamp.UnixNano())

	pipe := c.client.TxPipeline()
	pipe.ZAdd(ctx, zsetKey, redis.Z{Score: score, Member: member})
	pipe.Set(ctx, frameKeyPrefix+member, payload, c.ttl)
	// Drop index entries that have already aged out of the TTL window.
	cutoff := time.Now().Add(-c.ttl).UnixNano()
	pipe.ZRemRangeByScore(ctx, zsetKey, "-inf", strconv.FormatInt(cutoff, 10))
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("cache: put frame: %w", err)
	}
	return nil
}

// Recent returns frames observed within [from, to], newest first.
func (c *EchoCache) Recent(ctx context.Context, from, to time.Time) ([]model.CachedFrame, error) {
	min := strconv.FormatInt(from.UnixNano(), 10)
	max := strconv.FormatInt(to.UnixNano(), 10)
	members, err := c.client.ZRevRangeByScore(ctx, zsetKey, &redis.ZRangeBy{
		Min: min, Max: max, Count: 500,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: query index: %w", err)
	}
	if len(members) == 0 {
		return nil, nil
	}

	keys := make([]string, len(members))
	for i, m := range members {
		keys[i] = frameKeyPrefix + m
	}
	values, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("cache: mget frames: %w", err)
	}

	out := make([]model.CachedFrame, 0, len(values))
	for _, v := range values {
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		var cf model.CachedFrame
		if err := json.Unmarshal([]byte(s), &cf); err != nil {
			continue
		}
		out = append(out, cf)
	}
	return out, nil
}

// Latest returns the most recent cached frame.
func (c *EchoCache) Latest(ctx context.Context) (model.CachedFrame, error) {
	members, err := c.client.ZRevRange(ctx, zsetKey, 0, 0).Result()
	if err != nil {
		return model.CachedFrame{}, fmt.Errorf("cache: latest index: %w", err)
	}
	if len(members) == 0 {
		return model.CachedFrame{}, ErrCacheMiss
	}
	val, err := c.client.Get(ctx, frameKeyPrefix+members[0]).Bytes()
	if errors.Is(err, redis.Nil) {
		return model.CachedFrame{}, ErrCacheMiss
	}
	if err != nil {
		return model.CachedFrame{}, fmt.Errorf("cache: get latest: %w", err)
	}
	var cf model.CachedFrame
	if err := json.Unmarshal(val, &cf); err != nil {
		return model.CachedFrame{}, fmt.Errorf("cache: decode latest: %w", err)
	}
	return cf, nil
}

// Ping verifies connectivity to Redis.
func (c *EchoCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}
