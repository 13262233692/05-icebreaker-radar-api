// Package storage provides the recent-echo Redis cache and the durable
// PostgreSQL ice-statistics store.
package storage

import (
	"bytes"
	"context"
	"encoding/gob"
	"strconv"
	"time"

	"github.com/polar/icebreakerradar/internal/model"
	"github.com/redis/go-redis/v9"
)

const (
	// echoZSet holds analysed envelopes scored by sweep unix timestamp.
	echoZSet = "radar:echo:slices"
)

func init() {
	// Envelope embeds only plain model structs; register for gob clarity.
	gob.Register(model.Envelope{})
}

// RedisStore caches the most recent analysed echo slices (default 10 minutes)
// and answers the recent-echo REST queries.
type RedisStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisStore connects to Redis and verifies reachability with a PING.
func NewRedisStore(ctx context.Context, addr, password string, db int, ttl time.Duration) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisStore{client: client, ttl: ttl}, nil
}

// Add caches one analysed slice, evicts entries older than the retention
// window, and refreshes the key TTL.
func (s *RedisStore) Add(ctx context.Context, env model.Envelope) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(env); err != nil {
		return err
	}
	member := redis.Z{
		Score:  float64(env.Sweep.Time.UnixMilli()),
		Member: buf.Bytes(),
	}
	cutoff := time.Now().Add(-s.ttl).UnixMilli()
	pipe := s.client.TxPipeline()
	pipe.ZAdd(ctx, echoZSet, member)
	pipe.ZRemRangeByScore(ctx, echoZSet, "-inf", strconv.FormatInt(cutoff, 10))
	pipe.Expire(ctx, echoZSet, s.ttl+time.Minute)
	_, err := pipe.Exec(ctx)
	return err
}

// Recent returns up to limit envelopes from the retention window, newest
// first, optionally intersecting the slice coverage box with bbox.
func (s *RedisStore) Recent(ctx context.Context, f model.IceFilter) ([]model.Envelope, error) {
	max := time.Now()
	min := max.Add(-s.ttl)
	if !f.From.IsZero() && f.From.After(min) {
		min = f.From
	}
	if !f.To.IsZero() && f.To.Before(max) {
		max = f.To
	}
	// ZREVRANGEBYSCORE max min LIMIT 0 limit
	opts := &redis.ZRangeBy{
		Min:    strconv.FormatInt(min.UnixMilli(), 10),
		Max:    strconv.FormatInt(max.UnixMilli(), 10),
		Count:  int64(f.Limit * 4), // over-fetch for post bbox filtering
		Offset: 0,
	}
	raws, err := s.client.ZRevRangeByScore(ctx, echoZSet, opts).Result()
	if err != nil {
		return nil, err
	}
	return decodeAndFilter(raws, f, f.Limit)
}

// Range returns envelopes within [from, to] (newest first), subject to the
// same in-memory bbox filter.
func (s *RedisStore) Range(ctx context.Context, f model.IceFilter) ([]model.Envelope, error) {
	opts := &redis.ZRangeBy{
		Min:    strconv.FormatInt(f.From.UnixMilli(), 10),
		Max:    strconv.FormatInt(f.To.UnixMilli(), 10),
		Count:  int64(f.Limit * 4),
		Offset: 0,
	}
	raws, err := s.client.ZRevRangeByScore(ctx, echoZSet, opts).Result()
	if err != nil {
		return nil, err
	}
	return decodeAndFilter(raws, f, f.Limit)
}

// Close releases the connection pool.
func (s *RedisStore) Close() error { return s.client.Close() }

// Ping verifies Redis reachability for health checks.
func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func decodeAndFilter(raws []string, f model.IceFilter, limit int) ([]model.Envelope, error) {
	out := make([]model.Envelope, 0, len(raws))
	for _, raw := range raws {
		var env model.Envelope
		if err := gob.NewDecoder(bytes.NewReader([]byte(raw))).Decode(&env); err != nil {
			continue // skip corrupt cached entries
		}
		if f.HasBBox {
			cov := model.BBox{
				MinLat: env.Analysis.MinLat, MaxLat: env.Analysis.MaxLat,
				MinLon: env.Analysis.MinLon, MaxLon: env.Analysis.MaxLon,
			}
			if !f.BBox.Intersects(cov) {
				continue
			}
		}
		out = append(out, env)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
